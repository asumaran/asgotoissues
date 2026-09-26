package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func ghPullNodeJSON(owner, repo string, n int, title, head, state, author string, extra string) string {
	if extra != "" {
		extra = "," + extra
	}
	return fmt.Sprintf(`{"number":%d,"title":%q,"url":"https://github.com/%s/%s/pull/%d","body":"See https://j.example/browse/PLAT-7\n\nfixes #%d",`+
		`"state":%q,"isDraft":false,"createdAt":"2026-09-01T10:00:00Z","updatedAt":"2026-09-1%dT10:00:00Z",`+
		`"headRefName":%q,"baseRefName":"main","headRefOid":"h%d","baseRefOid":"b","author":{"login":%q},`+
		`"repository":{"name":%q,"nameWithOwner":"%s/%s"},"reviewDecision":"REVIEW_REQUIRED","mergeable":"MERGEABLE","mergeStateStatus":"BLOCKED",`+
		`"reviewRequests":{"totalCount":1},"latestReviews":{"nodes":[{"state":"APPROVED"},{"state":"COMMENTED"}]},`+
		`"statusCheckRollup":{"state":"SUCCESS"},"closingIssuesReferences":{"nodes":[{"number":%d,"repository":{"nameWithOwner":"%s/%s"}}]}%s}`,
		n, title, owner, repo, n, n, state, n%10, head, n, author, repo, owner, repo, n+100, owner, repo, extra)
}

const (
	qOpen   = "is:pr is:open archived:false involves:@me user:acme user:Me"
	qMerged = "is:pr is:merged archived:false author:@me updated:>=2026-08-25 user:acme user:Me"
	qAsked  = "is:pr is:open archived:false review-requested:@me user:acme user:Me"
)

// TestPullsFetchSearchesAndMaps: the three searches under the stack's
// owners, paged, the nodes mapped, a repeat dropped, mine told by the
// viewer's login, the review asked of me marked, the references read.
func TestPullsFetchSearchesAndMaps(t *testing.T) {
	prev := pullsNow
	pullsNow = func() time.Time { return time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { pullsNow = prev })
	asked := fakeGh(t, map[string]string{
		"":    `{"data":{"viewer":{"login":"Me"}}}`,
		qOpen: ghPage("c1", ghPullNodeJSON("acme", "shop", 1, "PLAT-1 checkout", "feat/plat-1", "OPEN", "me", "")),
		qOpen + " @c1": ghPage("",
			ghPullNodeJSON("acme", "shop", 2, "their fix", "fix/x", "OPEN", "them", `"isDraft":true`),
			ghPullNodeJSON("acme", "shop", 2, "their fix", "fix/x", "OPEN", "them", ""), // a repeat is dropped
			"{}"),
		qMerged: ghPage("", ghPullNodeJSON("Me", "shop", 3, "done", "feat/plat-3", "MERGED", "me", "")),
		qAsked:  `{"data":{"search":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[{"url":"https://github.com/acme/shop/pull/2"}]}}}`,
	})
	p := pullsProvider{stack{Name: "work", Orgs: []string{"acme", "Me"}}}
	res, err := p.fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"", qOpen, qOpen + " @c1", qMerged, qAsked}; strings.Join(*asked, "|") != strings.Join(want, "|") {
		t.Errorf("queries = %q, want %q", *asked, want)
	}
	var lines []string
	for _, pr := range res.pulls {
		lines = append(lines, fmt.Sprintf("%s %s mine=%v asked=%v refs=%v weak=%v", pr.Key, strings.ToLower(pr.State), pr.Mine, pr.ReviewRequested, pr.Refs, pr.WeakRefs))
	}
	want := "acme/shop#1 open mine=true asked=false refs=[PLAT-1 acme/shop#1 acme/shop#101] weak=[PLAT-7]|" +
		"acme/shop#2 open mine=false asked=true refs=[acme/shop#2 acme/shop#102] weak=[PLAT-7]|" +
		"Me/shop#3 merged mine=true asked=false refs=[PLAT-3 me/shop#3 me/shop#103] weak=[PLAT-7]" // the key spells the owner as GitHub does, the ref folds it
	if got := strings.Join(lines, "|"); got != want {
		t.Errorf("pulls =\n%s\nwant\n%s", strings.ReplaceAll(got, "|", "\n"), strings.ReplaceAll(want, "|", "\n"))
	}
	pr := res.pulls[0]
	if pr.Stack != "work" || pr.Repo != "acme/shop" || pr.Head != "feat/plat-1" || pr.Base != "main" || pr.Author != "me" ||
		pr.ReviewDecision != "REVIEW_REQUIRED" || pr.Mergeable != "MERGEABLE" || pr.MergeState != "BLOCKED" || pr.Checks != "SUCCESS" ||
		pr.Approvals != 1 || pr.ReviewRequests != 1 || pr.HeadOID != "h1" || !pr.Created.Equal(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("mapping: %+v", pr)
	}
	if !res.pulls[1].Draft {
		t.Errorf("the draft flag is read")
	}
}

// TestPullsFetchReadsPartialErrors: gh exits 1 on a GraphQL error but still
// prints the data it got; a page that decoded is a page.
func TestPullsFetchReadsPartialErrors(t *testing.T) {
	prev := ghRun
	ghRun = func(_ context.Context, args ...string) ([]byte, error) {
		for _, a := range args {
			if strings.HasPrefix(a, "q=") {
				page := ghPage("", ghPullNodeJSON("acme", "shop", 9, "PLAT-9", "feat/plat-9", "OPEN", "me", ""))
				return []byte(`{"errors":[{"message":"a field failed"}],` + page[1:]), errors.New("gh: a field failed")
			}
		}
		return []byte(`{"data":{"viewer":{"login":"me"}}}`), nil
	}
	t.Cleanup(func() { ghRun = prev })
	res, err := pullsProvider{stack{Name: "work", Orgs: []string{"acme"}}}.fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.pulls) != 1 || res.pulls[0].Key != "shop#9" {
		t.Errorf("pulls = %+v", res.pulls)
	}
	// Without data the error is the error, and it names the stack's PRs.
	ghRun = func(context.Context, ...string) ([]byte, error) {
		return []byte(`{"errors":[{"message":"nope"}]}`), errors.New("gh: nope")
	}
	if _, err := (pullsProvider{stack{Name: "work", Orgs: []string{"acme"}}}).fetch(context.Background()); err == nil || err.Error() != "work PRs: gh: nope" {
		t.Errorf("err = %v", err)
	}
}

func TestQualifyClashingPullKeys(t *testing.T) {
	pulls := []pull{
		{Key: "docs#1", Number: 1, Repo: "acme/docs"},
		{Key: "docs#2", Number: 2, Repo: "me/docs"},
		{Key: "tool#3", Number: 3, Repo: "me/tool"},
	}
	qualifyClashingPullKeys(pulls)
	if got := pulls[0].Key + " " + pulls[1].Key + " " + pulls[2].Key; got != "acme/docs#1 me/docs#2 tool#3" {
		t.Errorf("keys = %s", got)
	}
}
