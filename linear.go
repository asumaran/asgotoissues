package main

// Linear provider. Linear is read through Claude Code's Linear MCP server,
// not through its API: the user signs in to that MCP once in a Claude Code
// profile (`claude_config_dir`, e.g. ~/.claude-syft, what the `csyft` shell
// function selects) and keeps no API key. A fetch runs `claude -p` under that
// profile with a fixed prompt that names the MCP calls and their arguments,
// and reads the MCP's own answers out of the stream (`--output-format
// stream-json`: every tool_result is in it, verbatim). The model is only the
// hand that makes the calls: its prose is never read, so nothing it
// summarises or drops reaches the list. Each run costs the profile's account
// a few cents and some seconds, which is why the source has its own refresh
// interval (`refresh_every`, 2h by default; cache.go, `due`).
//
// Flags that matter: `--tools ToolSearch` keeps the MCP tools (they are
// deferred, the model loads them through ToolSearch) and drops every
// built-in tool; `--allowedTools` limits it to the two reads;
// `--setting-sources ""` is NOT passed, because it drops the profile's
// user-scoped MCP servers with it. The run's cwd is the temp dir, so no
// project CLAUDE.md, hook or .mcp.json of wherever the popup was opened
// joins it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	linearModel      = "claude-haiku-4-5-20251001"
	linearTimeout    = 3 * time.Minute
	linearMaxTurns   = "16"
	linearGhostChunk = 10 // parents asked for per run
)

// linearLiveStates are the state types a listed ticket can be in: done and
// canceled ones never enter the model's context.
var linearLiveStates = []string{"started", "unstarted", "backlog", "triage"}

var linearFields = []string{"id", "uuid", "title", "description", "status", "statusType", "parentId", "url", "createdAt", "updatedAt", "priority", "labels", "team", "project"}

const linearSystemPrompt = `You are a fetcher. Call the Linear MCP tools exactly as the user asks, with exactly the arguments given, one call after another. When a list_issues answer has hasNextPage true, call it again with the same arguments plus its cursor, until it is false. When every call is done answer with the single word DONE. Never answer with the data, never summarise it.`

// linearRun runs Claude Code headless under the profile and returns the
// stream it printed; the tests replace it. ASGOTOISSUES_LINEAR replaces the
// command (the prompt is appended), as ASGOTOISSUES_SUMMARIZER does for the
// summarizer. On a failure the stream printed so far comes back with the
// error: the result envelope and a tool's error are in it.
var linearRun = runLinearCLI

func runLinearCLI(ctx context.Context, configDir, prompt string) ([]byte, error) {
	argv := strings.Fields(os.Getenv("ASGOTOISSUES_LINEAR"))
	if len(argv) == 0 {
		argv = []string{"claude", "-p", "--model", linearModel, "--output-format", "stream-json", "--verbose",
			"--no-session-persistence", "--tools", "ToolSearch",
			"--allowedTools", "ToolSearch,mcp__linear__list_issues,mcp__linear__get_issue",
			"--max-turns", linearMaxTurns, "--system-prompt", linearSystemPrompt}
	}
	argv = append(argv, prompt)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = os.TempDir()
	if configDir != "" {
		cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+configDir)
	}
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("claude not found (install Claude Code)")
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("%s: %s", argv[0], firstLine(string(ee.Stderr)))
		}
		return out, fmt.Errorf("%s: %w", argv[0], err)
	}
	return out, nil
}

// linearNode is an issue as the MCP answers it. priority, labels, team and
// project are kept loose: the MCP gives a label or a number, a name or an
// object, and the row only needs words.
type linearNode struct {
	ID          string `json:"id"` // the identifier: SYF2-6136
	UUID        string `json:"uuid"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	StatusType  string `json:"statusType"`
	ParentID    string `json:"parentId"`
	URL         string `json:"url"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	Priority    any    `json:"priority"`
	Labels      any    `json:"labels"`
	Team        any    `json:"team"`
	Project     any    `json:"project"`
	ghost       bool
}

type linearProvider struct{ s stack }

func (linearProvider) kind() string { return kindLinear }

// fetch lists the user's live tickets, then the parents they name that are
// not among them, as ghosts, level by level like the Jira provider.
func (p linearProvider) fetch(ctx context.Context) (fetched, error) {
	ctx, cancel := context.WithTimeout(ctx, linearTimeout*(1+ghostLevels))
	defer cancel()

	nodes, err := p.run(ctx, linearListPrompt())
	if err != nil {
		return fetched{}, fmt.Errorf("%s: %w", p.s.Name, err)
	}
	have := map[string]bool{}
	mark := func(ns []linearNode) {
		for _, n := range ns {
			have[n.ID] = true
			if n.UUID != "" {
				have[n.UUID] = true
			}
		}
	}
	mark(nodes)
	want := linearMissingParents(nodes, have)
	for level := 0; level < ghostLevels && len(want) > 0; level++ {
		var got []linearNode
		for len(want) > 0 {
			chunk := want
			if len(chunk) > linearGhostChunk {
				chunk = chunk[:linearGhostChunk]
			}
			want = want[len(chunk):]
			ns, err := p.run(ctx, linearGhostPrompt(chunk))
			if err != nil {
				return fetched{}, fmt.Errorf("%s: %w", p.s.Name, err)
			}
			got = append(got, ns...)
		}
		for i := range got {
			got[i].ghost = true
		}
		mark(got)
		nodes = append(nodes, got...)
		want = linearMissingParents(got, have)
	}
	return fetched{issues: linearIssues(p.s.Name, nodes)}, nil
}

// run makes one headless run and reads the MCP's answers out of it.
func (p linearProvider) run(ctx context.Context, prompt string) ([]linearNode, error) {
	out, runErr := linearRun(ctx, p.s.LinearConfigDir, prompt)
	nodes, err := parseLinearStream(out)
	if err != nil {
		if runErr != nil && errors.Is(err, errLinearNoCall) {
			return nil, runErr
		}
		return nil, err
	}
	return nodes, nil
}

func linearFieldsJSON() string {
	b, _ := json.Marshal(linearFields)
	return string(b)
}

// linearListPrompt asks for one list_issues call per live state type.
func linearListPrompt() string {
	return fmt.Sprintf("Call mcp__linear__list_issues once per state type, in this order: %s. For each one the arguments are {\"assignee\":\"me\",\"state\":\"<the state type>\",\"limit\":250,\"orderBy\":\"updatedAt\",\"fields\":%s}. Page on with the cursor while hasNextPage is true. Then answer DONE.",
		strings.Join(linearLiveStates, ", "), linearFieldsJSON())
}

// linearGhostPrompt asks for one get_issue call per parent.
func linearGhostPrompt(ids []string) string {
	return fmt.Sprintf("Call mcp__linear__get_issue once per id, in this order: %s. For each one the arguments are {\"id\":\"<the id>\",\"fields\":%s}. Then answer DONE.",
		strings.Join(ids, ", "), linearFieldsJSON())
}

// linearMissingParents lists, once each, the parent ids of nodes that are
// not in have (by identifier or uuid: the MCP's parentId is one of them).
func linearMissingParents(nodes []linearNode, have map[string]bool) []string {
	var ids []string
	seen := map[string]bool{}
	for _, n := range nodes {
		if n.ParentID == "" || have[n.ParentID] || seen[n.ParentID] {
			continue
		}
		seen[n.ParentID] = true
		ids = append(ids, n.ParentID)
	}
	return ids
}

var errLinearNoCall = errors.New("claude made no Linear MCP call")

// streamLine is the part of a stream-json line the parser reads: a user
// message carries the tool results, the result envelope says how the run
// ended.
type streamLine struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
	Message struct {
		Content []struct {
			Type    string          `json:"type"`
			IsError bool            `json:"is_error"`
			Content json.RawMessage `json:"content"`
		} `json:"content"`
	} `json:"message"`
}

// parseLinearStream reads the MCP's answers out of a headless run: every
// tool_result that is a list ({"issues": [...]}) or one issue. A tool_result
// flagged as an error is the run's error (a sign-in that expired reads like
// that); so is a result envelope that is not a success. The model's own
// text is never read. A run with no tool result at all is errLinearNoCall.
func parseLinearStream(out []byte) ([]linearNode, error) {
	var nodes []linearNode
	called := false
	var envelope error
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var l streamLine
		if json.Unmarshal([]byte(line), &l) != nil {
			continue
		}
		switch l.Type {
		case "result":
			if l.IsError || (l.Subtype != "" && l.Subtype != "success") {
				msg := firstLine(l.Result)
				if msg == "" {
					msg = l.Subtype
				}
				envelope = fmt.Errorf("claude: %s", msg)
			}
		case "user":
			for _, b := range l.Message.Content {
				if b.Type != "tool_result" {
					continue
				}
				text := toolResultText(b.Content)
				if b.IsError {
					return nil, fmt.Errorf("linear MCP: %s", firstLine(text))
				}
				got, ok := linearNodesOf(text)
				if !ok {
					continue // ToolSearch's answer, or a tool's prose
				}
				called = true
				nodes = append(nodes, got...)
			}
		}
	}
	if !called {
		if envelope != nil {
			return nil, envelope
		}
		return nil, errLinearNoCall
	}
	return nodes, nil
}

// toolResultText is a tool_result's text: a string, or the text blocks
// joined.
func toolResultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// linearNodesOf reads a tool's answer as a list of issues or as one issue;
// anything else (ToolSearch's tool references, an error text) is not one.
func linearNodesOf(text string) ([]linearNode, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "{") {
		return nil, false
	}
	var probe map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &probe) != nil {
		return nil, false
	}
	if raw, ok := probe["issues"]; ok {
		var list []linearNode
		if json.Unmarshal(raw, &list) != nil {
			return nil, false
		}
		return list, true
	}
	if _, ok := probe["id"]; ok {
		var one linearNode
		if json.Unmarshal([]byte(text), &one) != nil || one.ID == "" {
			return nil, false
		}
		return []linearNode{one}, true
	}
	return nil, false
}

// linearIssues maps the nodes to issues, the parents resolved among them by
// identifier or uuid (a parent not fetched keeps its raw id as the key, with
// no URL: the tree roots the child).
func linearIssues(stackName string, nodes []linearNode) []issue {
	byID := map[string]linearNode{}
	for _, n := range nodes {
		byID[n.ID] = n
		if n.UUID != "" {
			byID[n.UUID] = n
		}
	}
	seen := map[string]bool{}
	var out []issue
	for _, n := range nodes {
		if n.ID == "" || seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		created, _ := time.Parse(time.RFC3339Nano, n.CreatedAt)
		updated, _ := time.Parse(time.RFC3339Nano, n.UpdatedAt)
		team := nameOf(n.Team)
		it := issue{
			Key:         n.ID,
			Stack:       stackName,
			Source:      kindLinear,
			URL:         n.URL,
			Summary:     n.Title,
			Description: n.Description,
			BodyFormat:  bodyMarkdown,
			Status:      n.Status,
			StatusCat:   n.StatusType,
			State:       linearState(n.Status, n.StatusType),
			Priority:    linearPriority(n.Priority),
			Project:     team,
			Ghost:       n.ghost,
			Created:     created,
			Updated:     updated,
		}
		for _, part := range []string{team, nameOf(n.Project), nameOf(n.Labels), it.Priority} {
			if part != "" {
				it.Meta = append(it.Meta, part)
			}
		}
		if n.ParentID != "" {
			it.ParentKey = n.ParentID
			if parent, ok := byID[n.ParentID]; ok {
				it.ParentKey = parent.ID
				it.ParentURL = parent.URL
				it.ParentSummary = parent.Title
			}
		}
		out = append(out, it)
	}
	return out
}

// linearState folds a Linear state into the picker's three: the state type
// says started or not, and the usual "blocked" naming wins.
func linearState(status, statusType string) string {
	if strings.Contains(strings.ToLower(status), "block") {
		return stateBlocked
	}
	if statusType == "started" {
		return stateDoing
	}
	return stateTodo
}

// linearPriority is the priority as a word: the MCP's label, or its number
// read as Linear defines it (1 urgent to 4 low, 0 none).
func linearPriority(v any) string {
	switch p := v.(type) {
	case string:
		if strings.EqualFold(p, "none") || strings.EqualFold(p, "no priority") {
			return ""
		}
		return p
	case float64:
		return [...]string{"", "Urgent", "High", "Medium", "Low"}[int(p)%5]
	case map[string]any:
		return nameOf(p)
	}
	return ""
}

// nameOf reads a name out of whatever shape the MCP gives a field: a string
// as is, an object by its key, name or title, a list as its items' names
// joined.
func nameOf(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]any:
		for _, k := range []string{"key", "name", "title", "identifier", "id"} {
			if s, ok := x[k].(string); ok && s != "" {
				return s
			}
		}
	case []any:
		var parts []string
		for _, item := range x {
			if s := nameOf(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	}
	return ""
}
