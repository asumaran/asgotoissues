package main

// Local work on a PR's branch: for each of my open PRs, the checkout of its
// head on this machine (a worktree of a repo under the configured roots,
// matched by the origin remote's owner/repo AND the branch: the same branch
// name lives in several repos) and what it holds that GitHub has not seen,
// in the counters the shell prompt and Claude Code's statusline use: ↑n
// commits not pushed, +n staged, !n unstaged, ?n untracked. Nothing here
// goes over the network; it runs in the background and a failure says
// nothing.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// localState is what a checkout holds that its PR does not.
type localState struct {
	ahead, staged, unstaged, untracked int
}

func (l localState) empty() bool {
	return l.ahead == 0 && l.staged == 0 && l.unstaged == 0 && l.untracked == 0
}

// localMsg carries the states of the checkouts, by PR URL.
type localMsg struct{ states map[string]localState }

// gitRun runs git in dir; the tests replace it.
var gitRun = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	return cmd.Output()
}

// localRoots are the directories whose repos are scanned for checkouts:
// ASGOTOISSUES_CHECKOUTS (paths joined by the list separator), else
// ~/Developer.
func localRoots() []string {
	if v := os.Getenv("ASGOTOISSUES_CHECKOUTS"); v != "" {
		return filepath.SplitList(v)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, "Developer")}
}

// localCmd works out the local states of my open PRs.
func localCmd(pulls []pull) tea.Cmd {
	var mine []pull
	for _, p := range pulls {
		if p.Mine && p.State == prOpen && p.Head != "" {
			mine = append(mine, p)
		}
	}
	if len(mine) == 0 {
		return nil
	}
	roots := localRoots()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return localMsg{localStates(ctx, roots, mine)}
	}
}

// remoteRe takes owner/repo out of an origin URL, ssh or https.
var remoteRe = regexp.MustCompile(`[:/]([^/:]+/[^/]+?)(?:\.git)?/?$`)

// localStates finds the checkouts of the PRs under roots and reads them.
func localStates(ctx context.Context, roots []string, pulls []pull) map[string]localState {
	checkouts := map[string]string{} // owner/repo@branch → worktree path
	for _, root := range roots {
		dirs, _ := os.ReadDir(root)
		for _, d := range dirs {
			dir := filepath.Join(root, d.Name())
			if !d.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
				continue
			}
			out, err := gitRun(ctx, dir, "remote", "get-url", "origin")
			if err != nil {
				continue
			}
			m := remoteRe.FindStringSubmatch(strings.TrimSpace(string(out)))
			if m == nil {
				continue
			}
			repo := strings.ToLower(m[1])
			wt, err := gitRun(ctx, dir, "worktree", "list", "--porcelain")
			if err != nil {
				continue
			}
			path := ""
			for _, l := range strings.Split(string(wt), "\n") {
				if p, ok := strings.CutPrefix(l, "worktree "); ok {
					path = p
				}
				if b, ok := strings.CutPrefix(l, "branch refs/heads/"); ok && path != "" {
					checkouts[repo+"@"+b] = path
				}
			}
		}
	}
	out := map[string]localState{}
	for _, p := range pulls {
		path, ok := checkouts[strings.ToLower(p.Repo)+"@"+p.Head]
		if !ok {
			continue
		}
		st := readCheckout(ctx, path, p.HeadOID)
		if !st.empty() {
			out[p.URL] = st
		}
	}
	return out
}

// readCheckout counts what a worktree holds: the commits on top of the PR's
// head (when that commit is here and HEAD descends from it: no upstream
// needed) and the files git status lists, as the prompt counts them.
func readCheckout(ctx context.Context, path, headOID string) localState {
	var st localState
	if out, err := gitRun(ctx, path, "status", "--porcelain=v1", "--untracked-files=normal"); err == nil {
		for _, l := range strings.Split(string(out), "\n") {
			if len(l) < 2 {
				continue
			}
			if l[:2] == "??" {
				st.untracked++
				continue
			}
			if l[0] != ' ' {
				st.staged++
			}
			if l[1] != ' ' {
				st.unstaged++
			}
		}
	}
	if headOID != "" {
		if _, err := gitRun(ctx, path, "merge-base", "--is-ancestor", headOID, "HEAD"); err == nil {
			if out, err := gitRun(ctx, path, "rev-list", "--count", headOID+"..HEAD"); err == nil {
				st.ahead, _ = strconv.Atoi(strings.TrimSpace(string(out)))
			}
		}
	}
	return st
}

// localFlags are the counters as the details line says them, each hidden at
// zero: ↑ pink, + green, ! yellow, ? grey, as the prompt draws them.
func localFlags(st localState) []localCounter {
	var out []localCounter
	add := func(sign string, n int, color string) {
		if n > 0 {
			out = append(out, localCounter{sign + strconv.Itoa(n), color})
		}
	}
	add("↑", st.ahead, "212")
	add("+", st.staged, "84")
	add("!", st.unstaged, "228")
	add("?", st.untracked, "245")
	return out
}

type localCounter struct {
	text  string
	color string // a 256-color index
}
