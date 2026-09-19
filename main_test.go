package main

import (
	"strings"
	"testing"
	"time"
)

const sampleConfig = `---
stacks:
  acme:
    jira:
      type: cloud
      base_url: https://acme.atlassian.net/
      email: me@example.com
      api_token_env: JIRA_TOKEN_ACME
    github:
      org: acme
  globex:
    jira:
      base_url: https://globex.atlassian.net
      email: me@globex.example
      api_token_env: JIRA_TOKEN_GLOBEX
  ghonly:
    github:
      org: someone
  legacy:
    jira:
      type: server
      base_url: https://jira.example.internal
      api_token_env: JIRA_TOKEN_LEGACY
---

# notes below the front matter are ignored
`

func TestParseStacksOrderAndDefaults(t *testing.T) {
	stacks, err := parseStacks(sampleConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(stacks) != 3 {
		t.Fatalf("len = %d, want 3 (GitHub-only stack skipped)", len(stacks))
	}
	wantNames := []string{"acme", "globex", "legacy"}
	for i, s := range stacks {
		if s.Name != wantNames[i] {
			t.Errorf("stack[%d] = %s, want %s (config order)", i, s.Name, wantNames[i])
		}
	}
	if stacks[0].BaseURL != "https://acme.atlassian.net" {
		t.Errorf("trailing slash not trimmed: %q", stacks[0].BaseURL)
	}
	if stacks[1].Type != "cloud" {
		t.Errorf("type default = %q, want cloud", stacks[1].Type)
	}
	if stacks[2].Type != "server" || stacks[2].host() != "jira.example.internal" {
		t.Errorf("server stack parsed wrong: %+v", stacks[2])
	}
}

func TestParseStacksPlainYAMLAndErrors(t *testing.T) {
	if _, err := parseStacks("stacks:\n  a:\n    jira:\n      base_url: https://a.atlassian.net\n"); err != nil {
		t.Errorf("plain yaml without fences should parse: %v", err)
	}
	if _, err := parseStacks("---\nstacks: {}\n---\n"); err == nil {
		t.Errorf("empty stacks should error")
	}
	if _, err := parseStacks("---\nstacks:\n  a:\n    github:\n      org: x\n---\n"); err == nil {
		t.Errorf("a config where no stack lists a tracker should error")
	}
}

func TestParseStacksIssuesKey(t *testing.T) {
	stacks, err := parseStacks(`stacks:
  both:
    issues: [GitHub, jira, github]
    jira:
      base_url: https://both.atlassian.net
    github:
      org: acme
      orgs: [acme, Octo]
  mine:
    issues: [github]
    github:
      org: me
  off:
    issues: []
    jira:
      base_url: https://off.atlassian.net
  plain:
    jira:
      base_url: https://plain.atlassian.net
    github:
      org: ignored
`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range stacks {
		got[s.Name] = strings.Join(s.Trackers, ",") + " " + strings.Join(s.Orgs, ",")
	}
	want := map[string]string{
		"both":  "github,jira acme,Octo", // order of `issues:`, duplicates dropped
		"mine":  "github me",
		"plain": "jira ignored", // no `issues:` key: Jira only, as before
	}
	if len(got) != len(want) {
		t.Errorf("stacks = %v, want %v (an empty `issues:` turns a stack off)", got, want)
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s = %q, want %q", name, got[name], w)
		}
	}
	if ids := sourceIDs(sourcesOf(stacks)); ids != "both/github both/jira mine/github plain/jira" {
		t.Errorf("sources = %s", ids)
	}

	for name, doc := range map[string]string{
		"github without an org":   "stacks:\n  a:\n    issues: [github]\n",
		"jira without a base_url": "stacks:\n  a:\n    issues: [jira]\n    github:\n      org: x\n",
		"unknown tracker":         "stacks:\n  a:\n    issues: [linear]\n",
	} {
		if _, err := parseStacks(doc); err == nil || !strings.Contains(err.Error(), "a") {
			t.Errorf("%s: err = %v, want an error naming the stack", name, err)
		}
	}
}

func sourceIDs(sources []source) string {
	var ids []string
	for _, s := range sources {
		ids = append(ids, s.id())
	}
	return strings.Join(ids, " ")
}

func TestMergeStacksFallsBackPerSource(t *testing.T) {
	stacks := []stack{{Name: "a", Trackers: []string{kindJira, kindGitHub}}}
	cached := []issue{
		{Key: "A-1", Stack: "a", Created: time.Unix(10, 0)}, // older cache: no Source, reads as jira
		{Key: "repo#1", Stack: "a", Source: kindGitHub, Created: time.Unix(20, 0)},
	}
	fresh := map[string][]issue{
		"a/github": {{Key: "repo#2", Stack: "a", Source: kindGitHub, Created: time.Unix(30, 0)}},
	}
	var keys []string
	for _, it := range mergeStacks(stacks, fresh, cached) {
		keys = append(keys, it.Key)
	}
	if got := strings.Join(keys, " "); got != "repo#2 A-1" {
		t.Errorf("merged = %s, want the fresh github issue and the cached jira one", got)
	}
}

func TestNetrcFind(t *testing.T) {
	oneLine := strings.Fields("machine a.atlassian.net login me@a password tok-a machine b.atlassian.net login me@b password tok-b")
	c, ok := netrcFind(oneLine, "b.atlassian.net")
	if !ok || c.user != "me@b" || c.secret != "tok-b" {
		t.Errorf("one-line lookup = %+v %v", c, ok)
	}
	multi := strings.Fields("machine a.atlassian.net\n  login me@a\n  password tok-a\ndefault\n  login x\n  password y")
	c, ok = netrcFind(multi, "a.atlassian.net")
	if !ok || c.user != "me@a" || c.secret != "tok-a" {
		t.Errorf("multi-line lookup = %+v %v", c, ok)
	}
	if _, ok := netrcFind(multi, "missing.example"); ok {
		t.Errorf("missing host should not resolve")
	}
}

func TestParseJiraTime(t *testing.T) {
	got, err := parseJiraTime("2026-08-17T11:40:18.035-0400")
	if err != nil {
		t.Fatal(err)
	}
	if got.UTC().Hour() != 15 || got.Minute() != 40 {
		t.Errorf("parsed = %s", got.UTC())
	}
	if _, err := parseJiraTime(""); err == nil {
		t.Errorf("empty should error")
	}
}

func jiraStack(name string) stack { return stack{Name: name, Trackers: []string{kindJira}} }

func TestMergeStacksFallsBackToCache(t *testing.T) {
	stacks := []stack{jiraStack("a"), jiraStack("b")}
	cached := []issue{
		{Key: "A-1", Stack: "a", Created: time.Unix(10, 0)},
		{Key: "B-1", Stack: "b", Created: time.Unix(20, 0)},
		{Key: "B-2", Stack: "b", Created: time.Unix(30, 0)},
	}
	fresh := map[string][]issue{
		"a/jira": {{Key: "A-2", Stack: "a", Created: time.Unix(5, 0)}, {Key: "A-3", Stack: "a", Created: time.Unix(50, 0)}},
	}
	got := mergeStacks(stacks, fresh, cached)
	keys := make([]string, len(got))
	for i, it := range got {
		keys[i] = it.Key
	}
	want := "A-3 A-2 B-2 B-1" // fresh a (newest created first), cached b kept
	if strings.Join(keys, " ") != want {
		t.Errorf("merged = %v, want %s", keys, want)
	}
}

func TestWikiToMarkdown(t *testing.T) {
	src := "h2. Context\n\nSee {{pkg/guard.ts}} and *bold* plus _em_ [docs|https://x.example/d] and [https://raw.example].\n\n{noformat}if (x) { y }\n{noformat}\n\n* one\n** nested\n# first\n\n{code:ts}\nconst a = 1;\n{code}\n\n||H1||H2||\n|c1|c2|\n\n{quote}\nquoted\n{quote}\n----\n[~accountid:123:abc]"
	got := wikiToMarkdown(src)
	for _, want := range []string{
		"## Context",
		"`pkg/guard.ts`",
		"**bold**",
		"*em*",
		"[docs](https://x.example/d)",
		"<https://raw.example>",
		"```\nif (x) { y }\n```",
		"- one\n  - nested\n1. first",
		"```ts\nconst a = 1;\n```",
		"| H1 | H2 |\n| --- | --- |\n| c1 | c2 |",
		"> quoted",
		"---",
		"@123:abc",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if wikiToMarkdown("") != "" {
		t.Errorf("empty input should stay empty")
	}
}

func TestWikiInlineDoesNotMangleMath(t *testing.T) {
	// A lone asterisk / underscore in prose must not become markup.
	got := inline("N >= 2 * M and snake_case_name stays")
	if strings.Contains(got, "**") || strings.Contains(got, "*case*") {
		t.Errorf("mangled: %q", got)
	}
}

func testEntries() []*entry {
	return buildEntries([]issue{
		{Key: "PLAT-2099", Stack: "dh", Summary: "Audit: enforce authz", Status: "UAT", Type: "Task", Project: "PLAT", Updated: time.Unix(300, 0)},
		{Key: "PLAT-2098", Stack: "dh", Summary: "Audit trail at the service boundary", Status: "UAT", Type: "Task", Project: "PLAT", Updated: time.Unix(200, 0)},
		{Key: "SHOP-602", Stack: "mo", Summary: "Create test fixtures", Status: "Open", Type: "Sub-task", Project: "SHOP", ParentKey: "SHOP-600", Updated: time.Unix(100, 0)},
	})
}

func TestBuildRowsGroupsByStack(t *testing.T) {
	entries := testEntries()
	s, k, m := corpora(entries)
	rows := buildRows(entries, "", s, k, m)
	kinds := []string{}
	for _, r := range rows {
		if r.kind == "header" {
			kinds = append(kinds, "H:"+r.stack)
		} else {
			kinds = append(kinds, r.e.it.Key)
		}
	}
	want := "H:dh PLAT-2099 PLAT-2098 H:mo SHOP-602"
	if strings.Join(kinds, " ") != want {
		t.Errorf("rows = %v, want %s", kinds, want)
	}
}

func TestRankingPrefersExactKeyAndNumber(t *testing.T) {
	entries := testEntries()
	s, k, m := corpora(entries)
	for q, want := range map[string]string{
		"2098":      "PLAT-2098",
		"plat-2099": "PLAT-2099",
		"shop":      "SHOP-602",
		"sub-task":  "SHOP-602",
		"boundary":  "PLAT-2098",
	} {
		rows := buildRows(entries, q, s, k, m)
		b := firstIssue(rows) // ranked: the best match is the first row
		if b < 0 {
			t.Errorf("query %q: no match", q)
			continue
		}
		if got := rows[b].e.it.Key; got != want {
			t.Errorf("query %q: best = %s, want %s", q, got, want)
		}
	}
	rows := buildRows(entries, "zzzzzz", s, k, m)
	if len(rows) != 0 {
		t.Errorf("non-matching query kept %d rows", len(rows))
	}
}

func TestNavigationSkipsHeaders(t *testing.T) {
	entries := testEntries()
	s, k, m := corpora(entries)
	rows := buildRows(entries, "", s, k, m)
	cur := firstIssue(rows)
	if rows[cur].e.it.Key != "PLAT-2099" {
		t.Fatalf("first = %s", rows[cur].e.it.Key)
	}
	cur = nextIssue(rows, cur, +1)
	cur = nextIssue(rows, cur, +1) // jumps over the "mo" header
	if rows[cur].e.it.Key != "SHOP-602" {
		t.Errorf("after two downs = %s", rows[cur].e.it.Key)
	}
	if nextIssue(rows, cur, +1) != cur {
		t.Errorf("down at the end should stay put")
	}
}

func TestNewerIssueFallsBackToKeyNumber(t *testing.T) {
	a := issue{Key: "PLAT-2099", Project: "PLAT"}
	b := issue{Key: "PLAT-2098", Project: "PLAT"}
	if !newerIssue(a, b) || newerIssue(b, a) {
		t.Errorf("without dates, higher key number should sort first")
	}
	c := issue{Key: "PLAT-1", Project: "PLAT", Created: time.Unix(100, 0)}
	if !newerIssue(c, a) {
		t.Errorf("a created date beats a missing one")
	}
}

func TestFilteringRanksRowsAndTheirStacks(t *testing.T) {
	entries := buildEntries([]issue{
		{Key: "WORK-9", Stack: "work", Summary: "Invoices need a dedicated export table"}, // i n d e x a b l e, scattered
		{Key: "WORK-3", Stack: "work", Summary: "Site TV indexable"},
		{Key: "HOME-1", Stack: "home", Summary: "Make the docs indexable by search"},
	})
	s, k, m := corpora(entries)
	var got []string
	for _, r := range buildRows(entries, "indexable", s, k, m) {
		if r.kind == "header" {
			got = append(got, "H:"+r.stack)
		} else {
			got = append(got, r.e.it.Key)
		}
	}
	// whole-word rows first, their own order between equals; the scattered match last in its stack
	if want := "H:work WORK-3 WORK-9 H:home HOME-1"; strings.Join(got, " ") != want {
		t.Errorf("rows = %v, want %s", got, want)
	}
	// without a query the list is the list: config order, nothing ranked
	got = got[:0]
	for _, r := range buildRows(entries, "", s, k, m) {
		if r.kind == "issue" {
			got = append(got, r.e.it.Key)
		}
	}
	if strings.Join(got, " ") != "WORK-9 WORK-3 HOME-1" {
		t.Errorf("unfiltered rows = %v", got)
	}
}
