package main

// Jumping to the herdr workspace of the selected row (ctrl+w): the row's
// workspace is resolved here, in a tea.Cmd, so a failure can flash with the
// TUI alive; the execution (workspace focus / worktree open) runs after quit
// in main.go, like openURL, because the popup closes with the process and its
// closing could steal the focus back.
//
// A workspace is matched, first in sidebar order winning: by checkout path
// (the row's PR branches resolved to local paths with the scan local.go
// uses; the only match that tells two repos apart), then by its ticket token
// (ticket rows only), then by its label holding the key (a workspace herdr
// has no worktree metadata for). When no workspace matches but a branch has
// a checkout on disk, that checkout opens as one.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// herdrRun runs herdr and returns its stdout. A failure is said the way
// herdr said it (the first line of its stderr), and a missing herdr in plain
// words. The tests replace it.
var herdrRun = func(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "herdr", args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("herdr: %s", firstLine(string(ee.Stderr)))
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("herdr not found")
		}
		return out, fmt.Errorf("herdr: %w", err)
	}
	return out, nil
}

// insideHerdr reports whether the process runs in a herdr pane (the plugin
// popup and a shell inside herdr alike).
func insideHerdr() bool { return os.Getenv("HERDR_ENV") == "1" }

// workspace is the part of herdr workspace list the jump reads.
type workspace struct {
	ID     string `json:"workspace_id"`
	Label  string `json:"label"`
	Tokens struct {
		Ticket string `json:"ticket"`
	} `json:"tokens"`
	Worktree struct {
		CheckoutPath string `json:"checkout_path"`
	} `json:"worktree"`
}

// wsEnvelope is the CLI's JSON envelope around the list.
type wsEnvelope struct {
	Result struct {
		Workspaces []workspace `json:"workspaces"`
	} `json:"result"`
}

// wsTarget is what the selected row points at: the ticket's key (empty on a
// PR row) and the repo+branch pairs of its PRs (the row's own PR, or every
// PR of the ticket).
type wsTarget struct {
	key      string
	branches []repoBranch
}

// repoBranch names a PR's head: owner/repo lower-cased, the branch as git
// spells it.
type repoBranch struct{ repo, branch string }

// wsMsg is the resolution: the workspace to focus, the checkout to open as
// one, or why neither could be found.
type wsMsg struct {
	focusID  string
	openPath string
	err      error
}

// rowTarget is what ctrl+w resolves for a row: a PR row its own head, a
// ticket row its key and the heads of its PRs.
func rowTarget(r *row) wsTarget {
	if r.kind == rowPull {
		return wsTarget{branches: []repoBranch{{strings.ToLower(r.p.Repo), r.p.Head}}}
	}
	t := wsTarget{key: r.e.it.Key}
	for _, p := range r.e.pulls {
		t.branches = append(t.branches, repoBranch{strings.ToLower(p.Repo), p.Head})
	}
	return t
}

// focusWorkspaceCmd resolves t to a workspace or a checkout, in the match
// order above. The checkout scan only runs when the row has branches to
// resolve.
func focusWorkspaceCmd(t wsTarget) tea.Cmd {
	roots := localRoots()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, err := herdrRun(ctx, "workspace", "list")
		if err != nil {
			return wsMsg{err: err}
		}
		var env wsEnvelope
		if err := json.Unmarshal(out, &env); err != nil {
			return wsMsg{err: fmt.Errorf("herdr workspace list: %v", err)}
		}
		wss := env.Result.Workspaces
		var paths []string
		if len(t.branches) > 0 {
			checkouts := scanCheckouts(ctx, roots)
			for _, rb := range t.branches {
				if p, ok := checkouts[rb.repo+"@"+rb.branch]; ok {
					paths = append(paths, p)
				}
			}
		}
		for _, w := range wss {
			for _, p := range paths {
				if w.Worktree.CheckoutPath == p {
					return wsMsg{focusID: w.ID}
				}
			}
		}
		if t.key != "" {
			for _, w := range wss {
				if strings.EqualFold(w.Tokens.Ticket, t.key) {
					return wsMsg{focusID: w.ID}
				}
			}
			for _, w := range wss {
				if labelHasKey(w.Label, t.key) {
					return wsMsg{focusID: w.ID}
				}
			}
		}
		if len(paths) > 0 {
			return wsMsg{openPath: paths[0]}
		}
		what := t.key
		if what == "" && len(t.branches) > 0 {
			what = t.branches[0].branch
		}
		return wsMsg{err: fmt.Errorf("no workspace for %s", what)}
	}
}

// labelHasKey reports whether label holds key (case-insensitive) with
// non-alphanumeric boundaries: fix-ESHOP-530-x holds ESHOP-530 but not
// SHOP-53.
func labelHasKey(label, key string) bool {
	l, k := strings.ToLower(label), strings.ToLower(key)
	if k == "" {
		return false
	}
	for i := 0; ; i++ {
		j := strings.Index(l[i:], k)
		if j < 0 {
			return false
		}
		i += j
		if (i == 0 || !isAlnum(l[i-1])) && (i+len(k) == len(l) || !isAlnum(l[i+len(k)])) {
			return true
		}
	}
}

func isAlnum(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// jumpWorkspace executes the resolution after quit: focus the workspace, or
// open the checkout as one (focused). herdr opens a worktree from the repo
// parent workspace (a linked worktree as --cwd is refused), so --cwd is the
// repo root: the parent of the common git dir.
func jumpWorkspace(focusID, openPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if focusID != "" {
		_, err := herdrRun(ctx, "workspace", "focus", focusID)
		return err
	}
	root := openPath
	if out, err := gitRun(ctx, openPath, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		root = filepath.Dir(strings.TrimSpace(string(out)))
	}
	_, err := herdrRun(ctx, "worktree", "open", "--cwd", root, "--path", openPath, "--focus")
	return err
}
