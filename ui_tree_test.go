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

// TestViewRendersTree: the story with its details under it (its status and
// its age: nothing its PRs need), its PRs as bullets under it (key and
// title, then the state and what they need), the sub-task on a branch from
// the story's arrow, the guide from the arrow down to it, keys in lower
// case, the counter of my tickets alone; and, with one-line rows,
// everything on the row.
func TestViewRendersTree(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := treeTestModel(t)
	lines := listText(m)
	want := []string{
		"▼ alpha",
		"▌▼ plat-100 fix login flow",
		"▌│     in progress · ",
		" │   ○ front#1 PLAT-100 login",
		" │     in review · conflicts · ",
		" │   ○ infra#2 PLAT-100 config",
		" │     merged · ",
		" └──── plat-101 add the test",
		"           open · ",
		"▼ beta",
		"   beta-7 update readme",
		"       to do · ",
	}
	for i, w := range want {
		if i >= len(lines) || !strings.HasPrefix(lines[i], w) {
			t.Errorf("line %d = %q, want it to start with %q", i, lines[i], w)
		}
	}
	wide := ansi.Strip(m.detailLine(m.rows[1], false, 200))
	if strings.Contains(wide, "conflicts") || m.lineOf[1] != 1 || m.lineOf[2] != 3 || m.lines != 12 {
		t.Errorf("the story says nothing of its PRs' needs and the rows know their lines: %q, %v, %d", wide, m.lineOf, m.lines)
	}
	m.rowsM = rowsOne
	m.renderList()
	lines = listText(m)
	want = []string{
		"▼ alpha",
		"▌▼ plat-100 in progress · ",
		" │   ○ front#1 in review · conflicts · ",
		" │   ○ infra#2 merged · ",
		" └──── plat-101 open · ",
		"▼ beta",
		"   beta-7 to do · ",
	}
	for i, w := range want {
		if i >= len(lines) || !strings.HasPrefix(lines[i], w) {
			t.Errorf("one line: line %d = %q, want it to start with %q", i, lines[i], w)
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

// sgrBold reports whether a styled string turns bold on anywhere.
func sgrBold(s string) bool {
	for _, seq := range strings.Split(s, "\x1b[")[1:] {
		end := strings.IndexByte(seq, 'm')
		if end < 0 {
			continue
		}
		params := strings.Split(seq[:end], ";")
		for i := 0; i < len(params); i++ {
			if params[i] == "38" || params[i] == "48" { // a color: skip its arguments
				if i+1 < len(params) && params[i+1] == "5" {
					i += 2
				} else if i+1 < len(params) && params[i+1] == "2" {
					i += 4
				}
				continue
			}
			if params[i] == "1" {
				return true
			}
		}
	}
	return false
}

// TestRowColors: a ticket's key in its level's color, a PR's in teal, the
// titles faint, the status and what a PR needs in their colors (faint), the
// rest of the details dim, and no bold anywhere, the selected row included
// (its dim words a lighter grey over the background).
func TestRowColors(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := treeTestModel(t)
	story, pr, sub := m.rows[1], m.rows[2], m.rows[4]
	checks := []struct {
		name, line, want string
	}{
		{"story key", m.rowLine(story, false, 200), keyStyle(0).Render("plat-100")},
		{"sub-task key", m.rowLine(sub, false, 200), keyStyle(1).Render("plat-101")},
		{"pr key", m.rowLine(pr, false, 200), stPRKey.Render("front#1")},
		{"title", m.rowLine(story, false, 200), lipgloss.NewStyle().Faint(true).Render("fix login flow")},
		{"status", m.detailLine(story, false, 200), stDoing.Faint(true).Render("in progress")},
		{"state", m.detailLine(pr, false, 200), stWarn.Faint(true).Render("in review")},
		{"needs", m.detailLine(pr, false, 200), stBad.Faint(true).Render("conflicts")},
		{"separator", m.detailLine(pr, false, 200), stDetail.Render(" · ")},
		{"selected dim", m.detailLine(story, true, 200), stDetailSel.Render(" · ")},
	}
	for _, c := range checks {
		if !strings.Contains(c.line, c.want) {
			t.Errorf("%s: %q lacks %q", c.name, c.line, c.want)
		}
	}
	for i := range m.rows {
		for _, sel := range []bool{false, true} {
			for _, l := range m.rowLines(m.rows[i], sel, 200) {
				if sgrBold(l) {
					t.Errorf("row %d (selected %v) is bold: %q", i, sel, l)
				}
			}
		}
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
	for _, want := range []string{"PLAT-100 login", "front#1 · feat/PLAT-100 → main · by me", "in review · conflicts", "review no rule"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the PR preview lacks %q:\n%s", want, plain)
		}
	}
	if hh := lipgloss.Height(m.headerOf(r, m.prevW())); m.prevVP.Height() != m.bodyH()-hh-1 {
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
	press(tea.KeyPressMsg{Code: tea.KeyF1}, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyRight})
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

// TestShowOptionHidesDoneWork: the panel's Show option set to pending takes
// out the merged PR row, and a ticket whose every PR is merged with it; it
// is remembered; ctrl+t cycles it.
func TestShowOptionHidesDoneWork(t *testing.T) {
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
	press(tea.KeyPressMsg{Code: tea.KeyF1}, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.show != showPending || loadSetting(stateDir(), "show") != "pending" {
		t.Errorf("show = %v, saved %q", m.show, loadSetting(stateDir(), "show"))
	}
	lines := strings.Join(listText(m), "\n")
	for _, gone := range []string{"infra#2", "beta-7", "beta"} {
		if strings.Contains(lines, gone) {
			t.Errorf("pending kept %q:\n%s", gone, lines)
		}
	}
	if !strings.Contains(lines, "front#1") || !strings.Contains(lines, "plat-101") {
		t.Errorf("pending lost open work:\n%s", lines)
	}
	if !strings.Contains(ansi.Strip(m.render()), "─ 2/3 ─┴") {
		t.Errorf("the counter says two of three tickets show:\n%s", ansi.Strip(m.render()))
	}
	if again := treeTestModel(t); again.show != showPending {
		t.Errorf("the next run opens with pending")
	}
	press(tea.KeyPressMsg{Code: tea.KeyEscape}, tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	if m.show != showAll || m.flash.text != "show: all" {
		t.Errorf("ctrl+t goes to the next show: %s, %q", m.show, m.flash.text)
	}
	if lines = strings.Join(listText(m), "\n"); !strings.Contains(lines, "plat-101") || !strings.Contains(lines, "beta-7") {
		t.Errorf("all lists everything:\n%s", lines)
	}
	press(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	if lines = strings.Join(listText(m), "\n"); m.show != showWorking || strings.Contains(lines, "plat-101") {
		t.Errorf("working leaves out the sub-task not started:\n%s", lines)
	}
}

// TestGroupByPhase: ctrl+g lists by phase: a section per PR state with the
// PR rows under it, each with the path of its tickets, and the tickets
// without a PR; ctrl+g again goes back to the tree. Space on a section
// folds it.
func TestGroupByPhase(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := treeTestModel(t)
	res, _ := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = res.(model)
	lines := listText(m)
	want := []string{
		"▼ alpha",
		" ▼ ── PR in review (1) ",
		"  plat-100 › front#1 PLAT-100 login",
		"      in review · conflicts · in progress · ",
		" ▼ ── no PR (1) ",
		"  plat-100 › plat-101 add the test",
		"      open · ",
		" ▼ ── PRs merged (1) ",
		"  plat-100 › infra#2 PLAT-100 config",
		"      merged · in progress · ",
		"▼ beta",
		" ▼ ── no PR (1) ",
		"  beta-7 update readme",
	}
	for i, w := range want {
		got := ""
		if i < len(lines) {
			got = strings.Replace(lines[i], "▌", " ", 1)
		}
		if !strings.HasPrefix(got, w) && !strings.HasPrefix(got, " "+w[1:]) {
			t.Errorf("line %d = %q, want it to start with %q", i, got, w)
		}
	}
	if m.group != groupPhase || loadSetting(stateDir(), "group") != "phase" {
		t.Errorf("group = %s", m.group)
	}
	for i, r := range m.rows {
		if r.kind == rowSection && r.text == phaseMerged {
			m.cursor = i
		}
	}
	res, _ = m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	m = res.(model)
	if strings.Contains(strings.Join(listText(m), "\n"), "infra#2") {
		t.Errorf("a folded section lists nothing")
	}
	res, _ = m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if res.(model).group != groupTree {
		t.Errorf("ctrl+g goes back to the tree")
	}
}

// TestLevelKeys: shift+tab folds the tree one level shallower (all, then
// the deepest level, down to the roots alone), tab one deeper and back to
// all; with a query they do nothing.
func TestLevelKeys(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := treeTestModel(t)
	shift := tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	tab := tea.KeyPressMsg{Code: tea.KeyTab}
	res, _ := m.Update(shift)
	m = res.(model)
	if m.depthNow != 1 || m.flash.text != "level 1" { // one level: the story holds everything
		t.Errorf("shift+tab from all: level %d, flash %q", m.depthNow, m.flash.text)
	}
	res, _ = m.Update(shift)
	m = res.(model)
	if m.depthNow != 1 {
		t.Errorf("shift+tab stops at the roots: level %d", m.depthNow)
	}
	lines := strings.Join(listText(m), "\n")
	if strings.Contains(lines, "front#1") || strings.Contains(lines, "plat-101") || !strings.Contains(lines, "▶") {
		t.Errorf("level 1 shows the roots folded:\n%s", lines)
	}
	res, _ = m.Update(tab)
	m = res.(model)
	if m.depthNow != 0 || m.flash.text != "all levels" || !strings.Contains(strings.Join(listText(m), "\n"), "plat-101") {
		t.Errorf("tab past the deepest level shows everything: level %d, flash %q", m.depthNow, m.flash.text)
	}
	res, _ = m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	res, _ = res.(model).Update(shift)
	if res.(model).depthNow != 0 {
		t.Errorf("with a query shift+tab does nothing")
	}
}

// TestHeaderRows: the cursor sits on a stack's header, space folds the
// stack to it, enter says there is nothing to open, and the preview counts
// the stack.
func TestHeaderRows(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := treeTestModel(t)
	res, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = res.(model)
	if r := m.currentRow(); r == nil || r.kind != rowHeader || r.stack != "alpha" {
		t.Fatalf("up from the first ticket lands on its stack: %+v", r)
	}
	plain := ansi.Strip(m.render())
	if !strings.Contains(plain, "1 in progress") || !strings.Contains(plain, "1 PRs in review") {
		t.Errorf("the header's preview counts the stack:\n%s", plain)
	}
	res, _ = m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	m = res.(model)
	lines := listText(m)
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "▶") || !strings.HasPrefix(lines[1], "▼ beta") {
		t.Errorf("space folds the stack to its header:\n%s", strings.Join(lines, "\n"))
	}
	res, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || res.(model).flash.text != "nothing to open" || res.(model).openURL != "" {
		t.Errorf("enter on a header: flash %q", res.(model).flash.text)
	}
}

// TestLocalCounters: my PR with a checkout says what it holds that GitHub
// has not seen, as the prompt counts it, after what it needs.
func TestLocalCounters(t *testing.T) {
	m := treeTestModel(t)
	res, _ := m.Update(localMsg{states: map[string]localState{"https://github.com/acme/front/pull/1": {ahead: 2, unstaged: 3, untracked: 1}}})
	m = res.(model)
	if got := ansi.Strip(m.detailLine(m.rows[2], false, 200)); !strings.HasPrefix(got, " │     in review · conflicts · ↑2 !3 ?1 · ") {
		t.Errorf("details = %q", got)
	}
}

// TestShortTitles: the list shows a ticket's short title once there is one,
// the preview keeps the tracker's with the short one under it, the search
// finds either, and the Titles option set to original shows the tracker's.
func TestShortTitles(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := treeTestModel(t)
	m.summBusy = true
	res, _ := m.Update(summariesMsg{written: summaries{"https://a.example/browse/PLAT-100": {Text: "Arreglar el flujo de login"}}})
	m = res.(model)
	if lines := listText(m); !strings.HasPrefix(lines[1], "▌▼ plat-100 Arreglar el flujo de login") {
		t.Errorf("the short title: %q", lines[1])
	}
	if loadSummaries()["https://a.example/browse/PLAT-100"].Text == "" {
		t.Errorf("the short titles are saved")
	}
	if plain := ansi.Strip(m.render()); !strings.Contains(plain, "fix login flow") || !strings.Contains(plain, "≈ Arreglar el flujo de login") {
		t.Errorf("the preview keeps the tracker's title:\n%s", plain)
	}
	for _, q := range []string{"arreglar", "login flow"} {
		m.ti.SetValue(q)
		m.applyFilter()
		if r := m.currentRow(); r == nil || r.kind != rowIssue || r.e.it.Key != "PLAT-100" {
			t.Errorf("%q finds the story: %+v", q, r)
		}
	}
	m.ti.SetValue("")
	m.setOption("titles", 1)
	if lines := strings.Join(listText(m), "\n"); !strings.Contains(lines, "plat-100 fix login flow") {
		t.Errorf("original titles:\n%s", lines)
	}
}

// TestSpaceFolds: space on a ticket folds its PRs and children away and
// turns its arrow, again unfolds it; on a PR it folds the PR's ticket and
// the cursor moves there; on a ticket with nothing under it it says so;
// with a query in the filter it is text.
func TestSpaceFolds(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := treeTestModel(t)
	space := tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	res, _ := m.Update(space)
	m = res.(model)
	lines := strings.Join(listText(m), "\n")
	if strings.Contains(lines, "front#1") || strings.Contains(lines, "plat-101") || !strings.Contains(lines, "▌▶\uFE0E plat-100 fix login flow") || m.lines != 6 {
		t.Errorf("space folds the story, its arrow turned:\n%s", lines)
	}
	if !strings.Contains(lines, "▌      in progress") {
		t.Errorf("the details stay, without the guide:\n%s", lines)
	}
	res, _ = m.Update(space)
	m = res.(model)
	if lines = strings.Join(listText(m), "\n"); !strings.Contains(lines, "front#1") || strings.Contains(lines, "▶") {
		t.Errorf("space again unfolds:\n%s", lines)
	}
	res, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // front#1
	res, _ = res.(model).Update(space)
	m = res.(model)
	if r := m.currentRow(); r == nil || r.kind != rowIssue || r.e.it.Key != "PLAT-100" || !r.collapsed {
		t.Errorf("space on a PR folds its ticket and sits on it: %+v", r)
	}
	res, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})           // the beta header
	res, _ = res.(model).Update(tea.KeyPressMsg{Code: tea.KeyDown}) // BETA-7, nothing under it
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
	if m.ti.Value() == "a " && !strings.Contains(strings.Join(listText(m), "\n"), "plat-101") {
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
	if !strings.Contains(lines, "front#9") || strings.Contains(lines, "front#1") || !strings.Contains(lines, "beta-7") {
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

// TestShortTitlesFailure: a summarizer that fails says so on the help line
// once; one that is not installed says nothing.
func TestShortTitlesFailure(t *testing.T) {
	m := treeTestModel(t)
	m.summBusy = true
	res, _ := m.Update(summariesMsg{err: errNoSummarizer})
	if got := res.(model); got.netErr != "" || got.summBusy {
		t.Errorf("a missing summarizer is quiet: %q", got.netErr)
	}
	res, _ = m.Update(summariesMsg{err: errTest("credit too low")})
	if got := res.(model); got.netErr != "short titles: credit too low" {
		t.Errorf("a failure takes the help line: %q", got.netErr)
	}
}
