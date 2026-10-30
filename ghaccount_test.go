package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeGhToken(t *testing.T, tokens map[string]string) *int {
	t.Helper()
	calls := 0
	prev := ghToken
	ghToken = func(_ context.Context, account string) (string, error) {
		calls++
		tok, ok := tokens[account]
		if !ok {
			return "", errors.New("gh account " + account + ": not logged in")
		}
		return tok, nil
	}
	t.Cleanup(func() {
		ghToken = prev
		ghTokens.Lock()
		ghTokens.m = map[string]string{}
		ghTokens.Unlock()
	})
	return &calls
}

func TestGhEnvSetsTheAccountToken(t *testing.T) {
	calls := fakeGhToken(t, map[string]string{"me-personal": "tok-1"})
	ctx := context.Background()
	if env, err := ghEnv(ctx); env != nil || err != nil {
		t.Errorf("without an account: env = %v, err = %v, want the process's own", env, err)
	}
	ctx = withGHAccount(ctx, "me-personal")
	if ghAccount(ctx) != "me-personal" || ghAccount(withGHAccount(context.Background(), "")) != "" {
		t.Errorf("the account does not travel with the context")
	}
	for i := 0; i < 2; i++ {
		env, err := ghEnv(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if env[len(env)-1] != "GH_TOKEN=tok-1" || len(env) < len(os.Environ()) {
			t.Errorf("env ends %q over %d vars, want GH_TOKEN over the process's env", env[len(env)-1], len(env))
		}
	}
	if *calls != 1 {
		t.Errorf("token asked %d times, want once per account", *calls)
	}
	if _, err := ghEnv(withGHAccount(context.Background(), "nobody")); err == nil || !strings.Contains(err.Error(), "nobody") {
		t.Errorf("an account gh does not know: %v", err)
	}
}

func TestGhRunUsesTheAccountToken(t *testing.T) {
	fakeGhToken(t, map[string]string{"me-personal": "tok-1"})
	dir := t.TempDir()
	script := "#!/bin/sh\necho \"token=$GH_TOKEN\"\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/bin:/usr/bin")
	t.Setenv("GH_TOKEN", "")
	os.Unsetenv("GH_TOKEN")
	if out, err := ghRun(context.Background(), "x"); err != nil || string(out) != "token=\n" {
		t.Errorf("without an account: out = %q, err = %v", out, err)
	}
	if out, err := ghRun(withGHAccount(context.Background(), "me-personal"), "x"); err != nil || string(out) != "token=tok-1\n" {
		t.Errorf("with an account: out = %q, err = %v", out, err)
	}
	if _, err := ghRun(withGHAccount(context.Background(), "nobody"), "x"); err == nil {
		t.Errorf("an account without a token should fail the run")
	}
}
