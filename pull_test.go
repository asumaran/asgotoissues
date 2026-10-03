package main

import (
	"strings"
	"testing"
	"time"
)

// TestPullRefs: the branch and the title link strongly, as do the closing
// keywords and the references GitHub resolved; a key in the body, as text or
// as a /browse/ link, is weak. #N is the PR's own repo, other/repo#N is that
// one and never #N too, and a ref found twice comes once.
func TestPullRefs(t *testing.T) {
	body := "Depends on ESHOP-2707 (https://x.atlassian.net/browse/ESHOP-2560).\n\nFixes #12, closes acme/other#3 and resolves: #12.\nSee ESHOP-2707 again."
	strong, weak := pullRefs("Me/Repo", "feat/eshop-1270-headers", "feat(eshop): ESHOP-1270 and PLAT2-9", body, []string{"Me/Repo#12", "acme/Other#3"})
	if got := strings.Join(strong, " "); got != "ESHOP-1270 PLAT2-9 me/repo#12 acme/other#3" {
		t.Errorf("strong = %q", got)
	}
	if got := strings.Join(weak, " "); got != "ESHOP-2560 ESHOP-2707" {
		t.Errorf("weak = %q", got)
	}
	if s, w := pullRefs("me/repo", "deploy/eshop-front-prod-hnx5", "Deploy config", "step #1 of the rollout", nil); len(s) != 0 || len(w) != 0 {
		t.Errorf("a branch without a key and a bare #N in the body link nothing: %v %v", s, w)
	}
	if s, _ := pullRefs("me/repo", "fix_eshop-270", "", "", nil); strings.Join(s, " ") != "ESHOP-270" {
		t.Errorf("an underscore before the key is no boundary: %v", s)
	}
}

// TestIssueRef: what a PR names and what an issue is called compare equal.
func TestIssueRef(t *testing.T) {
	gh := issue{Key: "tool#12", Source: kindGitHub, Project: "Me/Tool"}
	if gh.ref() != "me/tool#12" || issueRef("Me/Tool", "#12") != gh.ref() {
		t.Errorf("github ref = %q, issueRef = %q", gh.ref(), issueRef("Me/Tool", "#12"))
	}
	if (issue{Key: "plat-9"}).ref() != "PLAT-9" {
		t.Errorf("a jira ref is the key upper cased")
	}
}

// TestPullFlags: a PR's state, what it needs and the facts. A thing
// someone must act on is bad on my PR and a warning on another person's; a
// review asked of me is bad whoever's PR it is; an approval is ready only
// while nothing blocks it and no check runs; a PR waiting for a review is a
// warning.
func TestPullFlags(t *testing.T) {
	text := func(flags []prFlag) string {
		var parts []string
		for _, f := range flags {
			parts = append(parts, f.text)
		}
		return strings.Join(parts, " · ")
	}
	merged := &pull{Number: 7, State: prMerged}
	open := &pull{Number: 8, State: prOpen}
	for name, tc := range map[string]struct {
		p     pull
		base  *pull
		want  string // the state, then the flags
		level int
	}{
		"merged":                  {pull{State: prMerged, Mine: true, Mergeable: "CONFLICTING"}, nil, "merged", levelNone},
		"closed":                  {pull{State: prClosed}, nil, "closed", levelNone},
		"mine with conflicts":     {pull{State: prOpen, Mine: true, Mergeable: "CONFLICTING", ReviewDecision: "APPROVED"}, nil, "approved · conflicts", levelBad},
		"theirs with conflicts":   {pull{State: prOpen, MergeState: "DIRTY", Author: "ana"}, nil, "in review · conflicts · by ana", levelWarn},
		"changes requested":       {pull{State: prOpen, Mine: true, ReviewDecision: "CHANGES_REQUESTED"}, nil, "changes requested", levelBad},
		"changes without a rule":  {pull{State: prOpen, Mine: true, ChangesRequested: 1, Approvals: 1}, nil, "changes requested", levelBad},
		"ci failed":               {pull{State: prOpen, Mine: true, Checks: "FAILURE"}, nil, "in review · ci failed", levelBad},
		"to answer":               {pull{State: prOpen, Mine: true, ToAnswer: true}, nil, "in review · to answer", levelBad},
		"theirs to answer":        {pull{State: prOpen, ToAnswer: true}, nil, "in review", levelNone},
		"review asked of me":      {pull{State: prOpen, ReviewRequested: true, ReviewDecision: "REVIEW_REQUIRED"}, nil, "in review · review requested", levelBad},
		"base merged":             {pull{State: prOpen, Mine: true, Base: "x"}, merged, "in review · base merged", levelBad},
		"behind, required":        {pull{State: prOpen, Mine: true, Base: "main", MergeState: "BEHIND", ReviewDecision: "APPROVED"}, nil, "approved · behind main", levelOK},
		"behind, a fact":          {pull{State: prOpen, Mine: true, Base: "master", Behind: 26}, nil, "in review · behind master", levelNone},
		"stacked":                 {pull{State: prOpen, Mine: true, Draft: true}, open, "draft · stacked on #8", levelNone},
		"approved":                {pull{State: prOpen, Mine: true, ReviewDecision: "APPROVED", Checks: "SUCCESS"}, nil, "approved", levelOK},
		"approved, ci pending":    {pull{State: prOpen, Mine: true, ReviewDecision: "APPROVED", Checks: "PENDING"}, nil, "approved · ci pending", levelWarn},
		"approval without a rule": {pull{State: prOpen, Mine: true, Approvals: 2}, nil, "approved", levelOK},
		"awaiting review":         {pull{State: prOpen, Mine: true, ReviewDecision: "REVIEW_REQUIRED"}, nil, "in review", levelWarn},
		"reviewers pending":       {pull{State: prOpen, Mine: true, ReviewRequests: 2}, nil, "in review", levelWarn},
		"draft":                   {pull{State: prOpen, Mine: true, Draft: true, ReviewDecision: "REVIEW_REQUIRED"}, nil, "draft", levelNone},
		"no rule, no request":     {pull{State: prOpen, Mine: true, Checks: "SUCCESS"}, nil, "in review", levelNone},
	} {
		all := append([]prFlag{tc.p.state(tc.base)}, tc.p.flags(tc.base)...)
		if text(all) != tc.want || tc.p.attention(tc.base) != tc.level {
			t.Errorf("%s: %q (level %d), want %q (level %d)", name, text(all), tc.p.attention(tc.base), tc.want, tc.level)
		}
	}
}

func TestOverlaySharedPRs(t *testing.T) {
	t0 := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	shared := emptySharedPRs()
	shared.Pulls["https://github.com/o/r/pull/20"] = sharedPR{Number: 20, State: "merged", Title: "merged now", UpdatedAt: t0.Add(time.Hour)}
	shared.Pulls["https://github.com/o/r/pull/30"] = sharedPR{Number: 30, State: "draft", Title: "older", UpdatedAt: t0.Add(-time.Hour)}
	pulls := []pull{
		{URL: "https://github.com/o/r/pull/10", Number: 10, State: prMerged, Head: "feature", Updated: t0},
		{URL: "https://github.com/o/r/pull/20", Number: 20, State: prOpen, Draft: true, Head: "feature", Title: "open", Updated: t0},
		{URL: "https://github.com/o/r/pull/30", Number: 30, State: prOpen, Title: "mine is newer", Updated: t0},
	}
	got := overlaySharedPRs(pulls, shared)
	if got[0].State != prMerged || got[0].Number != 10 {
		t.Errorf("a PR the cache does not know is untouched, even on the same branch: %+v", got[0])
	}
	if got[1].State != prMerged || got[1].Draft || got[1].Title != "merged now" {
		t.Errorf("a newer shared version wins: %+v", got[1])
	}
	if got[2].Draft || got[2].Title != "mine is newer" {
		t.Errorf("an older shared version loses: %+v", got[2])
	}
	if pulls[1].State != prOpen || !pulls[1].Draft {
		t.Error("the overlay is a display layer: the cached slice is not touched")
	}
}
