package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestFoldsFileRoundtrips(t *testing.T) {
	dir := t.TempDir()
	collapsed, level := loadFolds(dir)
	if len(collapsed) != 0 || level != 0 {
		t.Errorf("no file: collapsed=%v level=%d, want none", collapsed, level)
	}
	saveFolds(dir, map[string]bool{"stack:alpha": true, "https://a.example/browse/PLAT-1": true}, 2)
	collapsed, level = loadFolds(dir)
	if level != 2 || len(collapsed) != 2 || !collapsed["stack:alpha"] || !collapsed["https://a.example/browse/PLAT-1"] {
		t.Errorf("loaded = %v, level %d", collapsed, level)
	}
	data, err := os.ReadFile(filepath.Join(dir, foldsFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `["https://a.example/browse/PLAT-1","stack:alpha"]`) {
		t.Errorf("keys should be sorted, for a deterministic file: %s", data)
	}

	if err := os.WriteFile(filepath.Join(dir, foldsFile), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if collapsed, level = loadFolds(dir); len(collapsed) != 0 || level != 0 {
		t.Errorf("broken file: collapsed=%v level=%d, want none", collapsed, level)
	}
	if err := os.WriteFile(filepath.Join(dir, foldsFile), []byte(`{"level":-1,"keys":["x"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if collapsed, level = loadFolds(dir); len(collapsed) != 0 || level != 0 {
		t.Errorf("negative level: collapsed=%v level=%d, want none", collapsed, level)
	}
}

// foldsFixtureModel is a list with a stack (alpha) that has a ticket with a
// PR and a PR of mine that names no ticket, and a stack (beta) whose root is
// a ghost epic with a sub-task under it: one of each row that can be folded.
func foldsFixtureModel(t *testing.T) model {
	t.Helper()
	issues := []issue{
		{Key: "PLAT-100", Stack: "alpha", URL: "https://a.example/browse/PLAT-100", Summary: "fix login flow",
			Status: "In Progress", State: stateDoing, Type: "Story", Created: time.Unix(300, 0), Updated: time.Unix(300, 0)},
		{Key: "PLAT-101", Stack: "alpha", URL: "https://a.example/browse/PLAT-101", Summary: "add the test",
			Status: "Open", Type: "Sub-task", ParentKey: "PLAT-100", ParentURL: "https://a.example/browse/PLAT-100",
			Created: time.Unix(200, 0), Updated: time.Unix(400, 0)},
		{Key: "SHOP-602", Stack: "beta", URL: "https://b.example/browse/SHOP-602", Summary: "create test fixtures",
			Status: "Open", ParentKey: "SHOP-600", ParentURL: "https://b.example/browse/SHOP-600", Created: time.Unix(150, 0)},
		{Key: "SHOP-600", Stack: "beta", URL: "https://b.example/browse/SHOP-600", Summary: "test the shop", Ghost: true},
	}
	pulls := []pull{
		{URL: "https://github.com/acme/front/pull/1", Number: 1, Repo: "acme/front", Key: "front#1", Stack: "alpha",
			Title: "PLAT-100 login", State: prOpen, Mine: true, Head: "feat/PLAT-100", Base: "main",
			Refs: []string{"PLAT-100"}, Updated: time.Unix(10, 0)},
		{URL: "https://github.com/acme/misc/pull/9", Number: 9, Repo: "acme/misc", Key: "misc#9", Stack: "alpha",
			Title: "unrelated cleanup", State: prOpen, Mine: true, Head: "chore/cleanup", Base: "main", Updated: time.Unix(5, 0)},
	}
	stacks := []stack{{Name: "alpha", Trackers: []string{kindJira}, Orgs: []string{"acme"}, BaseURL: "https://a.example"}, jiraStack("beta")}
	m := newModel(stacks, issueCache{FetchedAt: time.Now(), Issues: issues, Pulls: pulls}, false)
	res, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 30})
	return res.(model)
}

func findRow(rows []row, pred func(row) bool) int {
	for i, r := range rows {
		if pred(r) {
			return i
		}
	}
	return -1
}

// TestFoldsSurviveRestart: a stack, a ticket, a ghost, the group of PRs
// without a ticket and, by phase, a section, all folded by hand; a new
// model reading the same state dir comes back with every one of them still
// folded, and nothing else.
func TestFoldsSurviveRestart(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := foldsFixtureModel(t)

	fold := func(pred func(row) bool) {
		i := findRow(m.rows, pred)
		if i < 0 {
			t.Fatalf("no row matches the predicate; rows = %v", m.rows)
		}
		m.cursor = i
		m.fold()
	}
	fold(func(r row) bool { return r.kind == rowIssue && r.e.it.Key == "PLAT-100" })
	fold(func(r row) bool { return r.kind == rowIssue && r.e.it.Key == "SHOP-600" })
	fold(func(r row) bool { return r.kind == rowGroup && r.stack == "alpha" })
	fold(func(r row) bool { return r.kind == rowHeader && r.stack == "beta" })

	res, _ := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}) // group by phase
	m = res.(model)
	fold(func(r row) bool { return r.kind == rowSection && r.stack == "alpha" && r.text == phaseReview })

	want := []string{
		"https://a.example/browse/PLAT-100",
		"https://b.example/browse/SHOP-600",
		"group:alpha",
		"stack:beta",
		"section:alpha:" + phaseReview,
	}
	for _, k := range want {
		if !m.collapsed[k] {
			t.Errorf("expected %q folded before the restart: %v", k, m.collapsed)
		}
	}
	if len(m.collapsed) != len(want) {
		t.Errorf("unexpected extra folds: %v", m.collapsed)
	}

	fresh := foldsFixtureModel(t) // a new model, same state dir: a restart
	for _, k := range want {
		if !fresh.collapsed[k] {
			t.Errorf("the restart lost the fold %q: %v", k, fresh.collapsed)
		}
	}
	if len(fresh.collapsed) != len(want) {
		t.Errorf("the restart has extra folds: %v", fresh.collapsed)
	}
	if fresh.depthNow != 0 {
		t.Errorf("depthNow = %d, want 0 (tab/shift+tab was never pressed)", fresh.depthNow)
	}
}

// TestPartialRefreshDoesNotPruneFolds: a fold whose row no longer exists
// survives a refresh where a source failed.
func TestPartialRefreshDoesNotPruneFolds(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	stale := "https://a.example/browse/GONE"
	saveFolds(stateDir(), map[string]bool{stale: true}, 0)
	m := treeTestModel(t)
	if !m.collapsed[stale] {
		t.Fatalf("the saved fold should load: %v", m.collapsed)
	}
	m.refreshing, m.pending = true, 3
	res, _ := m.Update(sourceMsg{source: "alpha/jira", issues: m.cache.Issues[:2]})
	res, _ = res.(model).Update(sourceMsg{source: "alpha/pulls", pulls: nil})
	res, _ = res.(model).Update(sourceMsg{source: "beta/jira", err: errTest("boom")})
	got := res.(model)
	if !got.collapsed[stale] {
		t.Errorf("a partial refresh must not prune: %v", got.collapsed)
	}
	collapsed, _ := loadFolds(stateDir())
	if !collapsed[stale] {
		t.Errorf("the stale fold should still be on disk too: %v", collapsed)
	}
}

// TestCompleteRefreshPrunesFolds: a complete refresh drops a fold whose URL
// is gone and a stack that left the config, but keeps one still valid.
func TestCompleteRefreshPrunesFolds(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	stale := "https://a.example/browse/GONE"
	saveFolds(stateDir(), map[string]bool{stale: true, "stack:gamma": true, "stack:alpha": true}, 0)
	m := treeTestModel(t)
	m.refreshing, m.pending = true, 3
	res, _ := m.Update(sourceMsg{source: "alpha/jira", issues: m.cache.Issues[:2]})
	res, _ = res.(model).Update(sourceMsg{source: "alpha/pulls", pulls: nil})
	res, _ = res.(model).Update(sourceMsg{source: "beta/jira", issues: []issue{}})
	got := res.(model)
	if got.collapsed[stale] || got.collapsed["stack:gamma"] {
		t.Errorf("a complete refresh must prune stale folds: %v", got.collapsed)
	}
	if !got.collapsed["stack:alpha"] {
		t.Errorf("a still-configured stack keeps its fold: %v", got.collapsed)
	}
	collapsed, _ := loadFolds(stateDir())
	if collapsed[stale] || collapsed["stack:gamma"] {
		t.Errorf("the pruned folds should be gone from disk too: %v", collapsed)
	}
}

// TestEmptyStartKeepsSavedFoldsAfterRefresh: a saved fold for a ticket not
// yet fetched loads at once, and a complete refresh that brings the ticket
// in keeps it (its URL is valid again).
func TestEmptyStartKeepsSavedFoldsAfterRefresh(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	url := "https://a.example/browse/PLAT-1"
	saveFolds(stateDir(), map[string]bool{url: true, "stack:alpha": true}, 0)

	m := newModel([]stack{jiraStack("alpha")}, issueCache{}, true)
	if !m.collapsed[url] || !m.collapsed["stack:alpha"] {
		t.Fatalf("the saved folds should load before anything is fetched: %v", m.collapsed)
	}
	fresh := []issue{{Key: "PLAT-1", Stack: "alpha", URL: url, Summary: "s", Created: time.Now()}}
	res, _ := m.Update(sourceMsg{source: "alpha/jira", issues: fresh})
	got := res.(model)
	if !got.collapsed[url] || !got.collapsed["stack:alpha"] {
		t.Errorf("a complete refresh must keep a fold still valid: %v", got.collapsed)
	}
	collapsed, _ := loadFolds(stateDir())
	if !collapsed[url] {
		t.Errorf("the kept fold should be saved too: %v", collapsed)
	}
}

// TestFoldLevelClampsToDeepest: a saved level deeper than the tree now has
// (the config shrank since it was saved) is clamped to the tree's own
// deepest level before tab/shift+tab act on it.
func TestFoldLevelClampsToDeepest(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := treeTestModel(t) // its deepest level is 1: the story holds a sub-task and PRs

	m.depthNow = 99
	if cmd := m.foldTo(false); cmd == nil { // tab: one level deeper than the clamped 99 is "all"
		t.Fatal("foldTo should return a command")
	}
	if m.depthNow != 0 {
		t.Errorf("depthNow = %d, want 0 (clamped to 1, then past the deepest level)", m.depthNow)
	}

	m.depthNow = 99
	m.foldTo(true) // shift+tab: one level shallower than the clamped 99 lands on it
	if m.depthNow != 1 {
		t.Errorf("depthNow = %d, want 1 (the tree's own deepest level)", m.depthNow)
	}
}
