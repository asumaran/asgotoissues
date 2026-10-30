package main

// The gh account a stack's GitHub goes through (`github.account` in the
// config). gh keeps one active account per host, and a user signed in with
// two (a personal one and a work one) sees, through the active one, only the
// owners it belongs to. A stack that names its account travels it with the
// context to ghRun, which runs gh with that account's token (what `gh auth
// token --user` answers) as GH_TOKEN. A stack without one goes through the
// active account, as before.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

type ghAccountKey struct{}

// withGHAccount hands the account to the gh runs made under ctx. An empty
// account leaves ctx as it is.
func withGHAccount(ctx context.Context, account string) context.Context {
	if account == "" {
		return ctx
	}
	return context.WithValue(ctx, ghAccountKey{}, account)
}

// ghAccount is the account the gh runs under ctx go through, "" for gh's
// active one.
func ghAccount(ctx context.Context) string {
	s, _ := ctx.Value(ghAccountKey{}).(string)
	return s
}

// ghToken is the token gh keeps for an account. The tests replace it.
var ghToken = func(ctx context.Context, account string) (string, error) {
	out, err := exec.CommandContext(ctx, "gh", "auth", "token", "--user", account).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("gh account %s: %s", account, firstLine(string(ee.Stderr)))
		}
		if errors.Is(err, exec.ErrNotFound) {
			return "", errors.New("gh not found (install the GitHub CLI)")
		}
		return "", fmt.Errorf("gh account %s: %w", account, err)
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return "", fmt.Errorf("gh account %s: no token", account)
	}
	return tok, nil
}

// ghTokens holds the token of every account asked for, once per run.
var ghTokens = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// ghEnv is the environment a gh run under ctx takes: nil for the process's
// own (gh's active account), else the process's with GH_TOKEN set to the
// account's token. A failure to get the token is the run's failure.
func ghEnv(ctx context.Context) ([]string, error) {
	account := ghAccount(ctx)
	if account == "" {
		return nil, nil
	}
	ghTokens.Lock()
	tok, ok := ghTokens.m[account]
	ghTokens.Unlock()
	if !ok {
		var err error
		if tok, err = ghToken(ctx, account); err != nil {
			return nil, err
		}
		ghTokens.Lock()
		ghTokens.m[account] = tok
		ghTokens.Unlock()
	}
	return append(os.Environ(), "GH_TOKEN="+tok), nil
}
