package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func testModel(t *testing.T) model {
	t.Helper()
	issues := []issue{
		{Key: "PLAT-100", Stack: "alpha", URL: "https://a.example/browse/PLAT-100", Summary: "fix login flow",
			Status: "In Progress", StatusCat: "In Progress", Type: "Task", Updated: time.Unix(300, 0), Description: "Some *body* text"},
		{Key: "BETA-7", Stack: "beta", URL: "https://b.example/browse/BETA-7", Summary: "update readme",
			Status: "To Do", StatusCat: "To Do", Type: "Story", Updated: time.Unix(100, 0)},
	}
	m := newModel([]stack{jiraStack("alpha"), jiraStack("beta")}, issueCache{FetchedAt: time.Now(), Issues: issues}, false)
	res, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = res.(model)
	return m
}

func TestViewRendersRows(t *testing.T) {
	m := testModel(t)
	view := m.render()
	for _, want := range []string{"alpha", "PLAT-100", "fix login flow", "beta", "BETA-7", "update readme", "[In Progress]"} {
		if !strings.Contains(view, want) {
			t.Errorf("render() missing %q\n----\n%s", want, view)
		}
	}
}

func TestViewAfterWindowResize(t *testing.T) {
	m := testModel(t)
	res, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 34})
	view := res.(model).render()
	if !strings.Contains(view, "PLAT-100") {
		t.Errorf("render() after resize missing rows:\n%s", view)
	}
}

func TestFilterNarrowsRows(t *testing.T) {
	m := testModel(t)
	var mm tea.Model = m
	for _, r := range "readme" {
		mm, _ = mm.(model).handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	view := mm.(model).render()
	if strings.Contains(view, "fix login flow") {
		t.Errorf("filter kept non-matching ticket:\n%s", view)
	}
	if !strings.Contains(view, "update readme") {
		t.Errorf("filter lost matching ticket:\n%s", view)
	}
}

func TestEnterQueuesURLAndQuits(t *testing.T) {
	m := testModel(t)
	mm, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	mm, cmd = mm.(model).handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("enter should return tea.Quit")
	}
	if got := mm.(model).openURL; got != "https://b.example/browse/BETA-7" {
		t.Errorf("openURL = %q", got)
	}
}

// TestCtrlOOpensLikeEnter: ctrl+o is the family's "open in the browser" key,
// so it queues the URL and quits the same way enter does.
func TestCtrlOOpensLikeEnter(t *testing.T) {
	m := testModel(t)
	mm, cmd := m.handleKey(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatalf("ctrl+o should return tea.Quit")
	}
	if got := mm.(model).openURL; got != "https://a.example/browse/PLAT-100" {
		t.Errorf("openURL = %q", got)
	}
	if got := mm.(model).ti.Value(); got != "" {
		t.Errorf("ctrl+o leaked into the filter: %q", got)
	}
}

// TestCopyKeyCopiesTheIssueKey covers ctrl+y: the key of the issue under the
// cursor goes to the clipboard, the help line confirms it for a moment, and
// the filter is left alone.
func TestCopyKeyCopiesTheIssueKey(t *testing.T) {
	log := filepath.Join(t.TempDir(), "clip")
	stub := filepath.Join(t.TempDir(), "clipboard")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\ncat > "+log+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASGOTOISSUES_CLIPBOARD", stub)
	m := testModel(t)
	res, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+y returned no command")
	}
	res, _ = res.(model).Update(cmd())
	m = res.(model)
	if got, _ := os.ReadFile(log); string(got) != "PLAT-100" {
		t.Errorf("the clipboard got %q, want the key of the issue under the cursor", got)
	}
	plain := strings.Split(ansi.Strip(m.View().Content), "\n")
	if help := plain[len(plain)-2]; !strings.Contains(help, "copied PLAT-100") {
		t.Errorf("help line = %q, want the confirmation", help)
	}
	if m.ti.Value() != "" {
		t.Errorf("ctrl+y leaked into the filter: %q", m.ti.Value())
	}
	res, _ = m.Update(clearFlashMsg(m.flash.seq))
	plain = strings.Split(ansi.Strip(res.(model).View().Content), "\n")
	if help := plain[len(plain)-2]; !strings.Contains(help, "type filter") {
		t.Errorf("after the timer the help is back: %q", help)
	}
}

func TestEscQuitsWithoutURL(t *testing.T) {
	m := testModel(t)
	mm, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil || mm.(model).openURL != "" {
		t.Errorf("esc should quit without an action")
	}
}

func TestSourceMsgPartialFailureKeepsCache(t *testing.T) {
	m := testModel(t)
	m.refreshing = true
	m.pending = 2
	m.cache.FetchedAt = time.Unix(1000, 0)
	// alpha fails, beta returns a new ticket
	mm, _ := m.Update(sourceMsg{source: "alpha/jira", err: errTest("boom")})
	mm, _ = mm.(model).Update(sourceMsg{source: "beta/jira", issues: []issue{
		{Key: "BETA-8", Stack: "beta", URL: "u8", Summary: "brand new", Updated: time.Unix(500, 0)},
	}})
	res := mm.(model)
	if res.refreshing {
		t.Errorf("refresh should be finished")
	}
	view := res.render()
	if !strings.Contains(view, "PLAT-100") || !strings.Contains(view, "BETA-8") || strings.Contains(view, "BETA-7") {
		t.Errorf("expected cached alpha + fresh beta:\n%s", view)
	}
	if !strings.Contains(view, "boom") {
		t.Errorf("footer should surface the failed stack error")
	}
	if !res.cache.FetchedAt.Equal(time.Unix(1000, 0)) {
		t.Errorf("FetchedAt must not advance on partial failure")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

// TestMain sandboxes the cache: tests must never touch the real state dir.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "asgotoissues-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("HERDR_PLUGIN_STATE_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// TestFrameGeometry pins the single-frame layout: exactly height lines, each
// exactly width cells, sections where the click math expects them.
func TestFrameGeometry(t *testing.T) {
	m := testModel(t)
	lines := strings.Split(m.render(), "\n")
	if len(lines) != m.height {
		t.Errorf("%d lines, want %d", len(lines), m.height)
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != m.width {
			t.Errorf("line %d is %d cells, want %d: %q", i, w, m.width, ansi.Strip(l))
		}
	}
	plain := strings.Split(ansi.Strip(m.render()), "\n")
	if !strings.HasPrefix(plain[0], "╭") || !strings.HasPrefix(plain[len(plain)-1], "╰") ||
		!strings.Contains(plain[mainY(false)], "┬") || !strings.Contains(plain[len(plain)-3], "─ 2/2 ─┴") ||
		!strings.HasPrefix(plain[1], "│ asgotoissues ❯ ") {
		t.Errorf("frame sections misplaced:\n%s", strings.Join(plain, "\n"))
	}
	if help := plain[len(plain)-2]; !strings.Contains(help, "type filter") || !strings.Contains(help, "esc/q quit") {
		t.Errorf("help line = %q", help)
	}
}

func TestClickSelectsTicketRow(t *testing.T) {
	m := testModel(t)
	// rows: header(alpha) PLAT-100 header(beta) BETA-7
	click := func(x, y int) tea.MouseClickMsg { return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft} }
	res, _ := m.Update(click(3, listY(false)+3))
	got := res.(model)
	if r := got.currentRow(); r == nil || r.e.it.Key != "BETA-7" || got.openURL != "" {
		t.Fatalf("click must select BETA-7 without opening it: cursor=%d open=%q", got.cursor, got.openURL)
	}
	for _, c := range []tea.MouseClickMsg{click(3, listY(false)+2), click(got.listW()+10, listY(false)+1),
		click(got.listW()+1, listY(false)+1), click(0, listY(false)+1), click(3, mainY(false)), click(3, 1)} {
		res, _ = got.Update(c)
		if res.(model).cursor != got.cursor {
			t.Errorf("click %+v moved the cursor to %d", c, res.(model).cursor)
		}
	}
}

func TestQQuitsOnlyWithEmptyFilter(t *testing.T) {
	_, cmd := testModel(t).handleKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatal("q with an empty filter should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q cmd is not tea.Quit")
	}
	res, _ := testModel(t).handleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	res, _ = res.(model).handleKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if got := res.(model).ti.Value(); got != "xq" {
		t.Errorf("filter = %q, want q typed as text", got)
	}
}

// TestResizeList: shift+arrows move the divider, the frame still fits, and
// the position is there for the next run.
func TestResizeList(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	next, _ := testModel(t).Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m := next.(model)
	m.split = splitDefault
	m.resize()
	w := m.listW()
	shift := func(code rune) {
		next, _ := m.Update(tea.KeyPressMsg{Code: code, Mod: tea.ModShift})
		m = next.(model)
	}

	shift(tea.KeyRight)
	if m.listW() <= w || m.split != splitDefault-splitStep || loadSplit(stateDir()) != m.split {
		t.Errorf("grow: list %d -> %d, split=%d, saved=%d", w, m.listW(), m.split, loadSplit(stateDir()))
	}
	if m.listVP.Width() != m.listW() || m.prevVP.Width() != m.prevW() {
		t.Errorf("viewports %d | %d, want %d | %d", m.listVP.Width(), m.prevVP.Width(), m.listW(), m.prevW())
	}
	for i, l := range strings.Split(m.View().Content, "\n") {
		if got := ansi.StringWidth(l); got != m.width {
			t.Errorf("line %d is %d cells after the resize, want %d", i, got, m.width)
		}
	}

	shift(tea.KeyLeft)
	shift(tea.KeyLeft)
	if m.listW() >= w || m.split != splitDefault+splitStep {
		t.Errorf("shrink: list %d -> %d, split=%d", w, m.listW(), m.split)
	}
	for range 10 {
		shift(tea.KeyLeft)
	}
	if m.split != splitMax {
		t.Errorf("split should clamp at %d, got %d", splitMax, m.split)
	}
}

// TestMouseWheelFollowsThePointer: over the list the wheel moves the
// selection, as in asgitlog; anywhere else it scrolls the preview.
func TestMouseWheelFollowsThePointer(t *testing.T) {
	next, _ := testModel(t).Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m := next.(model)
	wheel := func(x int, b tea.MouseButton) {
		next, _ := m.Update(tea.MouseWheelMsg{X: x, Y: listY(false), Button: b})
		m = next.(model)
	}
	first := m.cursor
	wheel(2, tea.MouseWheelDown)
	if m.cursor <= first {
		t.Errorf("wheel down over the list: cursor %d -> %d", first, m.cursor)
	}
	wheel(2, tea.MouseWheelUp)
	if m.cursor != first {
		t.Errorf("wheel up over the list: cursor = %d, want %d", m.cursor, first)
	}
	m.prevVP.SetContent(strings.Repeat("line\n", 200))
	wheel(m.listW()+10, tea.MouseWheelDown)
	if m.cursor != first || m.prevVP.YOffset() == 0 {
		t.Errorf("wheel over the preview: cursor = %d, preview at %d", m.cursor, m.prevVP.YOffset())
	}
}

// The edge under the list carries the matches/total counter: group headers do
// not count and scrolling does not change it.
func TestCounterSitsUnderTheList(t *testing.T) {
	bottomEdge := func(m model) string {
		for _, l := range strings.Split(ansi.Strip(m.render()), "\n") {
			if strings.HasPrefix(l, "├") && strings.Contains(l, "┴") {
				return l
			}
		}
		return ""
	}
	m := testModel(t)
	if edge := bottomEdge(m); !strings.Contains(edge, "─ 2/2 ─┴") {
		t.Errorf("edge = %q, want 2/2 on the list's side", edge)
	}
	var issues []issue
	for i := 1; i <= 30; i++ {
		stack := "alpha"
		if i > 20 {
			stack = "beta"
		}
		issues = append(issues, issue{Key: "K-" + strconv.Itoa(i), Stack: stack, URL: "u" + strconv.Itoa(i), Summary: "s", Created: time.Unix(int64(1000-i), 0)})
	}
	m = newModel([]stack{jiraStack("alpha"), jiraStack("beta")}, issueCache{FetchedAt: time.Now(), Issues: issues}, false)
	res, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 16})
	m = res.(model)
	if edge := bottomEdge(m); !strings.Contains(edge, "─ 30/30 ─┴") {
		t.Errorf("edge = %q, want 30/30 on the list's side", edge)
	}
	for i := 0; i < 40; i++ {
		res, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = res.(model)
	}
	if edge := bottomEdge(m); !strings.Contains(edge, "─ 30/30 ─┴") {
		t.Errorf("scrolled to the bottom the edge = %q, want 30/30 still", edge)
	}
	if w := ansi.StringWidth(bottomEdge(m)); w != 120 {
		t.Errorf("the edge is %d cells wide, want 120", w)
	}
}

func TestSelectedRowKeepsItsMatches(t *testing.T) {
	var mm tea.Model = testModel(t)
	for _, r := range "readme" {
		mm, _ = mm.(model).handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m := mm.(model)
	r := m.rows[m.cursor]
	if r.kind != "issue" || len(r.idx) == 0 {
		t.Fatalf("the cursor should sit on the matching issue: %+v", r)
	}
	sel, plain := m.rowLine(r, true, m.keyW(), 200), m.rowLine(r, false, m.keyW(), 200)
	if !strings.Contains(sel, matchOver(stSel).Render("readme")) {
		t.Errorf("selected row lost its match: %q", sel)
	}
	if !strings.Contains(plain, stMatch.Render("readme")) {
		t.Errorf("row lost its match: %q", plain)
	}
	// (the selected row is also padded to the column, so compare without it)
	if strings.TrimRight(ansi.Strip(sel), " ")[len("▌ "):] != ansi.Strip(plain)[len("  "):] {
		t.Errorf("selecting a row changes only its gutter: %q vs %q", ansi.Strip(sel), ansi.Strip(plain))
	}
	// cutting the row keeps the ellipsis inside the last styled run
	cut := truncate(sel, 14)
	if ansi.StringWidth(cut) != 14 || !strings.Contains(ansi.Strip(cut), "…") {
		t.Errorf("cut = %q", cut)
	}
}

// TestPanel: f1 lays the keys over a frame that keeps its size, takes
// every key while it is open, and esc closes it before it quits. `?` is text
// for the filter. This tool has no options, so the panel lists the keys alone.
func TestPanel(t *testing.T) {
	m := testModel(t) // 120x30
	press := func(keys ...tea.KeyPressMsg) {
		for _, k := range keys {
			res, _ := m.Update(k)
			m = res.(model)
		}
	}
	closed := strings.Split(ansi.Strip(m.render()), "\n")
	if foot := closed[len(closed)-2]; !strings.Contains(foot, "f1 help") {
		t.Fatalf("the help line offers the panel: %q", foot)
	}
	list := m.listVP.Height()
	press(tea.KeyPressMsg{Code: tea.KeyF1})
	open := strings.Split(ansi.Strip(m.render()), "\n")
	if len(open) != len(closed) || m.listVP.Height() != list {
		t.Fatalf("the panel changed the frame: %d lines (list %d), want %d (list %d)", len(open), m.listVP.Height(), len(closed), list)
	}
	all := strings.Join(open, "\n")
	if strings.Contains(all, "Options") {
		t.Errorf("no options here, so no such section:\n%s", all)
	}
	for _, want := range []string{"╭─ help ", "Keys", "esc close", "pgup/pgdn", "⌥↑/⌥↓", "scroll the description", "resize the list"} {
		if !strings.Contains(all, want) {
			t.Errorf("the panel lacks %q:\n%s", want, all)
		}
	}
	for i, l := range open {
		if ansi.StringWidth(l) != m.width {
			t.Errorf("line %d is %d cells wide, want %d", i, ansi.StringWidth(l), m.width)
		}
	}
	cursor := m.cursor
	press(tea.KeyPressMsg{Code: 'z', Text: "z"}, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.ti.Value() != "" || m.cursor != cursor || !m.panel.open {
		t.Errorf("the panel should take every key: filter %q, cursor %d -> %d, open %v", m.ti.Value(), cursor, m.cursor, m.panel.open)
	}
	res, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = res.(model)
	if m.panel.open || cmd != nil {
		t.Errorf("esc closes the panel and nothing else: open=%v cmd=%v", m.panel.open, cmd)
	}
	press(tea.KeyPressMsg{Code: 'x', Text: "x"}, tea.KeyPressMsg{Code: '?', Text: "?"})
	if m.ti.Value() != "x?" || m.panel.open {
		t.Errorf("? is text: filter %q, panel open %v", m.ti.Value(), m.panel.open)
	}
	press(tea.KeyPressMsg{Code: tea.KeyF1})
	if !m.panel.open {
		t.Errorf("f1 opens the panel whatever the filter says")
	}
}

// TestScrollingUpRevealsTheGroupHeader: coming up onto the first issue of a
// stack shows the stack's name too, as asgotopr does with a repo (scrollTo in
// listnav.go).
func TestScrollingUpRevealsTheGroupHeader(t *testing.T) {
	m := testModel(t)
	first := -1 // the first issue of the last stack
	for i, r := range m.rows {
		if r.kind == "header" {
			first = i + 1
		}
	}
	// A list short enough to scroll down to that issue.
	for h := 12; h > 6 && m.listVP.Height() > len(m.rows)-first; h-- {
		res, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: h})
		m = res.(model)
	}
	m.listVP.SetYOffset(first)
	if m.listVP.YOffset() != first {
		t.Fatalf("could not scroll to row %d: %d rows in %d lines", first, len(m.rows), m.listVP.Height())
	}
	m.cursor = first
	m.ensureVisible()
	if got := m.listVP.YOffset(); got != first-1 {
		t.Errorf("offset = %d, want %d: the header above row %d should show", got, first-1, first)
	}
}

// TestEmptyListSaysWhy: a query that matches nothing says so in the list, as
// in every tool of the family (emptyList in listnav.go).
func TestEmptyListSaysWhy(t *testing.T) {
	m := testModel(t)
	for _, r := range "zzzzqq" {
		res, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = res.(model)
	}
	if len(m.rows) != 0 {
		t.Fatalf("the query should match nothing, got %d rows", len(m.rows))
	}
	list := ansi.Strip(m.listLines()[0])
	if !strings.HasPrefix(list, " No matches") {
		t.Errorf("the list should say there are no matches: %q", list)
	}
}

// TestPasteFilters: a paste changes the query without a key press, and the
// list must follow it (toInput). A key that leaves the query alone must not
// move the cursor off the row it is on.
func TestPasteFilters(t *testing.T) {
	m := testModel(t)
	res, _ := m.Update(tea.PasteMsg{Content: "zzzzqq"})
	m = res.(model)
	if m.ti.Value() != "zzzzqq" || len(m.rows) != 0 {
		t.Fatalf("a paste should filter: query %q, %d rows", m.ti.Value(), len(m.rows))
	}
	for range "zzzzqq" {
		res, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
		m = res.(model)
	}
	if len(m.rows) < 2 {
		t.Skipf("the fixture lists %d rows", len(m.rows))
	}
	res, _ = m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = res.(model)
	if len(m.rows) < 2 {
		t.Skipf("the query leaves %d rows", len(m.rows))
	}
	res, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = res.(model)
	at := m.cursor
	res, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = res.(model)
	if m.cursor != at {
		t.Errorf("a key that does not edit the query moved the cursor: %d -> %d", at, m.cursor)
	}
	m.panel.open = true
	res, _ = m.Update(tea.PasteMsg{Content: "xx"})
	if got := res.(model).ti.Value(); got != "a" {
		t.Errorf("a paste under the panel should be dropped, the query is %q", got)
	}
}

// TestSelectedRowSpansListWidth: the highlight reaches the divider, as in
// every picker, and a long summary is cut to the column.
func TestSelectedRowSpansListWidth(t *testing.T) {
	m := testModel(t)
	w := m.listW()
	r := m.rows[m.cursor]
	got := m.rowLine(r, true, m.keyW(), w)
	if n := ansi.StringWidth(got); n != w || !strings.HasSuffix(ansi.Strip(got), " ") {
		t.Errorf("selected row is %d cells, want %d padded: %q", n, w, ansi.Strip(got))
	}
	r.e.it.Summary = strings.Repeat("x", 200)
	for _, sel := range []bool{true, false} {
		if n := ansi.StringWidth(m.rowLine(r, sel, m.keyW(), w)); n > w || sel && n != w {
			t.Errorf("long row (selected=%v) is %d cells, want %d", sel, n, w)
		}
	}
}

// TestNetworkErrorGivesTheHelpLineBack: a failed refresh takes the help line
// like a notice, until the next key; the edge over the input keeps saying the
// list is the cached one.
func TestNetworkErrorGivesTheHelpLineBack(t *testing.T) {
	m := testModel(t)
	m.netErr, m.stale = "gh: could not resolve host", true
	plain := strings.Split(ansi.Strip(m.render()), "\n")
	if !strings.Contains(plain[len(plain)-2], "could not resolve host") || !strings.Contains(plain[0], "refresh failed") {
		t.Fatalf("the error should take the help line and mark the edge:\n%s\n%s", plain[0], plain[len(plain)-2])
	}
	res, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	plain = strings.Split(ansi.Strip(res.(model).render()), "\n")
	if !strings.Contains(plain[len(plain)-2], "type filter") || !strings.Contains(plain[0], "refresh failed") {
		t.Errorf("the next key gives the help line back, the mark stays:\n%s\n%s", plain[0], plain[len(plain)-2])
	}
}

// TestSpaceIsNotAQuery: a query without terms (spaces, a bare ~) searches for
// nothing, so it neither ranks the list nor moves the cursor (hasTerms).
func TestSpaceIsNotAQuery(t *testing.T) {
	m := testModel(t)
	res, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = res.(model)
	at, rows := m.cursor, len(m.rows)
	for _, k := range []string{" ", "~"} {
		res, _ = m.Update(tea.KeyPressMsg{Code: []rune(k)[0], Text: k})
		m = res.(model)
		if m.cursor != at || len(m.rows) != rows {
			t.Errorf("after %q: cursor %d -> %d, rows %d -> %d", k, at, m.cursor, rows, len(m.rows))
		}
	}
}
