package main

// Short Spanish titles, written by a model: one plain sentence of 6 to 14
// words per ticket and per PR, shown in the list instead of the tracker's
// title (the preview keeps the full one). A child reads as a part of its
// parent's goal: the request goes per stack as the tree itself, each item
// with its parent's id, and the items already summarized travel as
// read-only context, so a new child is written against its parent's current
// summary. The summaries are cached in summaries.json by URL, with a hash of
// what they were written from (the title, the start of the body and the
// parent's summary): only new or edited items are sent.
//
// It runs the Claude Code CLI (ASGOTOISSUES_SUMMARIZER replaces the command)
// in the background; until a summary arrives, or when the CLI is missing or
// fails, the row shows its own title.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// summaryBatch is how many items one call writes.
const summaryBatch = 50

type summaryEntry struct {
	Hash string `json:"hash"`
	Text string `json:"text"`
}

// summaries is the cache: URL → the summary and what it was written from.
type summaries map[string]summaryEntry

func summariesFile() string { return filepath.Join(stateDir(), "summaries.json") }

func loadSummaries() summaries {
	var s summaries
	readJSONFile(summariesFile(), &s)
	if s == nil {
		s = summaries{}
	}
	return s
}

func saveSummaries(s summaries) { writeJSONFile(summariesFile(), s) }

// summaryItem is a ticket or a PR as the model gets it.
type summaryItem struct {
	ID      string `json:"id"`               // the URL
	Parent  string `json:"parent,omitempty"` // the parent's id: the ticket a sub-task or a PR hangs from
	Kind    string `json:"kind"`             // ticket | pr
	Title   string `json:"title"`
	Body    string `json:"body,omitempty"`
	Summary string `json:"summary,omitempty"` // already written: context, do not rewrite
	hash    string
}

// summaryHash is what a summary depends on: the title, the start of the
// body, and the parent's summary (a child is rewritten when its parent's
// changes).
func summaryHash(title, body, parentSummary string) string {
	if len(body) > 2048 {
		body = body[:2048]
	}
	h := sha256.Sum256([]byte(title + "\x00" + body + "\x00" + parentSummary))
	return hex.EncodeToString(h[:8])
}

// pendingSummaries is, per stack, the items whose summary is missing or
// stale, with the summarized ancestors they need as context. A parent comes
// before its children, so one call can write both.
func pendingSummaries(entries []*entry, cache summaries) [][]summaryItem {
	byURL := map[string]*entry{}
	for _, e := range entries {
		byURL[e.it.URL] = e
	}
	// written is the summary as it will be once this round is done: the
	// cached one when it is still good.
	var stacks []string
	perStack := map[string][]summaryItem{}
	context := map[string]map[string]bool{}
	var visit func(e *entry)
	done := map[string]bool{}
	visit = func(e *entry) {
		if done[e.it.URL] {
			return
		}
		done[e.it.URL] = true
		parentSummary := ""
		if p := byURL[e.it.ParentURL]; p != nil && p != e {
			visit(p)
			parentSummary = cache[p.it.URL].Text
		}
		st := e.it.Stack
		if _, ok := perStack[st]; !ok {
			stacks = append(stacks, st)
			perStack[st] = nil
			context[st] = map[string]bool{}
		}
		body := e.it.Description
		h := summaryHash(e.it.Summary, body, parentSummary)
		if c, ok := cache[e.it.URL]; !ok || c.Hash != h {
			perStack[st] = append(perStack[st], summaryItem{ID: e.it.URL, Parent: parentOf(e, byURL), Kind: "ticket", Title: e.it.Summary, Body: clip(body, 600), hash: h})
		}
		for _, p := range e.pulls {
			if done[p.URL] {
				continue
			}
			done[p.URL] = true
			h := summaryHash(p.Title, p.Body, cache[e.it.URL].Text)
			if c, ok := cache[p.URL]; !ok || c.Hash != h {
				perStack[st] = append(perStack[st], summaryItem{ID: p.URL, Parent: e.it.URL, Kind: "pr", Title: p.Title, Body: clip(p.Body, 600), hash: h})
			}
		}
	}
	for _, e := range entries {
		visit(e)
	}
	var out [][]summaryItem
	for _, st := range stacks {
		items := perStack[st]
		for len(items) > 0 {
			n := min(summaryBatch, len(items))
			out = append(out, withContext(items[:n], byURL, cache))
			items = items[n:]
		}
	}
	return out
}

// withContext adds to a batch the ancestors of its items that are already
// summarized, as read-only context.
func withContext(items []summaryItem, byURL map[string]*entry, cache summaries) []summaryItem {
	in := map[string]bool{}
	for _, it := range items {
		in[it.ID] = true
	}
	var ctx []summaryItem
	for _, it := range items {
		for id := it.Parent; id != "" && !in[id]; {
			e := byURL[id]
			if e == nil {
				break
			}
			in[id] = true
			ctx = append(ctx, summaryItem{ID: id, Parent: parentOf(e, byURL), Kind: "ticket", Title: e.it.Summary, Summary: cache[id].Text})
			id = parentOf(e, byURL)
		}
	}
	return append(ctx, items...)
}

func parentOf(e *entry, byURL map[string]*entry) string {
	if p := byURL[e.it.ParentURL]; p != nil && p != e {
		return p.it.URL
	}
	return ""
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	// cut on a rune boundary
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

const summaryPrompt = `You write short titles for a developer's task list.
You get a JSON object {"items": [...]}. Each item is a ticket or a pull request: id, kind, title, maybe a body, and maybe "parent" (the id of the ticket it belongs to).
Items that already have a "summary" are context only: do not rewrite them.
For every other item write one plain Spanish sentence of 6 to 14 words that says what the work is.
A child (an item with a parent) says which piece of its parent's goal it covers, and reuses the parent's key term, so the list reads top-down.
No ticket keys, no quotes, no trailing period.
Reply with only a JSON array: [{"id": "...", "summary": "..."}].`

// summarizeRun runs the summarizer on a prompt's input and returns what it
// wrote; the tests replace it.
var summarizeRun = func(ctx context.Context, input []byte) ([]byte, error) {
	argv := strings.Fields(os.Getenv("ASGOTOISSUES_SUMMARIZER"))
	if len(argv) == 0 {
		argv = []string{"claude", "-p", "--model", "claude-haiku-4-5-20251001", "--output-format", "json",
			"--no-session-persistence", "--tools", "", "--setting-sources", "", "--strict-mcp-config",
			"--system-prompt", summaryPrompt}
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(string(input))
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrNotFound) {
		return nil, errNoSummarizer
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("%s: %s", argv[0], firstLine(string(ee.Stderr)))
		}
		return nil, err
	}
	return out, nil
}

// errNoSummarizer is a summarizer that is not installed: the rows keep the
// trackers' titles and nothing says so (it is not an error to fix).
var errNoSummarizer = errors.New("the summarizer is not installed")

// summariesMsg carries what one call wrote, or why it could not.
type summariesMsg struct {
	written summaries
	err     error
}

// summarizeCmd writes the summaries of one batch.
func summarizeCmd(batch []summaryItem) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		got, err := summarize(ctx, batch)
		return summariesMsg{written: got, err: err}
	}
}

// summarize sends a batch and reads the answer: the CLI's JSON envelope
// (its "result") or, from a replacement command, the array itself, fenced
// in a code block or not.
func summarize(ctx context.Context, batch []summaryItem) (summaries, error) {
	input, err := json.Marshal(map[string]any{"items": batch})
	if err != nil {
		return nil, err
	}
	out, err := summarizeRun(ctx, input)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(string(out))
	var envelope struct {
		Result  *string `json:"result"`
		IsError bool    `json:"is_error"`
	}
	if strings.HasPrefix(text, "{") && json.Unmarshal([]byte(text), &envelope) == nil && envelope.Result != nil {
		if envelope.IsError {
			return nil, fmt.Errorf("summaries: %s", firstLine(*envelope.Result))
		}
		text = strings.TrimSpace(*envelope.Result)
	}
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(strings.TrimSpace(text), "```")
	var list []struct {
		ID      string `json:"id"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &list); err != nil {
		return nil, fmt.Errorf("summaries: the answer is not a JSON list")
	}
	want := map[string]string{}
	for _, it := range batch {
		if it.Summary == "" {
			want[it.ID] = it.hash
		}
	}
	got := summaries{}
	for _, s := range list {
		h, ok := want[s.ID]
		text := strings.TrimSuffix(strings.TrimSpace(s.Summary), ".")
		if ok && text != "" {
			got[s.ID] = summaryEntry{Hash: h, Text: text}
		}
	}
	return got, nil
}

// settleHashes stamps what was just written with the hash of what it was
// written from, now that the parents' summaries are in the cache too: a
// child written in the same call as its parent would otherwise be stale at
// once and written again on the next open.
func settleHashes(entries []*entry, cache summaries, written summaries) {
	byURL := map[string]*entry{}
	for _, e := range entries {
		byURL[e.it.URL] = e
	}
	parentText := func(url string) string {
		if e := byURL[url]; e != nil {
			if p := byURL[e.it.ParentURL]; p != nil && p != e {
				return cache[p.it.URL].Text
			}
		}
		return ""
	}
	seen := map[string]bool{} // a PR under two tickets is written against the first
	for _, e := range entries {
		if w, ok := written[e.it.URL]; ok {
			w.Hash = summaryHash(e.it.Summary, e.it.Description, parentText(e.it.URL))
			cache[e.it.URL] = w
		}
		for _, p := range e.pulls {
			if w, ok := written[p.URL]; ok && !seen[p.URL] {
				seen[p.URL] = true
				w.Hash = summaryHash(p.Title, p.Body, cache[e.it.URL].Text)
				cache[p.URL] = w
			}
		}
	}
}

// titlesMode is what the list shows as a row's title.
type titlesMode string

const (
	titlesShort    titlesMode = "short"
	titlesOriginal titlesMode = "original"
)

var titlesModes = []titlesMode{titlesShort, titlesOriginal}

func parseTitles(s string) titlesMode {
	if strings.TrimSpace(strings.ToLower(s)) == string(titlesOriginal) {
		return titlesOriginal
	}
	return titlesShort
}

// summarizerOff is set by the tests and by -dump so nothing is spawned
// unless asked.
var summarizerOff = os.Getenv("ASGOTOISSUES_NO_SUMMARIES") != ""
