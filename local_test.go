package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLocalStates: a checkout is found by the origin's owner/repo and the
// branch (the same branch in another repo is not it), and read as the
// prompt counts it: the commits on top of the PR's head (no upstream
// needed), staged, unstaged and untracked files. A PR without a checkout,
// or one with nothing in it, says nothing.
func TestLocalStates(t *testing.T) {
	root := t.TempDir()
	for _, repo := range []string{"front", "infra", "notes"} {
		if err := os.MkdirAll(filepath.Join(root, repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	orig := gitRun
	t.Cleanup(func() { gitRun = orig })
	gitRun = func(_ context.Context, dir string, args ...string) ([]byte, error) {
		repo := filepath.Base(dir)
		switch strings.Join(args, " ") {
		case "remote get-url origin":
			switch repo {
			case "front":
				return []byte("git@github.com:Acme/front.git\n"), nil
			case "infra":
				return []byte("https://github.com/acme/infra\n"), nil
			}
			return nil, errors.New("no origin")
		case "worktree list --porcelain":
			return []byte("worktree " + dir + "\nHEAD abc\nbranch refs/heads/main\n\nworktree /wt/" + repo + "/feat\nHEAD def\nbranch refs/heads/feat/x\n"), nil
		case "status --porcelain=v1 --untracked-files=normal":
			if dir == "/wt/front/feat" {
				return []byte("M  staged.go\n M edited.go\nMM both.go\n?? new.go\n"), nil
			}
			return nil, nil
		case "merge-base --is-ancestor h1 HEAD":
			return nil, nil
		case "rev-list --count h1..HEAD":
			return []byte("2\n"), nil
		}
		return nil, errors.New("unexpected: " + strings.Join(args, " "))
	}
	pulls := []pull{
		{URL: "front", Repo: "acme/front", Head: "feat/x", HeadOID: "h1"},
		{URL: "infra", Repo: "acme/infra", Head: "feat/x", HeadOID: "gone"}, // clean, its head not here
		{URL: "other", Repo: "acme/other", Head: "feat/x"},
	}
	got := localStates(context.Background(), []string{root}, pulls)
	if len(got) != 1 || got["front"] != (localState{ahead: 2, staged: 2, unstaged: 2, untracked: 1}) {
		t.Errorf("states = %+v", got)
	}
	var texts []string
	for _, c := range localFlags(got["front"]) {
		texts = append(texts, c.text)
	}
	if strings.Join(texts, " ") != "↑2 +2 !2 ?1" {
		t.Errorf("counters = %v", texts)
	}
}
