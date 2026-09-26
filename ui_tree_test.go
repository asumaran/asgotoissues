package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// treeTestModel is a list with a story and its sub-task, two PRs under the
// story in two repos (one conflicting, one merged) and a ticket in another
// stack.
func treeTestModel(t *testing.T) model {
	t.Helper()
	issues := []issue{
		{Key: "PLAT-100", Stack: "alpha", URL: "https://a.example/browse/PLAT-100", Summary: "fix login flow", Project: "PLAT",
			Status: "In Progress", State: stateDoing, Type: "Story", Created: time.Unix(300, 0), Updated: time.Unix(300, 0), Description: "Some *body* text"},
		{Key: "PLAT-101", Stack: "alpha", URL: "https://a.example/browse/PLAT-101", Summary: "add the test", Project: "PLAT",
			Status: "Open", Type: "Sub-task", ParentKey: "PLAT-100", ParentSummary: "fix login flow", ParentURL: "https://a.example/browse/PLAT-100", Created: time.Unix(200, 0), Updated: time.Unix(400, 0)},
		{Key: "BETA-7", Stack: "beta", URL: "https://b.example/browse/BETA-7", Summary: "update readme",
			Status: "To Do", StatusCat: "To Do", Type: "Story", Updated: time.Unix(100, 0)},
	}
	pulls := []pull{
		{URL: "https://github.com/acme/front/pull/1", Number: 1, Repo: "acme/front", Key: "front#1", Stack: "alpha", Title: "PLAT-100 login", Body: "the *body*",
			State: prOpen, Mine: true, Head: "feat/PLAT-100", Base: "main", Author: "me", Mergeable: "CONFLICTING", Refs: []string{"PLAT-100"}, Updated: time.Unix(10, 0)},
		{URL: "https://github.com/acme/infra/pull/2", Number: 2, Repo: "acme/infra", Key: "infra#2", Stack: "alpha", Title: "PLAT-100 config",
			State: prMerged, Mine: true, Head: "feat/PLAT-100-config", Base: "main", Refs: []string{"PLAT-100"}, Updated: time.Unix(20, 0)},
	}
	stacks := []stack{{Name: "alpha", Trackers: []string{kindJira}, Orgs: []string{"acme"}, BaseURL: "https://a.example"}, jiraStack("beta")}
	m := newModel(stacks, issueCache{FetchedAt: time.Now(), Issues: issues, Pulls: pulls}, false)
	res, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 30})
	return res.(model)
}

func listText(m model) []string {
	var out []string
	for _, l := range m.listLines() {
		out = append(out, strings.TrimRight(ansi.Strip(l), " "))
	}
	return out
}

// TestViewRendersTree: the story with its details under it (what its PRs
// need, in words), its PRs under it (key and title, then the state and the
// flags), the sub-task set in where the story's title starts, the counter
// of my tickets alone; and, with one-line rows, everything on the row.
func TestViewRendersTree(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := treeTestModel(t)
	lines := listText(m)
	want := []string{
		"alpha",
		"▌ PLAT-100 fix login flow",
		"▌          In Progress · Story · updated ",
		"           ↳ front#1 PLAT-100 login",
		"                     open · conflicts · ",
		"           ↳ infra#2 PLAT-100 config",
		"                     merged · ",
		"           PLAT-101 add the test",
		"                    Open · Sub-task · updated ",
		"beta",
		"  BETA-7 update readme",
		"         To Do · Story · updated ",
	}
	for i, w := range want {
		if i >= len(lines) || !strings.HasPrefix(lines[i], w) {
			t.Errorf("line %d = %q, want it to start with %q", i, lines[i], w)
		}
	}
	wide := ansi.Strip(m.detailLine(m.rows[1], false, 200))
	if !strings.HasSuffix(wide, " · 2 PRs · conflicts") || m.lineOf[1] != 1 || m.lineOf[2] != 3 || m.lines != 12 {
		t.Errorf("the story counts its PRs and the rows know their lines: %q, %v, %d", wide, m.lineOf, m.lines)
	}
	m.rowsM = rowsOne
	m.renderList()
	lines = listText(m)
	want = []string{
		"alpha",
		"▌ PLAT-100 [In Progress] fix login flow",
		"           ↳ front#1  conflicts  PLAT-100 login",
		"           ↳ infra#2  merged  PLAT-100 config",
		"           PLAT-101 [Open] add the test",
		"beta",
		"  BETA-7 [To Do] update readme",
	}
	for i, w := range want {
		if i >= len(lines) || lines[i] != w {
			t.Errorf("one line: line %d = %q, want %q", i, lines[i], w)
		}
	}
	if m.lines != 7 {
		t.Errorf("one line per row: %d lines", m.lines)
	}
	plain := ansi.Strip(m.render())
	if !strings.Contains(plain, "─ 3/3 ─┴") {
		t.Errorf("the counter counts my tickets, not the PRs:\n%s", plain)
	}
	if !strings.Contains(plain, "Some body text") && !strings.Contains(plain, "rendering") {
		t.Errorf("the preview is the story's:\n%s", plain)
	}
}

// TestTwoTones: the list is two tones: a row's first line is plain (no
// color on the key, the status or the flags), its details are dim, and
// under the selection the details take a lighter grey over the background
// instead of the dim one, which would vanish there.
func TestTwoTones(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := treeTestModel(t)
	story, pr := m.rows[1], m.rows[2]
	for name, line := range map[string]string{
		"story":       m.rowLine(story, false, 200),
		"story (sel)": m.rowLine(story, true, 200),
		"pr":          m.rowLine(pr, false, 200),
		"pr (sel)":    m.rowLine(pr, true, 200),
	} {
		for _, color := range []lipgloss.Style{stKey, stDoing, stBad, stWarn, stOK} {
			if seq := color.Render("x"); strings.Contains(line, seq[:strings.Index(seq, "x")]) {
				t.Errorf("%s: the first line carries a color: %q", name, line)
			}
		}
	}
	detail := m.detailLine(story, false, 200)
	if !strings.Contains(detail, stDetail.Render("In Progress · Story")[:5]) || strings.Contains(detail, stDoing.Render("x")[:5]) {
		t.Errorf("the details are dim, one tone: %q", detail)
	}
	sel := m.detailLine(story, true, 200)
	dimOnSel := stSel.Foreground(stDim.GetForeground()).Render("x")
	if strings.Contains(sel, dimOnSel[:strings.Index(dimOnSel, "x")]) || !strings.Contains(sel, stDetailSel.Render("x")[:strings.Index(stDetailSel.Render("x"), "x")]) {
		t.Errorf("the selected details are a lighter grey over the background: %q", sel)
	}
	m.rowsM = rowsOne
	one := m.rowLine(pr, false, 200)
	if !strings.Contains(one, stDetail.Render("conflicts")) || strings.Contains(one, stBad.Render("conflicts")) {
		t.Errorf("one-line flags are dim, not colored: %q", one)
	}
}

// TestPullRowOpensAndCopies: the cursor reaches a PR row, enter opens the
// PR, ctrl+y copies its URL (a ticket's row still copies the key), and the
// preview shows the PR's facts with the body under them.
func TestPullRowOpensAndCopies(t *testing.T) {
	log := filepath.Join(t.TempDir(), "clip")
	stub := filepath.Join(t.TempDir(), "clipboard")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\ncat > "+log+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASGOTOISSUES_CLIPBOARD", stub)
	m := treeTestModel(t)
	res, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = res.(model)
	r := m.currentRow()
	if r == nil || r.kind != rowPull || r.p.Key != "front#1" {
		t.Fatalf("down from the story lands on its first PR, got %+v", r)
	}
	plain := ansi.Strip(m.render())
	for _, want := range []string{"PLAT-100 login", "front#1 · feat/PLAT-100 → main · by me", "[open] conflicts", "review no rule"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the PR preview lacks %q:\n%s", want, plain)
		}
	}
	if hh := lipgloss.Height(headerOf(r, m.prevW())); m.prevVP.Height() != m.bodyH()-hh-1 {
		t.Errorf("the body viewport is %d lines under a header of %d, want %d", m.prevVP.Height(), hh, m.bodyH()-hh-1)
	}
	res, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	res.(model).Update(cmd())
	if got, _ := os.ReadFile(log); string(got) != "https://github.com/acme/front/pull/1" {
		t.Errorf("ctrl+y on a PR copied %q, want its URL", got)
	}
	res, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || res.(model).openURL != "https://github.com/acme/front/pull/1" {
		t.Errorf("enter on a PR opens it: %q", res.(model).openURL)
	}
}

// TestOrderKeyCyclesAndPersists: ctrl+s takes the order to the next mode,
// says so, names the one after on its key, and the next run opens with it.
func TestOrderKeyCyclesAndPersists(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := treeTestModel(t)
	if m.order != orderCreated {
		t.Fatalf("a fresh state dir starts on created, got %s", m.order)
	}
	res, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = res.(model)
	if m.order != orderUpdated || loadSetting(stateDir(), "order") != "updated" || cmd == nil {
		t.Errorf("ctrl+s: order %s, saved %q", m.order, loadSetting(stateDir(), "order"))
	}
	if m.flash.text != "order: updated" {
		t.Errorf("flash = %q", m.flash.text)
	}
	if keys := ansi.Strip(strings.Join(keyLines(m.help, m.keys, 200), "\n")); !strings.Contains(keys, "order by key") {
		t.Errorf("the key names the next order: %q", keys)
	}
	if m.ti.Value() != "" {
		t.Errorf("ctrl+s leaked into the filter: %q", m.ti.Value())
	}
	if again := treeTestModel(t); again.order != orderUpdated {
		t.Errorf("the next run opens with the saved order, got %s", again.order)
	}
	for range 3 {
		res, _ = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
		m = res.(model)
	}
	if m.order != orderCreated {
		t.Errorf("four presses go around: %s", m.order)
	}
}

// TestPrsOptionHidesMerged: the panel's PRs option set to open takes the
// merged PR row out and keeps the cursor on its row.
func TestPrsOptionHidesMerged(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := treeTestModel(t)
	press := func(keys ...tea.KeyPressMsg) {
		for _, k := range keys {
			res, _ := m.Update(k)
			m = res.(model)
		}
	}
	press(tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyDown}) // PLAT-101
	press(tea.KeyPressMsg{Code: tea.KeyF1}, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.prs != prsOpen || loadSetting(stateDir(), "prs") != "open" {
		t.Errorf("prs = %s, saved %q", m.prs, loadSetting(stateDir(), "prs"))
	}
	lines := strings.Join(listText(m), "\n")
	if strings.Contains(lines, "infra#2") || !strings.Contains(lines, "front#1") {
		t.Errorf("open hides the merged PR only:\n%s", lines)
	}
	if r := m.currentRow(); r == nil || r.kind != rowIssue || r.e.it.Key != "PLAT-101" {
		t.Errorf("the cursor stays on its row: %+v", r)
	}
	press(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.prs != prsAttention || strings.Contains(strings.Join(listText(m), "\n"), "infra#2") {
		t.Errorf("attention: prs = %s", m.prs)
	}
}

// TestRowsOptionPersists: the panel's Rows option set to one line takes the
// details out, is remembered, and a click on a row's second line selects it
// while there is one.
func TestRowsOptionPersists(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := treeTestModel(t)
	click := func(y int) {
		res, _ := m.Update(tea.MouseClickMsg{X: 3, Y: listY + y, Button: tea.MouseLeft})
		m = res.(model)
	}
	click(6) // the second line of infra#2
	if r := m.currentRow(); r == nil || r.kind != rowPull || r.p.Key != "infra#2" {
		t.Fatalf("a click on the details selects the row: %+v", r)
	}
	press := func(keys ...tea.KeyPressMsg) {
		for _, k := range keys {
			res, _ := m.Update(k)
			m = res.(model)
		}
	}
	press(tea.KeyPressMsg{Code: tea.KeyF1}, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.rowsM != rowsOne || loadSetting(stateDir(), "rows") != "one" || m.lines != 7 {
		t.Errorf("rows = %s, saved %q, %d lines", m.rowsM, loadSetting(stateDir(), "rows"), m.lines)
	}
	if r := m.currentRow(); r == nil || r.kind != rowPull || r.p.Key != "infra#2" {
		t.Errorf("the cursor stays on its row: %+v", r)
	}
	if again := treeTestModel(t); again.rowsM != rowsOne {
		t.Errorf("the next run opens with one-line rows, got %s", again.rowsM)
	}
}

// TestMergedOptionHidesDoneWork: the panel's Merged option set to hide takes
// out the merged PR row, and a ticket whose every PR is merged with it; it is
// remembered.
func TestMergedOptionHidesDoneWork(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := treeTestModel(t)
	// BETA-7 gets a merged PR and nothing else: done.
	m.cache.Pulls = append(m.cache.Pulls, pull{URL: "https://github.com/b/x/pull/3", Number: 3, Repo: "b/x", Key: "x#3", Stack: "beta", Title: "done", State: prMerged, Mine: true, Refs: []string{"BETA-7"}})
	m.setEntries(m.cache.Issues, m.cache.Pulls)
	m.applyFilter()
	m.renderList()
	press := func(keys ...tea.KeyPressMsg) {
		for _, k := range keys {
			res, _ := m.Update(k)
			m = res.(model)
		}
	}
	press(tea.KeyPressMsg{Code: tea.KeyF1}, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyRight})
	if !m.hideMerged || loadSetting(stateDir(), "merged") != "hide" {
		t.Errorf("merged = %v, saved %q", m.hideMerged, loadSetting(stateDir(), "merged"))
	}
	lines := strings.Join(listText(m), "\n")
	for _, gone := range []string{"infra#2", "BETA-7", "beta"} {
		if strings.Contains(lines, gone) {
			t.Errorf("hide merged kept %q:\n%s", gone, lines)
		}
	}
	if !strings.Contains(lines, "front#1") || !strings.Contains(lines, "PLAT-101") {
		t.Errorf("hide merged lost open work:\n%s", lines)
	}
	if !strings.Contains(ansi.Strip(m.render()), "─ 2/3 ─┴") {
		t.Errorf("the counter says two of three tickets show:\n%s", ansi.Strip(m.render()))
	}
	if again := treeTestModel(t); !again.hideMerged {
		t.Errorf("the next run opens with merged hidden")
	}
}

// TestSpaceFolds: space on a ticket folds its PRs and children away and
// marks it, again unfolds it; on a PR it folds the PR's ticket and the
// cursor moves there; on a ticket with nothing under it it says so; with a
// query in the filter it is text.
func TestSpaceFolds(t *testing.T) {
	m := treeTestModel(t)
	space := tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	res, _ := m.Update(space)
	m = res.(model)
	lines := strings.Join(listText(m), "\n")
	if strings.Contains(lines, "front#1") || strings.Contains(lines, "PLAT-101") || !strings.Contains(lines, "▌▸PLAT-100 fix login flow") || m.lines != 6 {
		t.Errorf("space folds the story, the mark in the gutter:\n%s", lines)
	}
	if !strings.Contains(lines, "▌          In Progress") {
		t.Errorf("the details stay under the title:\n%s", lines)
	}
	res, _ = m.Update(space)
	m = res.(model)
	if lines = strings.Join(listText(m), "\n"); !strings.Contains(lines, "front#1") || strings.Contains(lines, "▸") {
		t.Errorf("space again unfolds:\n%s", lines)
	}
	res, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // front#1
	res, _ = res.(model).Update(space)
	m = res.(model)
	if r := m.currentRow(); r == nil || r.kind != rowIssue || r.e.it.Key != "PLAT-100" || !r.collapsed {
		t.Errorf("space on a PR folds its ticket and sits on it: %+v", r)
	}
	res, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // BETA-7, nothing under it
	res, cmd := res.(model).Update(space)
	m = res.(model)
	if cmd == nil || m.flash.text != "nothing to fold" {
		t.Errorf("space on a leaf: flash %q", m.flash.text)
	}
	res, _ = m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	res, _ = res.(model).Update(space)
	m = res.(model)
	if m.ti.Value() != "a " {
		t.Errorf("with a query space is text: %q", m.ti.Value())
	}
	if m.ti.Value() == "a " && !strings.Contains(strings.Join(listText(m), "\n"), "PLAT-101") {
		t.Errorf("a query shows the folded children")
	}
}

// TestPullsSourceRefreshes: the pulls source answers with PRs, which the
// merged snapshot keeps and the list shows; the snapshot on disk has them.
func TestPullsSourceRefreshes(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := treeTestModel(t)
	m.refreshing, m.pending = true, 3
	fresh := []pull{{URL: "https://github.com/acme/front/pull/9", Number: 9, Repo: "acme/front", Key: "front#9", Stack: "alpha", Title: "new one", State: prOpen, Mine: true, Refs: []string{"PLAT-101"}}}
	res, _ := m.Update(sourceMsg{source: "alpha/jira", issues: m.cache.Issues[:2]})
	res, _ = res.(model).Update(sourceMsg{source: "alpha/pulls", pulls: fresh})
	res, _ = res.(model).Update(sourceMsg{source: "beta/jira", err: errTest("boom")})
	m = res.(model)
	lines := strings.Join(listText(m), "\n")
	if !strings.Contains(lines, "front#9") || strings.Contains(lines, "front#1") || !strings.Contains(lines, "BETA-7") {
		t.Errorf("the fresh PRs replace alpha's, beta keeps its cache:\n%s", lines)
	}
	if got := loadCache(); len(got.Pulls) != 1 || got.Pulls[0].Key != "front#9" {
		t.Errorf("the snapshot has the PRs: %+v", got.Pulls)
	}
}

// TestOldSnapshotNests: a snapshot written before parent_url existed still
// nests its Jira tickets, from the parent key and the stack's site.
func TestOldSnapshotNests(t *testing.T) {
	issues := []issue{
		{Key: "PLAT-1", Stack: "alpha", URL: "https://a.example/browse/PLAT-1", Summary: "epic"},
		{Key: "PLAT-2", Stack: "alpha", URL: "https://a.example/browse/PLAT-2", Summary: "story", ParentKey: "PLAT-1"},
	}
	stacks := []stack{{Name: "alpha", Trackers: []string{kindJira}, BaseURL: "https://a.example"}}
	m := newModel(stacks, issueCache{FetchedAt: time.Now(), Issues: issues}, false)
	if got := rowKeys(m.rows); got != "H:alpha PLAT-1 >PLAT-2" {
		t.Errorf("rows = %s", got)
	}
}
