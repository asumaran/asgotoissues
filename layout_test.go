package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// TestLayoutTogglePersists: ctrl+l saves the layout and resizes the
// viewports right away, with no tea.WindowSizeMsg in between.
func TestLayoutTogglePersists(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	next, _ := testModel(t).Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m := next.(model)
	if m.layout != layoutColumns {
		t.Fatalf("default layout = %s, want columns", m.layout)
	}
	res, cmd := m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	m = res.(model)
	if m.layout != layoutRows || loadSetting(stateDir(), "layout") != "rows" || cmd == nil {
		t.Errorf("ctrl+l: layout = %s, saved %q", m.layout, loadSetting(stateDir(), "layout"))
	}
	if m.listVP.Width() != m.innerW() || m.listVP.Height() != m.listH() || m.prevVP.Width() != m.prevW() {
		t.Errorf("the viewports did not resize right after the key: list %dx%d, preview width %d",
			m.listVP.Width(), m.listVP.Height(), m.prevVP.Width())
	}

	res, _ = m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	m = res.(model)
	if m.layout != layoutColumns || loadSetting(stateDir(), "layout") != "columns" {
		t.Errorf("ctrl+l again: layout = %s, saved %q", m.layout, loadSetting(stateDir(), "layout"))
	}
}

// hasColumnsDivider reports whether a rendered frame has the columns
// layout's vertical divider (┬ over the list, ┴ under it); the rows layout
// has neither.
func hasColumnsDivider(plain string) bool {
	return strings.Contains(plain, "┬") || strings.Contains(plain, "┴")
}

// TestRowsLayoutFrame: at several sizes the frame is exactly height lines of
// width cells in the rows layout too, with no vertical divider, and it
// falls back to columns when the body is too short (the same floor
// TestFrameGeometry pins for the columns layout).
func TestRowsLayoutFrame(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := testModel(t)
	m.layout = layoutRows
	for _, size := range [][2]int{{120, 30}, {94, 24}, {90, 16}, {61, 12}, {40, 10}, {25, 7}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		mm := next.(model)
		lines := strings.Split(mm.render(), "\n")
		if len(lines) != size[1] {
			t.Errorf("%v: %d lines, want %d", size, len(lines), size[1])
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w != size[0] {
				t.Errorf("%v: line %d is %d cells, want %d", size, i, w, size[0])
			}
		}
		plain := ansi.Strip(mm.render())
		switch wantRows := mm.bodyH() >= 5; {
		case wantRows && hasColumnsDivider(plain):
			t.Errorf("%v: the rows layout should have no vertical divider:\n%s", size, plain)
		case wantRows && mm.listW() != mm.innerW():
			t.Errorf("%v: the rows layout's list should take the whole width: %d != %d", size, mm.listW(), mm.innerW())
		case !wantRows && !hasColumnsDivider(plain):
			t.Errorf("%v: a body under 5 lines should fall back to columns:\n%s", size, plain)
		}
	}
}

// TestNarrowScreenFallsBackToRows: a saved columns setting still draws rows
// under minColumnsW, as long as the body is tall enough, and the fallback
// never touches the saved setting.
func TestNarrowScreenFallsBackToRows(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	next, _ := testModel(t).Update(tea.WindowSizeMsg{Width: 50, Height: 24})
	m := next.(model)
	if m.layout != layoutColumns {
		t.Fatalf("the saved setting should stay columns, got %s", m.layout)
	}
	if m.columns() {
		t.Errorf("width 50 < minColumnsW should fall back to rows")
	}
	if loadSetting(stateDir(), "layout") != "" {
		t.Errorf("the narrow fallback must never write the setting: %q", loadSetting(stateDir(), "layout"))
	}
}

// TestResizeListPerLayout: the divider moves along its own axis. In the
// rows layout shift+up/down move split-rows and leave split-columns where
// it was, and shift+left/right do nothing; in columns it is the other way
// round. Neither pair scrolls the preview any more.
func TestResizeListPerLayout(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	next, _ := testModel(t).Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m := next.(model)
	shift := func(code rune) {
		res, _ := m.Update(tea.KeyPressMsg{Code: code, Mod: tea.ModShift})
		m = res.(model)
	}
	shift(tea.KeyDown)
	shift(tea.KeyUp)
	if m.splitColumns != splitColumnsDefault || loadSetting(stateDir(), "split-columns") != "" || loadSetting(stateDir(), "split-rows") != "" || m.prevVP.YOffset() != 0 {
		t.Errorf("columns: shift+up/down resized or scrolled: columns=%d rows=%q offset=%d", m.splitColumns, loadSetting(stateDir(), "split-rows"), m.prevVP.YOffset())
	}

	res, _ := m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl}) // rows
	m = res.(model)
	h := m.listH()
	shift(tea.KeyLeft)
	shift(tea.KeyRight)
	if m.splitRows != splitRowsDefault || loadSetting(stateDir(), "split-rows") != "" || m.listH() != h {
		t.Errorf("rows: shift+left/right resized: split-rows=%d list %d -> %d", m.splitRows, h, m.listH())
	}
	shift(tea.KeyDown)
	if m.listH() <= h || m.splitRows != splitRowsDefault-splitStep || loadSplit(stateDir(), splitRowsFile, splitRowsDefault) != m.splitRows {
		t.Errorf("rows: shift+down grows the list: %d -> %d, split-rows=%d", h, m.listH(), m.splitRows)
	}
	shift(tea.KeyUp)
	if m.listH() != h || m.splitRows != splitRowsDefault {
		t.Errorf("rows: shift+up shrinks it back: %d, split-rows=%d", m.listH(), m.splitRows)
	}
	if m.splitColumns != splitColumnsDefault || loadSetting(stateDir(), "split-columns") != "" {
		t.Errorf("resizing in rows touched split-columns: %d", m.splitColumns)
	}
}

// manyIssuesModel is a tall list of tickets in one stack, wide enough for
// the rows layout to have room to scroll.
func manyIssuesModel(t *testing.T, n int) model {
	t.Helper()
	var issues []issue
	for i := 1; i <= n; i++ {
		issues = append(issues, issue{Key: "K-" + strconv.Itoa(i), Stack: "alpha", URL: "u" + strconv.Itoa(i),
			Summary: "ticket " + strconv.Itoa(i), Created: time.Unix(int64(10000-i), 0)})
	}
	m := newModel([]stack{jiraStack("alpha")}, issueCache{FetchedAt: time.Now(), Issues: issues}, false)
	m.layout = layoutRows
	res, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	return res.(model)
}

// TestClickInRowsLayout: a click selects the row under it even with the
// list scrolled and two-line rows; a click on the divider or the preview
// selects nothing.
func TestClickInRowsLayout(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := manyIssuesModel(t, 20)
	if m.columns() {
		t.Fatalf("expected the rows layout at this size")
	}
	for range 15 {
		res, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = res.(model)
	}
	if m.listVP.YOffset() == 0 {
		t.Fatalf("the list should have scrolled")
	}

	off := m.listVP.YOffset()
	row0, ok := rowOfLine(off, m.lineOf)
	if !ok {
		t.Fatal("no row on the first visible line")
	}
	res, _ := m.Update(tea.MouseClickMsg{X: 3, Y: listY, Button: tea.MouseLeft})
	if got := res.(model).cursor; got != row0 {
		t.Errorf("click on the first visible line selected %d, want the scrolled row %d", got, row0)
	}
	row1, ok := rowOfLine(off+1, m.lineOf)
	if !ok {
		t.Fatal("no row on the second visible line")
	}
	res, _ = m.Update(tea.MouseClickMsg{X: 3, Y: listY + 1, Button: tea.MouseLeft})
	if got := res.(model).cursor; got != row1 {
		t.Errorf("click on the second visible line selected %d, want %d", got, row1)
	}

	before := m.cursor
	for _, y := range []int{listY + m.listH(), listY + m.listH() + 2} {
		res, _ = m.Update(tea.MouseClickMsg{X: 3, Y: y, Button: tea.MouseLeft})
		if got := res.(model).cursor; got != before {
			t.Errorf("a click at y=%d (the divider or the preview) moved the cursor to %d", y, got)
		}
	}
}

// TestWheelInRowsLayout: the wheel over the preview (below the divider)
// scrolls the preview, not the list; over the list it still moves the
// selection.
func TestWheelInRowsLayout(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	m := manyIssuesModel(t, 20)
	if m.columns() {
		t.Fatalf("expected the rows layout at this size")
	}
	m.prevVP.SetContent(strings.Repeat("line\n", 200))
	first := m.cursor

	y := listY + m.listH() + 2 // inside the preview, below the divider
	res, _ := m.Update(tea.MouseWheelMsg{X: 3, Y: y, Button: tea.MouseWheelDown})
	after := res.(model)
	if after.cursor != first || after.prevVP.YOffset() == 0 {
		t.Errorf("wheel over the preview in rows: cursor %d (want %d), preview offset %d (want > 0)",
			after.cursor, first, after.prevVP.YOffset())
	}

	res, _ = m.Update(tea.MouseWheelMsg{X: 3, Y: listY, Button: tea.MouseWheelDown})
	after = res.(model)
	if after.cursor == first {
		t.Errorf("wheel over the list in rows should still move the selection")
	}
}
