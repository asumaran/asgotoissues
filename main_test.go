package main

import (
	"bytes"
	tea "charm.land/bubbletea/v2"
	"path/filepath"
	"strconv"
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
	// A stack with GitHub owners has a pulls source after its trackers,
	// whether or not it lists GitHub issues.
	if ids := sourceIDs(sourcesOf(stacks)); ids != "both/github both/jira both/pulls mine/github mine/pulls plain/jira plain/pulls" {
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
	s, k, m := corpora(entries, trackerTitle)
	rows := testRows(entries, "", s, k, m)
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
	s, k, m := corpora(entries, trackerTitle)
	for q, want := range map[string]string{
		"2098":      "PLAT-2098",
		"plat-2099": "PLAT-2099",
		"shop":      "SHOP-602",
		"sub-task":  "SHOP-602",
		"boundary":  "PLAT-2098",
	} {
		rows := testRows(entries, q, s, k, m)
		b := firstIssue(rows) // ranked: the best match is the first row
		if b < 0 {
			t.Errorf("query %q: no match", q)
			continue
		}
		if got := rows[b].e.it.Key; got != want {
			t.Errorf("query %q: best = %s, want %s", q, got, want)
		}
	}
	rows := testRows(entries, "zzzzzz", s, k, m)
	if len(rows) != 0 {
		t.Errorf("non-matching query kept %d rows", len(rows))
	}
}

func TestNavigationSkipsHeaders(t *testing.T) {
	entries := testEntries()
	s, k, m := corpora(entries, trackerTitle)
	rows := testRows(entries, "", s, k, m)
	cur := firstIssue(rows)
	if rows[cur].e.it.Key != "PLAT-2099" {
		t.Fatalf("first = %s", rows[cur].e.it.Key)
	}
	nav, down := defaultListNav(), tea.KeyPressMsg{Code: tea.KeyDown}
	isIssue := func(i int) bool { return rows[i].kind == "issue" }
	cur = nav.move(down, cur, len(rows), 10, isIssue)
	cur = nav.move(down, cur, len(rows), 10, isIssue) // jumps over the "mo" header
	if rows[cur].e.it.Key != "SHOP-602" {
		t.Errorf("after two downs = %s", rows[cur].e.it.Key)
	}
	if nav.move(down, cur, len(rows), 10, isIssue) != cur {
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
	s, k, m := corpora(entries, trackerTitle)
	var got []string
	for _, r := range testRows(entries, "indexable", s, k, m) {
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
	for _, r := range testRows(entries, "", s, k, m) {
		if r.kind == "issue" {
			got = append(got, r.e.it.Key)
		}
	}
	if strings.Join(got, " ") != "WORK-9 WORK-3 HOME-1" {
		t.Errorf("unfiltered rows = %v", got)
	}
}

// dumpInputs is what main hands runDump, built from testEntries: the stacks
// and the cache. The config and the state dir are temporary, so the header
// names no real file and nothing is read from the real ones.
func dumpInputs(t *testing.T) ([]stack, issueCache) {
	t.Helper()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	t.Setenv("ASGOTOISSUES_CONFIG", filepath.Join(t.TempDir(), "asdev.local.md"))
	cache := issueCache{FetchedAt: time.Now()}
	for _, e := range testEntries() {
		cache.Issues = append(cache.Issues, e.it)
	}
	return []stack{jiraStack("dh"), jiraStack("mo")}, cache
}

// TestRunDump covers -dump on a fresh cache (no refresh, so no network): the
// config and the counts on top, a line per tracker, and every ticket once
// under its stack.
func TestRunDump(t *testing.T) {
	stacks, cache := dumpInputs(t)
	var out bytes.Buffer
	runDump(&out, stacks, cache, false, "", "", "", false)
	got := out.String()
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	// 3 summary lines, 2 trackers, the order, 2 stack headers, 3 tickets of
	// two lines each.
	if len(lines) != 14 {
		t.Fatalf("got %d lines, want 14:\n%s", len(lines), got)
	}
	if lines[0] != "config: "+configPath() {
		t.Errorf("first line %q, want the config path", lines[0])
	}
	if !strings.HasPrefix(lines[1], "cache: 3 issues, 0 PRs, fetched ") || lines[2] != "stacks: 2" {
		t.Errorf("summary %q, want the cache and the stacks", lines[1:3])
	}
	if lines[5] != "order: created" || lines[6] != "▼ dh" || lines[11] != "▼ mo" {
		t.Errorf("order %q, stack headers %q and %q, want created, dh and mo", lines[5], lines[6], lines[11])
	}
	for _, e := range testEntries() {
		if n := strings.Count(got, strings.ToLower(e.it.Key)+" "); n != 1 {
			t.Errorf("%s is listed %d times, want once:\n%s", e.it.Key, n, got)
		}
	}
	if lines[7] != "   plat-2099 Audit: enforce authz" || !strings.HasPrefix(lines[8], "       uat · ") {
		t.Errorf("ticket rows %q, want the key and the title, then the status", lines[7:9])
	}
}

// TestRunDumpQuery covers -dump -query: the matches with their scores, best
// first, instead of the grouped list.
func TestRunDumpQuery(t *testing.T) {
	stacks, cache := dumpInputs(t)
	var out bytes.Buffer
	runDump(&out, stacks, cache, false, "boundary audit", "", "", false)
	got := out.String()
	_, matches, ok := strings.Cut(got, "query \"boundary audit\":\n")
	if !ok {
		t.Fatalf("no query line:\n%s", got)
	}
	if want := "PLAT-2098 Audit trail at the service boundary"; strings.Count(matches, "\n") != 1 || !strings.HasSuffix(matches, "  "+want+"\n") {
		t.Errorf("matches %q, want only %q", matches, want)
	}

	out.Reset()
	runDump(&out, stacks, cache, false, "audit", "", "", false)
	got = out.String()
	_, matches, _ = strings.Cut(got, "query \"audit\":\n")
	lines := strings.Split(strings.TrimRight(matches, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d matches, want the 2 audit tickets:\n%s", len(lines), got)
	}
	last := 0
	for i, l := range lines {
		score, rest, _ := strings.Cut(strings.TrimSpace(l), "  ")
		n, err := strconv.Atoi(score)
		if err != nil || !strings.HasPrefix(rest, "PLAT-20") {
			t.Errorf("match %d is %q, want a score and a PLAT ticket", i, l)
		}
		if i > 0 && n > last {
			t.Errorf("score %d after %d, want the best first:\n%s", n, last, got)
		}
		last = n
	}
	// The matches replace the list: no other ticket, no stack header, no row.
	for _, not := range []string{"SHOP-602", "\ndh\n", "[UAT]", "(updated "} {
		if strings.Contains(got, not) {
			t.Errorf("the query dump has %q, a piece of the full listing:\n%s", not, got)
		}
	}
}

// testRows is buildRows with no PRs and the default options: the rows of a
// list of tickets alone.
func testRows(entries []*entry, q string, summaries, keys, metas []string) []row {
	return buildRows(entries, nil, nil, opts(orderCreated, prsAll, false, nil), q, summaries, keys, metas)
}

// TestRunDumpTree: -dump prints the tree: the indent, a ghost marked, what
// a ticket's PRs need, the PRs under their ticket with their flags, the
// order line and the pulls source; -order beats the saved setting and is
// not saved.
func TestRunDumpTree(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	t.Setenv("ASGOTOISSUES_CONFIG", filepath.Join(t.TempDir(), "asdev.local.md"))
	saveSetting(stateDir(), "order", "key")
	saveFolds(stateDir(), map[string]bool{"stack:work": true}, 1) // the popup's folds never hide a ticket from the dump
	stacks := []stack{{Name: "work", Trackers: []string{kindJira}, Orgs: []string{"me"}, BaseURL: "https://w"}, {Name: "home", Trackers: []string{kindGitHub}, Orgs: []string{"me"}}}
	cache := issueCache{FetchedAt: time.Now(), Issues: treeIssues(), Pulls: treePulls()}
	var out bytes.Buffer
	runDump(&out, stacks, cache, false, "", "", "", false)
	got := out.String()
	for _, want := range []string{
		"cache: 6 issues, 7 PRs, fetched ",
		"  work             pulls  me\n",
		"order: key\n",
		"▼ work\n ▼ w-1 epic\n │     open · not yours · ",
		" └── ▼ w-10 story\n     │     in progress · ",
		"     │   ○ front#3 three\n     │     approved · ",
		"     └── ▼ w-11 sub-task\n               open · ",
		"             ○ front#1 one\n               in review · conflicts · ",
		"             ○ infra#2 two\n               merged · ",
		" ▼ w-12 alone\n       blocked · ",
		" ▼ PRs without a ticket\n     ○ front#4 four\n       in review · ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dump lacks %q:\n%s", want, got)
		}
	}
	out.Reset()
	runDump(&out, stacks, cache, false, "", "", "attention", false)
	if !strings.Contains(out.String(), "order: attention\n") || loadSetting(stateDir(), "order") != "key" {
		t.Errorf("-order is used and not saved:\n%s", out.String())
	}
}
