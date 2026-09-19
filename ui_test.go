package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

func testModel(t *testing.T) model {
	t.Helper()
	issues := []issue{
		{Key: "PLAT-100", Stack: "alpha", URL: "https://a.example/browse/PLAT-100", Summary: "fix login flow",
			Status: "In Progress", StatusCat: "In Progress", Type: "Task", Updated: time.Unix(300, 0), Description: "Some *body* text"},
		{Key: "BETA-7", Stack: "beta", URL: "https://b.example/browse/BETA-7", Summary: "update readme",
			Status: "To Do", StatusCat: "To Do", Type: "Story", Updated: time.Unix(100, 0)},
	}
	m := model{
		stacks:  []stack{{Name: "alpha"}, {Name: "beta"}},
		cache:   issueCache{FetchedAt: time.Now(), Issues: issues},
		ti:      newFilterInput("> "),
		listVP:  viewport.New(viewport.WithWidth(50), viewport.WithHeight(20)),
		prevVP:  viewport.New(viewport.WithWidth(40), viewport.WithHeight(17)),
		help:    help.New(),
		keys:    defaultKeys(),
		renders: map[string]string{},
		width:   120,
		height:  30,
	}
	m.setEntries(issues)
	m.applyFilter()
	m.resize()
	m.renderList()
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
