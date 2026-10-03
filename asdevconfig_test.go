package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAsdevStacks(t *testing.T) {
	doc := `---
stacks:
  zeta:
    jira:
      base_url: https://z.atlassian.net/
      email: me@z
      api_token_env: Z_TOKEN
      default_project: ZED
  alpha:
    issues: [github]
    github:
      org: Acme
      orgs: [acme, other]
---
# notes below the front matter are ignored
`
	stacks, err := parseAsdevStacks(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(stacks) != 2 || stacks[0].Name != "zeta" || stacks[1].Name != "alpha" {
		t.Fatalf("stacks = %+v, want zeta then alpha (file order)", stacks)
	}
	z := stacks[0]
	if z.BaseURL != "https://z.atlassian.net" || z.Type != "cloud" || z.Email != "me@z" || z.TokenEnv != "Z_TOKEN" || z.DefaultProject != "ZED" || z.Issues != nil {
		t.Errorf("zeta = %+v", z)
	}
	if z.host() != "z.atlassian.net" {
		t.Errorf("host = %q", z.host())
	}
	a := stacks[1]
	if strings.Join(a.Orgs, ",") != "Acme,other" || strings.Join(a.Issues, ",") != "github" || a.BaseURL != "" {
		t.Errorf("alpha = %+v", a)
	}
	if _, err := parseAsdevStacks("---\nstacks: {}\n---\n"); err == nil {
		t.Error("no stacks should be an error")
	}
}

func TestAsdevConfigPath(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	t.Setenv("SOME_TOOL_CONFIG", "")
	if got := asdevConfigPath("SOME_TOOL_CONFIG"); got != filepath.Join("/home/me", ".claude", "asdev.local.md") {
		t.Errorf("default path = %q", got)
	}
	t.Setenv("SOME_TOOL_CONFIG", "/tmp/x.md")
	if got := asdevConfigPath("SOME_TOOL_CONFIG"); got != "/tmp/x.md" {
		t.Errorf("env path = %q", got)
	}
}

func TestNetrcFind(t *testing.T) {
	oneLine := strings.Fields("machine a.atlassian.net login me@a password tok-a machine b.atlassian.net login me@b password tok-b")
	c, ok := netrcFind(oneLine, "b.atlassian.net")
	if !ok || c.user != "me@b" || c.secret != "tok-b" {
		t.Errorf("one-line lookup = %+v %v", c, ok)
	}
	multi := strings.Fields("machine a.atlassian.net\n  login me@a\n  password tok-a\ndefault\n  login x\n  password y")
	c, ok = netrcFind(multi, "a.atlassian.net")
	if !ok || c.user != "me@a" || c.secret != "tok-a" {
		t.Errorf("multi-line lookup = %+v %v", c, ok)
	}
	if _, ok := netrcFind(multi, "missing.example"); ok {
		t.Errorf("missing host should not resolve")
	}
}

func TestResolveCredential(t *testing.T) {
	t.Setenv("NETRC", filepath.Join(t.TempDir(), "none"))
	t.Setenv("TOK", "secret")
	cases := []struct {
		s    stack
		want credential
	}{
		{stack{Name: "c", BaseURL: "https://c.atlassian.net", Type: "cloud", Email: "me@c", TokenEnv: "TOK"}, credential{user: "me@c", secret: "secret"}},
		{stack{Name: "s", BaseURL: "https://jira.s", Type: "server", TokenEnv: "TOK"}, credential{secret: "secret"}},
		{stack{Name: "u", BaseURL: "https://jira.u", Type: "server", Username: "me", TokenEnv: "TOK"}, credential{user: "me", secret: "secret"}},
	}
	for _, c := range cases {
		got, err := resolveCredential(c.s)
		if err != nil || got != c.want {
			t.Errorf("%s: %+v %v, want %+v", c.s.Name, got, err, c.want)
		}
	}
	if _, err := resolveCredential(stack{Name: "x", BaseURL: "https://x.atlassian.net", Type: "cloud", TokenEnv: "TOK"}); err == nil {
		t.Error("cloud without email should fail")
	}
}
