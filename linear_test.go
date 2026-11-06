package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// streamToolResult is one stream-json line carrying a tool's answer.
func streamToolResult(text string, isErr bool) string {
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"content": []any{
		map[string]any{"type": "tool_result", "is_error": isErr, "content": []any{map[string]any{"type": "text", "text": text}}}}}})
	return string(b)
}

// streamText is the model talking: never read.
func streamText(text string) string {
	b, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}})
	return string(b)
}

func streamEnvelope(subtype string, isErr bool, result string) string {
	b, _ := json.Marshal(map[string]any{"type": "result", "subtype": subtype, "is_error": isErr, "result": result})
	return string(b)
}

func linearNodeJSON(id, uuid, title, status, statusType, parentID string, extra map[string]any) string {
	n := map[string]any{"id": id, "uuid": uuid, "title": title, "description": "# " + title, "status": status, "statusType": statusType,
		"url": "https://linear.app/syft/issue/" + id + "/x", "createdAt": "2026-10-01T10:00:00.000Z", "updatedAt": "2026-10-02T10:00:00.000Z"}
	if parentID != "" {
		n["parentId"] = parentID
	}
	for k, v := range extra {
		n[k] = v
	}
	b, _ := json.Marshal(n)
	return string(b)
}

func linearListJSON(hasNext bool, nodes ...string) string {
	return `{"issues":[` + strings.Join(nodes, ",") + `],"hasNextPage":` + map[bool]string{true: "true", false: "false"}[hasNext] + `}`
}

// fakeLinear answers the list prompt with pages["list"] and a ghost prompt
// with pages[<the ids it names>], and records every run.
func fakeLinear(t *testing.T, pages map[string]string) (*[]string, *[]string) {
	t.Helper()
	var prompts, dirs []string
	prev := linearRun
	linearRun = func(_ context.Context, configDir, prompt string) ([]byte, error) {
		prompts = append(prompts, prompt)
		dirs = append(dirs, configDir)
		key := "list"
		if strings.HasPrefix(prompt, "Call mcp__linear__get_issue") {
			_, after, _ := strings.Cut(prompt, "in this order: ")
			key, _, _ = strings.Cut(after, ". For each")
		}
		page, ok := pages[key]
		if !ok {
			return nil, errors.New("unexpected prompt: " + key)
		}
		return []byte(page), nil
	}
	t.Cleanup(func() { linearRun = prev })
	return &prompts, &dirs
}

func TestLinearFetchListsAndGhosts(t *testing.T) {
	team := map[string]any{"key": "SYF2", "name": "Syft"}
	list := strings.Join([]string{
		streamToolResult(`[{"type":"tool_reference","tool_name":"mcp__linear__list_issues"}]`, false), // ToolSearch's answer
		streamText("Fetching the started issues."),
		streamToolResult(linearListJSON(true,
			linearNodeJSON("SYF2-11", "uuid-11", "sub-task", "In Progress", "started", "uuid-10",
				map[string]any{"priority": 2, "labels": []any{map[string]any{"name": "bug"}, map[string]any{"name": "ui"}}, "team": team, "project": "Outreach"})), false),
		streamToolResult(linearListJSON(false,
			linearNodeJSON("SYF2-20", "uuid-20", "mine too", "Spec Review", "unstarted", "SYF2-2", map[string]any{"team": team}),
			linearNodeJSON("SYF2-11", "uuid-11", "sub-task", "In Progress", "started", "uuid-10", nil), // a repeat is dropped
			linearNodeJSON("SYF2-2", "uuid-2", "other epic", "Backlog", "backlog", "", map[string]any{"team": team, "priority": 0})), false),
		streamText("DONE"),
		streamEnvelope("success", false, "DONE"),
	}, "\n")
	ghost1 := streamToolResult(linearNodeJSON("SYF2-10", "uuid-10", "story", "Blocked", "started", "SYF2-1", map[string]any{"team": team}), false)
	ghost2 := streamToolResult(linearNodeJSON("SYF2-1", "uuid-1", "epic", "Done", "completed", "SYF2-0", nil), false)
	ghost3 := streamToolResult(linearNodeJSON("SYF2-0", "uuid-0", "initiative", "Todo", "unstarted", "SYF2--1", nil), false)
	prompts, dirs := fakeLinear(t, map[string]string{"list": list, "uuid-10": ghost1, "SYF2-1": ghost2, "SYF2-0": ghost3})

	p := linearProvider{stack{Name: "syft", LinearConfigDir: "/tmp/syft-profile"}}
	res, err := p.fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(*prompts) != 4 || (*dirs)[0] != "/tmp/syft-profile" {
		t.Errorf("runs = %d under %q, want the list and three ghost levels under the profile", len(*prompts), *dirs)
	}
	if lp := (*prompts)[0]; !strings.Contains(lp, "started, unstarted, backlog, triage") || !strings.Contains(lp, `"assignee":"me"`) || !strings.Contains(lp, `"description"`) {
		t.Errorf("list prompt = %q", lp)
	}
	var lines []string
	for _, it := range res.issues {
		lines = append(lines, strings.Join([]string{it.Key, it.State, it.ParentKey, it.ParentURL, map[bool]string{true: "ghost", false: "mine"}[it.Ghost]}, " "))
	}
	want := []string{
		"SYF2-11 doing SYF2-10 https://linear.app/syft/issue/SYF2-10/x mine",
		"SYF2-20 todo SYF2-2 https://linear.app/syft/issue/SYF2-2/x mine",
		"SYF2-2 todo   mine",
		"SYF2-10 blocked SYF2-1 https://linear.app/syft/issue/SYF2-1/x ghost",
		"SYF2-1 todo SYF2-0 https://linear.app/syft/issue/SYF2-0/x ghost",
		"SYF2-0 todo SYF2--1  ghost",
	}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("issues:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	first := res.issues[0]
	if first.Source != kindLinear || first.BodyFormat != bodyMarkdown || first.Description != "# sub-task" || first.Status != "In Progress" || first.StatusCat != "started" ||
		first.Project != "SYF2" || first.Priority != "High" || strings.Join(first.Meta, "|") != "SYF2|Outreach|bug, ui|High" ||
		first.ParentSummary != "story" || first.URL != "https://linear.app/syft/issue/SYF2-11/x" || first.ref() != "SYF2-11" ||
		!first.Created.Equal(time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("first = %+v", first)
	}
	if third := res.issues[2]; len(third.Meta) != 1 || third.Meta[0] != "SYF2" {
		t.Errorf("a priority of none and no labels leave the meta to the team: %q", third.Meta)
	}
}

func TestLinearStreamErrors(t *testing.T) {
	cases := map[string]struct {
		stream string
		want   string
	}{
		"a tool error": {
			streamToolResult("Needs authentication: run /mcp\nmore", true) + "\n" + streamEnvelope("success", false, "DONE"),
			"linear MCP: Needs authentication: run /mcp"},
		"an envelope error without a call": {
			streamText("I cannot") + "\n" + streamEnvelope("error_max_turns", true, ""),
			"claude: error_max_turns"},
		"no call at all": {
			streamText("DONE") + "\n" + streamEnvelope("success", false, "DONE"),
			errLinearNoCall.Error()},
		"nothing printed": {"", errLinearNoCall.Error()},
	}
	for name, c := range cases {
		_, err := parseLinearStream([]byte(c.stream))
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
	// A list that came back empty is a call all the same.
	if nodes, err := parseLinearStream([]byte(streamToolResult(linearListJSON(false), false))); err != nil || len(nodes) != 0 {
		t.Errorf("empty list: nodes = %v, err = %v", nodes, err)
	}
	// The run's own failure is the error when the stream says nothing better.
	prev := linearRun
	linearRun = func(context.Context, string, string) ([]byte, error) { return nil, errors.New("claude: exit status 1") }
	t.Cleanup(func() { linearRun = prev })
	if _, err := (linearProvider{stack{Name: "syft"}}).fetch(context.Background()); err == nil || err.Error() != "syft: claude: exit status 1" {
		t.Errorf("run failure: %v", err)
	}
	// But a tool error in the stream beats it.
	linearRun = func(context.Context, string, string) ([]byte, error) {
		return []byte(streamToolResult("Needs authentication", true)), errors.New("claude: exit status 1")
	}
	if _, err := (linearProvider{stack{Name: "syft"}}).fetch(context.Background()); err == nil || err.Error() != "syft: linear MCP: Needs authentication" {
		t.Errorf("tool error with a run failure: %v", err)
	}
}

// TestLinearRunCommand: the replacement command gets the prompt as its last
// argument and the profile in CLAUDE_CONFIG_DIR; a command that is not
// there reads as such.
func TestLinearRunCommand(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	script := "#!/bin/sh\necho \"dir=$CLAUDE_CONFIG_DIR last=${@: -1}\" > " + log + "\n" +
		"printf '%s\\n' '" + strings.ReplaceAll(streamToolResult(linearListJSON(false), false), "'", "'\\''") + "'\n"
	stub := filepath.Join(dir, "claude-stub")
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASGOTOISSUES_LINEAR", "/bin/bash "+stub+" --flag")
	out, err := runLinearCLI(context.Background(), "/tmp/profile", "the prompt")
	if err != nil {
		t.Fatal(err)
	}
	if nodes, err := parseLinearStream(out); err != nil || len(nodes) != 0 {
		t.Errorf("stream: %v %v", nodes, err)
	}
	if got, _ := os.ReadFile(log); strings.TrimSpace(string(got)) != "dir=/tmp/profile last=the prompt" {
		t.Errorf("the stub saw %q", got)
	}
	t.Setenv("ASGOTOISSUES_LINEAR", filepath.Join(dir, "missing"))
	if _, err := runLinearCLI(context.Background(), "", "p"); err == nil || err.Error() != "claude not found (install Claude Code)" {
		t.Errorf("missing command: %v", err)
	}
}

func TestLinearPrompts(t *testing.T) {
	if p := linearGhostPrompt([]string{"SYF2-1", "uuid-2"}); !strings.Contains(p, "in this order: SYF2-1, uuid-2. For each") || !strings.Contains(p, `"fields":["id","uuid"`) {
		t.Errorf("ghost prompt = %q", p)
	}
	if got := linearMissingParents([]linearNode{{ID: "a", ParentID: "p1"}, {ID: "b", ParentID: "p1"}, {ID: "c", ParentID: "have"}, {ID: "d"}}, map[string]bool{"have": true}); strings.Join(got, ",") != "p1" {
		t.Errorf("missing parents = %v", got)
	}
	if nameOf([]any{map[string]any{"name": "x"}, "y"}) != "x, y" || linearPriority(4.0) != "Low" || linearPriority("Urgent") != "Urgent" || linearPriority("None") != "" {
		t.Errorf("field readers")
	}
}
