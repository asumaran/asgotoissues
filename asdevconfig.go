package main

// The stacks of the asdev config (~/.claude/asdev.local.md): a markdown file
// whose YAML front matter lists stacks, each with a `jira` block (base_url,
// type, email, api_token_env, username, default_project), a `github` block
// (org, orgs) and an `issues:` list. Everything else in the file is ignored.
// What a tool does with a stack (list its issues, look a ticket up) is the
// tool's own; this file only reads them, in file order, and finds the
// credential for a stack's Jira.
//
// This file is the same in every tool of the family that reads the asdev
// config.

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// stack is one stack of the config.
type stack struct {
	Name     string   // key under `stacks:`
	Issues   []string // the `issues:` list as written; nil when absent
	Trackers []string // what the tool lists for the stack, filled by the tool
	Orgs     []string // GitHub owners (orgs or users), `org` and `orgs` merged

	// Jira
	BaseURL        string // https://org.atlassian.net, no trailing slash
	Type           string // "cloud" (default) | "server"
	Email          string // basic-auth user for cloud (server uses a bearer token)
	TokenEnv       string // env var holding the API token / PAT
	Username       string // server basic-auth fallback user
	DefaultProject string // the project key this stack's tickets usually carry
	Order          int    // position in the config file, for stable grouping
}

// host returns the bare hostname of the stack's Jira site.
func (s stack) host() string {
	if u, err := url.Parse(s.BaseURL); err == nil && u.Host != "" {
		return u.Host
	}
	return strings.TrimPrefix(strings.TrimPrefix(s.BaseURL, "https://"), "http://")
}

// asdevConfigPath is the config file: the one env names when it is set, else
// ~/.claude/asdev.local.md.
func asdevConfigPath(env string) string {
	if p := os.Getenv(env); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "asdev.local.md")
}

// frontMatter returns the YAML between the leading `---` fences of a
// markdown file, or the whole input when it has no fences (plain YAML).
func frontMatter(doc string) string {
	doc = strings.TrimPrefix(doc, "\ufeff")
	lines := strings.Split(doc, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return doc
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[1:i], "\n")
		}
	}
	return strings.Join(lines[1:], "\n")
}

type rawJira struct {
	BaseURL        string `yaml:"base_url"`
	Type           string `yaml:"type"`
	Email          string `yaml:"email"`
	TokenEnv       string `yaml:"api_token_env"`
	Username       string `yaml:"username"`
	DefaultProject string `yaml:"default_project"`
}

type rawGitHub struct {
	Org  string   `yaml:"org"`
	Orgs []string `yaml:"orgs"`
}

type rawStack struct {
	Issues []string   `yaml:"issues"`
	Jira   *rawJira   `yaml:"jira"`
	GitHub *rawGitHub `yaml:"github"`
}

// owners merges `org` and `orgs`, first mention wins.
func (g *rawGitHub) owners() []string {
	if g == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, o := range append([]string{g.Org}, g.Orgs...) {
		o = strings.TrimSpace(o)
		if o == "" || seen[strings.ToLower(o)] {
			continue
		}
		seen[strings.ToLower(o)] = true
		out = append(out, o)
	}
	return out
}

// parseAsdevStacks decodes every stack of the config document, in file order
// (yaml.v3 map decoding loses it, so the key order is recovered from a
// yaml.Node walk).
func parseAsdevStacks(doc string) ([]stack, error) {
	var root struct {
		Stacks map[string]rawStack `yaml:"stacks"`
	}
	src := frontMatter(doc)
	if err := yaml.Unmarshal([]byte(src), &root); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if len(root.Stacks) == 0 {
		return nil, errors.New("config: no stacks defined")
	}
	order := stackKeyOrder(src)
	out := make([]stack, 0, len(root.Stacks))
	for name, rs := range root.Stacks {
		st := stack{Name: name, Issues: rs.Issues, Orgs: rs.GitHub.owners(), Order: order[name]}
		if rs.Jira != nil {
			st.BaseURL = strings.TrimRight(strings.TrimSpace(rs.Jira.BaseURL), "/")
			st.Type = strings.ToLower(strings.TrimSpace(rs.Jira.Type))
			if st.Type == "" {
				st.Type = "cloud"
			}
			st.Email = strings.TrimSpace(rs.Jira.Email)
			st.TokenEnv = strings.TrimSpace(rs.Jira.TokenEnv)
			st.Username = strings.TrimSpace(rs.Jira.Username)
			st.DefaultProject = strings.TrimSpace(rs.Jira.DefaultProject)
		}
		out = append(out, st)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out, nil
}

// stackKeyOrder maps each stack name to its position under `stacks:`.
func stackKeyOrder(src string) map[string]int {
	order := map[string]int{}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil || len(doc.Content) == 0 {
		return order
	}
	top := doc.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != "stacks" {
			continue
		}
		stacks := top.Content[i+1]
		for j, n := 0, 0; j+1 < len(stacks.Content); j, n = j+2, n+1 {
			order[stacks.Content[j].Value] = n
		}
	}
	return order
}

// readAsdevStacks reads and decodes the config file at path.
func readAsdevStacks(path string) ([]stack, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return parseAsdevStacks(string(data))
}

// ---- credentials ----

// credential is what authenticates a request against one stack.
type credential struct {
	user   string // empty → bearer token
	secret string
}

// netrcLookup finds the login/password for host in ~/.netrc (or $NETRC).
// The tokenizer accepts both the one-line and the multi-line layouts.
func netrcLookup(host string) (credential, bool) {
	path := os.Getenv("NETRC")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return credential{}, false
		}
		path = filepath.Join(home, ".netrc")
	}
	f, err := os.Open(path)
	if err != nil {
		return credential{}, false
	}
	defer f.Close()
	var toks []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		toks = append(toks, strings.Fields(line)...)
	}
	return netrcFind(toks, host)
}

func netrcFind(toks []string, host string) (credential, bool) {
	var cur *credential
	inHost := false
	for i := 0; i < len(toks); i++ {
		switch toks[i] {
		case "machine":
			if inHost && cur != nil {
				return *cur, cur.secret != ""
			}
			inHost = i+1 < len(toks) && toks[i+1] == host
			if inHost {
				cur = &credential{}
			}
			i++
		case "default":
			if inHost && cur != nil {
				return *cur, cur.secret != ""
			}
			inHost = false
		case "login":
			if inHost && cur != nil && i+1 < len(toks) {
				cur.user = toks[i+1]
			}
			i++
		case "password":
			if inHost && cur != nil && i+1 < len(toks) {
				cur.secret = toks[i+1]
			}
			i++
		case "account", "macdef":
			i++
		}
	}
	if inHost && cur != nil {
		return *cur, cur.secret != ""
	}
	return credential{}, false
}

// resolveCredential picks the credential for a stack: ~/.netrc first (works
// no matter how the plugin process was spawned), then the env var named in
// the config. Cloud always authenticates with basic auth (email + token);
// server uses a bearer PAT unless a username is configured.
func resolveCredential(s stack) (credential, error) {
	if c, ok := netrcLookup(s.host()); ok {
		return c, nil
	}
	if s.TokenEnv != "" {
		if tok := os.Getenv(s.TokenEnv); tok != "" {
			switch {
			case s.Type == "server" && s.Username == "":
				return credential{secret: tok}, nil
			case s.Type == "server":
				return credential{user: s.Username, secret: tok}, nil
			case s.Email != "":
				return credential{user: s.Email, secret: tok}, nil
			}
			return credential{}, fmt.Errorf("%s: jira.email missing for basic auth", s.Name)
		}
	}
	return credential{}, fmt.Errorf("%s: no credentials (add %s to ~/.netrc or export %s)", s.Name, s.host(), orDefault(s.TokenEnv, "the API token env var"))
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
