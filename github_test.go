package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func ghNode(owner, repo string, n int, title string, labels ...string) string {
	var ls []string
	for _, l := range labels {
		ls = append(ls, fmt.Sprintf(`{"name":%q}`, l))
	}
	return fmt.Sprintf(`{"number":%d,"title":%q,"url":"https://github.com/%s/%s/issues/%d","body":"a _body_ with snake_case_name\n\n* bullet",`+
		`"createdAt":"2026-09-0%dT10:00:00Z","updatedAt":"2026-09-10T10:00:00Z",`+
		`"repository":{"name":%q,"nameWithOwner":"%s/%s"},"labels":{"nodes":[%s]},"milestone":null}`,
		n, title, owner, repo, n, n, repo, owner, repo, strings.Join(ls, ","))
}

func ghPage(cursor string, nodes ...string) string {
	return fmt.Sprintf(`{"data":{"search":{"pageInfo":{"hasNextPage":%t,"endCursor":%q},"nodes":[%s]}}}`,
		cursor != "", cursor, strings.Join(nodes, ","))
}

// fakeGh answers each search query (the `q=` argument, plus the cursor when
// one is sent) with a canned page and records what was asked.
func fakeGh(t *testing.T, pages map[string]string) *[]string {
	t.Helper()
	var asked []string
	prev := ghRun
	ghRun = func(_ context.Context, args ...string) ([]byte, error) {
		q := ""
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "q="); ok {
				q = v
			}
			if v, ok := strings.CutPrefix(a, "after="); ok {
				q += " @" + v
			}
		}
		asked = append(asked, q)
		page, ok := pages[q]
		if !ok {
			return nil, fmt.Errorf("gh: unexpected query %q", q)
		}
		return []byte(page), nil
	}
	t.Cleanup(func() { ghRun = prev })
	return &asked
}

const qAssigned = "is:issue is:open archived:false assignee:@me user:acme user:Me"

func TestGitHubFetchListsAssignedIssues(t *testing.T) {
	asked := fakeGh(t, map[string]string{
		qAssigned: ghPage("c1", ghNode("acme", "shop", 1, "assigned in the org")),
		qAssigned + " @c1": ghPage("",
			ghNode("me", "tool", 2, "assigned in my repo", "in progress"),
			ghNode("me", "tool", 2, "assigned in my repo", "in progress"), // a repeat is dropped
			ghNode("me", "tool", 3, "blocked one", "bug", "Blocked"),
			"{}"), // what a non-issue node decodes to
	})
	p := githubProvider{stack{Name: "home", Orgs: []string{"acme", "Me"}}}
	got, err := p.fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{qAssigned, qAssigned + " @c1"}; strings.Join(*asked, "|") != strings.Join(want, "|") {
		t.Errorf("queries = %q, want %q", *asked, want)
	}
	var lines []string
	for _, it := range got {
		lines = append(lines, it.Key+" "+it.State+" "+it.Project)
	}
	want := "shop#1 todo acme/shop|tool#2 doing me/tool|tool#3 blocked me/tool"
	if strings.Join(lines, "|") != want {
		t.Errorf("issues = %q, want %q", lines, want)
	}
	it := got[2]
	if it.Stack != "home" || it.Source != kindGitHub || it.Status != "open" || it.URL != "https://github.com/me/tool/issues/3" {
		t.Errorf("mapping: %+v", it)
	}
	if strings.Join(it.metaParts(), " · ") != "me/tool · bug, Blocked" {
		t.Errorf("meta = %q", it.metaParts())
	}
	if !it.Created.Equal(time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("created = %v", it.Created)
	}
	if md := it.markdown(); md != it.Description || !strings.Contains(md, "snake_case_name") || !strings.Contains(md, "\n* bullet") {
		t.Errorf("a markdown body must not go through the wiki converter: %q", md)
	}
}

func TestGitHubFetchErrorNamesTheStack(t *testing.T) {
	prev := ghRun
	ghRun = func(context.Context, ...string) ([]byte, error) { return nil, errors.New("gh: not logged in") }
	t.Cleanup(func() { ghRun = prev })
	_, err := githubProvider{stack{Name: "work", Orgs: []string{"acme"}}}.fetch(context.Background())
	if err == nil || err.Error() != "work: gh: not logged in" {
		t.Errorf("err = %v", err)
	}
}

func TestQualifyClashingKeys(t *testing.T) {
	issues := []issue{
		{Key: "docs#1", Project: "acme/docs"},
		{Key: "docs#2", Project: "me/docs"},
		{Key: "tool#3", Project: "me/tool"},
	}
	qualifyClashingKeys(issues)
	got := issues[0].Key + " " + issues[1].Key + " " + issues[2].Key
	if got != "acme/docs#1 me/docs#2 tool#3" {
		t.Errorf("keys = %s", got)
	}
	if issues[0].number() != "1" {
		t.Errorf("number of %q = %q", issues[0].Key, issues[0].number())
	}
}

func TestIssuesCachedBeforeStateStillResolve(t *testing.T) {
	for _, tc := range []struct {
		it   issue
		want string
	}{
		{issue{Status: "In Review", StatusCat: "In Progress"}, stateDoing},
		{issue{Status: "Blocked by legal", StatusCat: "In Progress"}, stateBlocked},
		{issue{Status: "Backlog", StatusCat: "To Do"}, stateTodo},
		{issue{Status: "open", State: stateDoing}, stateDoing},
	} {
		if got := tc.it.state(); got != tc.want {
			t.Errorf("state(%+v) = %s, want %s", tc.it, got, tc.want)
		}
	}
	old := issue{Key: "PLAT-1", Type: "Task", Priority: "High", ParentKey: "PLAT-0", Description: "h1. Title"}
	if got := strings.Join(old.metaParts(), " · "); got != "Task · High · ↳ PLAT-0" {
		t.Errorf("meta = %q", got)
	}
	if old.sourceKind() != kindJira || !strings.HasPrefix(old.markdown(), "# Title") {
		t.Errorf("an issue without Source or BodyFormat is a Jira one: %q", old.markdown())
	}
}

func TestRankingFindsGitHubKeys(t *testing.T) {
	entries := buildEntries([]issue{
		{Key: "tool#12", Stack: "home", Project: "me/tool", Summary: "twelve", Meta: []string{"me/tool", "bug"}},
		{Key: "shop#120", Stack: "home", Project: "me/shop", Summary: "one twenty"},
	})
	summaries, keys, metas := corpora(entries)
	top := func(q string) string {
		for _, r := range buildRows(entries, q, summaries, keys, metas) {
			if r.kind == "issue" {
				return r.e.it.Key
			}
		}
		return ""
	}
	for q, want := range map[string]string{"12": "tool#12", "shop#1": "shop#120", "bug": "tool#12"} {
		if got := top(q); got != want {
			t.Errorf("top(%q) = %s, want %s", q, got, want)
		}
	}
}
