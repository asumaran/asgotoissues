package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// summaryEntries is an epic, a story under it with a PR, and a ticket of
// another stack.
func summaryEntries() []*entry {
	entries := buildEntries([]issue{
		{Key: "E-1", Stack: "s", URL: "e1", Summary: "epic"},
		{Key: "E-2", Stack: "s", URL: "e2", Summary: "story", ParentURL: "e1", Description: "body"},
		{Key: "O-1", Stack: "o", URL: "o1", Summary: "other"},
	})
	linkPulls(entries, []pull{{URL: "p1", Repo: "r/r", Key: "r#1", Stack: "s", Title: "pr", State: prOpen, Refs: []string{"E-2"}}})
	return entries
}

// TestPendingSummaries: every item without a summary goes, per stack, a
// parent before its children and a PR after its ticket; a summary whose
// hash still holds is not sent again, and it travels as context for a new
// child; a parent's new summary makes its children stale.
func TestPendingSummaries(t *testing.T) {
	entries := summaryEntries()
	ids := func(batches [][]summaryItem) string {
		var out []string
		for _, b := range batches {
			var ids []string
			for _, it := range b {
				id := it.ID
				if it.Summary != "" {
					id = "(" + id + ")"
				}
				ids = append(ids, id)
			}
			out = append(out, strings.Join(ids, " "))
		}
		return strings.Join(out, " | ")
	}
	cache := summaries{}
	if got := ids(pendingSummaries(entries, cache)); got != "e1 e2 p1 | o1" {
		t.Errorf("fresh = %q", got)
	}
	cache["e1"] = summaryEntry{Hash: summaryHash("epic", "", ""), Text: "Épica"}
	cache["o1"] = summaryEntry{Hash: summaryHash("other", "", ""), Text: "Otro"}
	if got := ids(pendingSummaries(entries, cache)); got != "(e1) e2 p1" {
		t.Errorf("with the epic written = %q, want the story and its PR with the epic as context", got)
	}
	cache["e2"] = summaryEntry{Hash: summaryHash("story", "body", "Épica"), Text: "Historia"}
	cache["p1"] = summaryEntry{Hash: summaryHash("pr", "", "Historia"), Text: "PR"}
	if got := pendingSummaries(entries, cache); len(got) != 0 {
		t.Errorf("all written: %q", ids(got))
	}
	cache["e1"] = summaryEntry{Hash: cache["e1"].Hash, Text: "Épica nueva"}
	if got := ids(pendingSummaries(entries, cache)); got != "(e1) e2" {
		t.Errorf("a parent's new summary makes its children stale: %q", got)
	}
}

// TestSettleHashes: what was written in one call with its parent is stamped
// with the parent's new summary, so it is not stale at once.
func TestSettleHashes(t *testing.T) {
	entries := summaryEntries()
	written := summaries{"e1": {Text: "Épica"}, "e2": {Text: "Historia"}, "p1": {Text: "PR"}, "o1": {Text: "Otro"}}
	cache := summaries{}
	for k, v := range written {
		cache[k] = v
	}
	settleHashes(entries, cache, written)
	if got := pendingSummaries(entries, cache); len(got) != 0 {
		t.Errorf("settled summaries are not sent again: %v", got)
	}
}

// TestSummarize: the CLI's JSON envelope and a bare, fenced array both
// read; a context item is never written over; an answer that is no list, an
// error envelope and a failing command are errors.
func TestSummarize(t *testing.T) {
	orig := summarizeRun
	t.Cleanup(func() { summarizeRun = orig })
	batch := []summaryItem{{ID: "a", Title: "x", hash: "h1"}, {ID: "b", Title: "y", Summary: "ctx"}}
	var sent []byte
	for name, tc := range map[string]struct {
		out     string
		err     error
		want    string
		wantErr bool
	}{
		"envelope": {out: `{"result":"` + "```json\\n" + `[{\"id\":\"a\",\"summary\":\"Hacer x.\"},{\"id\":\"b\",\"summary\":\"no\"}]` + "\\n```" + `","is_error":false}`, want: "a=Hacer x"},
		"bare":     {out: `[{"id":"a","summary":"Hacer x"}]`, want: "a=Hacer x"},
		"not json": {out: `sorry`, wantErr: true},
		"is_error": {out: `{"result":"credit too low","is_error":true}`, wantErr: true},
		"fails":    {err: errors.New("claude not found"), wantErr: true},
	} {
		summarizeRun = func(_ context.Context, input []byte) ([]byte, error) {
			sent = input
			return []byte(tc.out), tc.err
		}
		got, err := summarize(context.Background(), batch)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v", name, err)
			continue
		}
		var parts []string
		for k, v := range got {
			parts = append(parts, k+"="+v.Text)
			if v.Hash != "h1" {
				t.Errorf("%s: the hash is the one the item was sent with: %q", name, v.Hash)
			}
		}
		if strings.Join(parts, " ") != tc.want {
			t.Errorf("%s: got %v, want %q", name, parts, tc.want)
		}
	}
	var in struct {
		Items []map[string]any `json:"items"`
	}
	if json.Unmarshal(sent, &in) != nil || len(in.Items) != 2 || in.Items[1]["summary"] != "ctx" {
		t.Errorf("the input is the batch as JSON: %s", sent)
	}
}
