package main

import (
	"os"
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
	m := newModel([]stack{{Name: "alpha"}, {Name: "beta"}}, issueCache{FetchedAt: time.Now(), Issues: issues}, false, "> ")
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

func TestEscQuitsWithoutURL(t *testing.T) {
	m := testModel(t)
	mm, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil || mm.(model).openURL != "" {
		t.Errorf("esc should quit without an action")
	}
}

func TestStackMsgPartialFailureKeepsCache(t *testing.T) {
	m := testModel(t)
	m.refreshing = true
	m.pending = 2
	m.cache.FetchedAt = time.Unix(1000, 0)
	// alpha fails, beta returns a new ticket
	mm, _ := m.Update(stackMsg{stack: "alpha", err: errTest("boom")})
	mm, _ = mm.(model).Update(stackMsg{stack: "beta", issues: []issue{
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
	dir, err := os.MkdirTemp("", "gotojira-test")
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
		!strings.Contains(plain[mainY(false)], "┬") || !strings.Contains(plain[0], "2/2") ||
		!strings.HasPrefix(plain[1], "│ > ") {
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
