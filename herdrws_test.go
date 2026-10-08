package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// wsFixture is herdr workspace list as the CLI prints it: w1 and w2 are the
// same branch in two repos (only the checkout path tells them apart), w3 and
// w5 report the same ticket token (sidebar order decides), w4 has no tokens
// or worktree (the label is all there is).
const wsFixture = `{"id":"cli:workspace:list","result":{"type":"workspace_list","workspaces":[
  {"workspace_id":"w1","label":"front-feat","tokens":{"ref":"feat/x"},"worktree":{"checkout_path":"/wt/front/feat","is_linked_worktree":true}},
  {"workspace_id":"w2","label":"infra-feat","tokens":{"ref":"feat/x"},"worktree":{"checkout_path":"/wt/infra/feat","is_linked_worktree":true}},
  {"workspace_id":"w3","label":"work-eshop-9","tokens":{"ticket":"ESHOP-9"}},
  {"workspace_id":"w4","label":"fix-ESHOP-77-coverage","tokens":{"title":"fix-ESHOP-77-coverage"}},
  {"workspace_id":"w5","label":"second-eshop-9","tokens":{"ticket":"eshop-9"}}
]}}`

// resolveWS runs focusWorkspaceCmd against the fixture and a fake checkout
// scan: front and infra both hold feat/x, front alone holds feat/y, and only
// feat/y's checkout is missing from the sidebar.
func resolveWS(t *testing.T, target wsTarget) wsMsg {
	t.Helper()
	root := t.TempDir()
	for _, repo := range []string{"front", "infra"} {
		if err := os.MkdirAll(filepath.Join(root, repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ASGOTOISSUES_CHECKOUTS", root)
	origGit, origHerdr := gitRun, herdrRun
	t.Cleanup(func() { gitRun, herdrRun = origGit, origHerdr })
	gitRun = func(_ context.Context, dir string, args ...string) ([]byte, error) {
		repo := filepath.Base(dir)
		switch strings.Join(args, " ") {
		case "remote get-url origin":
			return []byte("git@github.com:Acme/" + repo + ".git\n"), nil
		case "worktree list --porcelain":
			out := "worktree " + dir + "\nbranch refs/heads/main\n\nworktree /wt/" + repo + "/feat\nbranch refs/heads/feat/x\n"
			if repo == "front" {
				out += "\nworktree /wt/front/featy\nbranch refs/heads/feat/y\n"
			}
			return []byte(out), nil
		}
		return nil, errors.New("unexpected: " + strings.Join(args, " "))
	}
	herdrRun = func(_ context.Context, args ...string) ([]byte, error) {
		if strings.Join(args, " ") != "workspace list" {
			return nil, errors.New("unexpected: " + strings.Join(args, " "))
		}
		return []byte(wsFixture), nil
	}
	msg := focusWorkspaceCmd(target)()
	ws, ok := msg.(wsMsg)
	if !ok {
		t.Fatalf("msg = %#v", msg)
	}
	return ws
}

// TestWorkspaceResolution covers the match order: the checkout path first
// (the only match that tells two repos apart, and it beats the ticket
// token), then the ticket token (the first workspace in sidebar order wins),
// then the label with non-alphanumeric boundaries; with no workspace but a
// checkout on disk the checkout opens, and with nothing the key says so.
func TestWorkspaceResolution(t *testing.T) {
	cases := []struct {
		name   string
		target wsTarget
		want   wsMsg
	}{
		{"checkout path tells repos apart",
			wsTarget{branches: []repoBranch{{"acme/infra", "feat/x"}}}, wsMsg{focusID: "w2"}},
		{"checkout path beats the ticket token",
			wsTarget{key: "ESHOP-9", branches: []repoBranch{{"acme/front", "feat/x"}}}, wsMsg{focusID: "w1"}},
		{"ticket token, first in sidebar order",
			wsTarget{key: "eshop-9"}, wsMsg{focusID: "w3"}},
		{"label fallback",
			wsTarget{key: "ESHOP-77"}, wsMsg{focusID: "w4"}},
		{"label needs boundaries",
			wsTarget{key: "SHOP-77"}, wsMsg{err: errors.New("no workspace for SHOP-77")}},
		{"a checkout without a workspace opens",
			wsTarget{key: "PLAT-5", branches: []repoBranch{{"acme/front", "feat/y"}}}, wsMsg{openPath: "/wt/front/featy"}},
		{"nothing",
			wsTarget{key: "NOPE-1"}, wsMsg{err: errors.New("no workspace for NOPE-1")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveWS(t, c.target)
			if got.focusID != c.want.focusID || got.openPath != c.want.openPath {
				t.Errorf("resolved %+v, want %+v", got, c.want)
			}
			switch {
			case c.want.err == nil && got.err != nil:
				t.Errorf("err = %v", got.err)
			case c.want.err != nil && (got.err == nil || got.err.Error() != c.want.err.Error()):
				t.Errorf("err = %v, want %v", got.err, c.want.err)
			}
		})
	}
}

// TestRowTarget: a PR row is its own head only; a ticket row is its key and
// the heads of every PR under it.
func TestRowTarget(t *testing.T) {
	p := pull{Repo: "Acme/Front", Head: "feat/x"}
	e := &entry{it: issue{Key: "PLAT-1"}, pulls: []*pull{&p}}
	got := rowTarget(&row{kind: rowPull, p: &p, pe: e})
	if got.key != "" || len(got.branches) != 1 || got.branches[0] != (repoBranch{"acme/front", "feat/x"}) {
		t.Errorf("pull row target = %+v", got)
	}
	got = rowTarget(&row{kind: rowIssue, e: e})
	if got.key != "PLAT-1" || len(got.branches) != 1 || got.branches[0] != (repoBranch{"acme/front", "feat/x"}) {
		t.Errorf("issue row target = %+v", got)
	}
}

func TestLabelHasKey(t *testing.T) {
	for _, c := range []struct {
		label, key string
		want       bool
	}{
		{"fix-ESHOP-530-coverage", "eshop-530", true},
		{"ESHOP-530", "ESHOP-530", true},
		{"fix-ESHOP-5301-x", "ESHOP-530", false}, // a digit continues the key
		{"fix-MESHOP-530-x", "ESHOP-530", false}, // a letter precedes it
		{"meshop-530 eshop-530", "ESHOP-530", true},
		{"anything", "", false},
	} {
		if got := labelHasKey(c.label, c.key); got != c.want {
			t.Errorf("labelHasKey(%q, %q) = %v", c.label, c.key, got)
		}
	}
}

// TestJumpWorkspace pins the commands the post-quit jump runs: a workspace
// is focused by id; a checkout opens with --cwd at the repo root (the common
// git dir's parent: herdr refuses a linked worktree as --cwd) and --path at
// the worktree.
func TestJumpWorkspace(t *testing.T) {
	var ran [][]string
	origGit, origHerdr := gitRun, herdrRun
	t.Cleanup(func() { gitRun, herdrRun = origGit, origHerdr })
	herdrRun = func(_ context.Context, args ...string) ([]byte, error) {
		ran = append(ran, args)
		return nil, nil
	}
	gitRun = func(_ context.Context, dir string, args ...string) ([]byte, error) {
		if dir != "/wt/front/feat" || strings.Join(args, " ") != "rev-parse --path-format=absolute --git-common-dir" {
			return nil, errors.New("unexpected: " + dir + " " + strings.Join(args, " "))
		}
		return []byte("/dev/front/.git\n"), nil
	}
	if err := jumpWorkspace("w7", ""); err != nil {
		t.Fatal(err)
	}
	if err := jumpWorkspace("", "/wt/front/feat"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"workspace", "focus", "w7"},
		{"worktree", "open", "--cwd", "/dev/front", "--path", "/wt/front/feat", "--focus"},
	}
	if len(ran) != len(want) {
		t.Fatalf("ran %v", ran)
	}
	for i := range want {
		if strings.Join(ran[i], " ") != strings.Join(want[i], " ") {
			t.Errorf("command %d = %v, want %v", i, ran[i], want[i])
		}
	}
}

// TestCtrlWJumpKey: ctrl+w on an issue row resolves in the background (the
// stubbed herdr answers the error through wsMsg, so it would flash), a
// wsMsg with a workspace queues the jump and quits, one with an error
// flashes it, and the key never reaches the filter.
func TestCtrlWJumpKey(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	m := testModel(t)
	res, cmd := m.handleKey(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+w returned no command")
	}
	if got := res.(model).ti.Value(); got != "" {
		t.Errorf("ctrl+w leaked into the filter: %q", got)
	}
	ws, ok := cmd().(wsMsg)
	if !ok || ws.err == nil {
		t.Fatalf("resolution against the stubbed herdr = %#v", ws)
	}

	next, quit := res.(model).Update(wsMsg{focusID: "w7"})
	if got := next.(model); got.wsFocus != "w7" || got.wsOpen != "" {
		t.Errorf("wsFocus = %q, wsOpen = %q", got.wsFocus, got.wsOpen)
	}
	if !quitsNow(quit) {
		t.Errorf("a resolved workspace must quit the popup")
	}
	next, quit = res.(model).Update(wsMsg{openPath: "/wt/x"})
	if got := next.(model); got.wsOpen != "/wt/x" || !quitsNow(quit) {
		t.Errorf("wsOpen = %q", got.wsOpen)
	}

	next, _ = res.(model).Update(wsMsg{err: errors.New("no workspace for PLAT-100")})
	got := next.(model)
	if got.flash.text != "no workspace for PLAT-100" || !got.flash.bad {
		t.Errorf("error flash = %+v", got.flash)
	}
	if got.wsFocus != "" || got.wsOpen != "" {
		t.Errorf("an error must not queue a jump")
	}
}

// TestCtrlWGates: outside herdr the key flashes instead of resolving, and a
// row that opens nothing says so.
func TestCtrlWGates(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	m := testModel(t)
	res, _ := m.handleKey(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	got := res.(model)
	if got.flash.text != "not inside herdr" || !got.flash.bad {
		t.Errorf("outside herdr: flash = %+v", got.flash)
	}
	if got.ti.Value() != "" {
		t.Errorf("ctrl+w leaked into the filter: %q", got.ti.Value())
	}

	t.Setenv("HERDR_ENV", "1")
	m = testModel(t)
	m.cursor = 0 // the alpha header: selectable, opens nothing
	res, _ = m.handleKey(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	if got := res.(model); got.flash.text != "nothing to open" {
		t.Errorf("on a header: flash = %+v", got.flash)
	}
}
