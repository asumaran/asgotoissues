package main

// Filtering and ranking. Entries (one per ticket, pre-grouped by stack) carry
// three lowercased corpora matched independently per keystroke — summary,
// key, and metadata (status, type, project, stack, parent key) — so typing
// "2099" finds PLAT-2099, "uat" finds tickets in UAT, "subtask" finds
// sub-tasks. Only summary matches produce highlight indexes.

import (
	"sort"
	"strings"
)

// entry is one selectable ticket.
type entry struct {
	it issue
}

type row struct {
	kind  string // "header" | "issue"
	e     *entry // nil for headers
	stack string
	match bool
	score int
	idx   []int // matched rune positions in the summary, for highlighting
}

// buildEntries wraps issues in display order (mergeStacks already grouped
// them by stack, newest first within each).
func buildEntries(issues []issue) []*entry {
	out := make([]*entry, 0, len(issues))
	for _, it := range issues {
		out = append(out, &entry{it: it})
	}
	return out
}

// corpora returns the three parallel lowercased search texts for entries.
func corpora(entries []*entry) (summaries, keys, metas []string) {
	for _, e := range entries {
		summaries = append(summaries, strings.ToLower(e.it.Summary))
		keys = append(keys, strings.ToLower(e.it.Key)+" "+e.it.number())
		meta := strings.ToLower(strings.Join([]string{e.it.Status, e.it.Type, e.it.Project, e.it.Stack, e.it.ParentKey, strings.Join(e.it.Meta, " ")}, " "))
		metas = append(metas, meta)
	}
	return summaries, keys, metas
}

type hit struct {
	score int
	idx   []int // only from summary matches
	key   bool  // best score came from the key corpus
}

// findHits matches the query's terms over all corpora (see findFields).
func findHits(q string, summaries, keys, metas []string) map[int]hit {
	hits := map[int]hit{}
	for i, h := range findFields(q, summaries, keys, metas) {
		hits[i] = hit{score: h.Score, idx: h.Idx[0], key: h.Field == 1}
	}
	return hits
}

// matchBonus biases ranking beyond the raw fuzzy score: an exact key or
// number jumps to the obvious ticket, key hits beat summary hits on ties,
// exact stack/project names bubble their group up.
func matchBonus(e *entry, h hit, q string) int {
	bonus := 0
	lk := strings.ToLower(e.it.Key)
	switch {
	case q == lk:
		bonus += 30
	case q == e.it.number():
		bonus += 20
	case strings.HasPrefix(lk, q) && strings.ContainsAny(q, "-#"):
		bonus += 10
	}
	if q == strings.ToLower(e.it.Stack) || q == strings.ToLower(e.it.Project) {
		bonus += 10
	}
	if h.key {
		bonus += 2
	}
	return bonus
}

// buildRows turns entries into display rows: a header per stack, then its
// tickets. When filtering, only matching tickets (and their headers) survive
// and they are ranked: best match first, the stack that holds it on top.
func buildRows(entries []*entry, q string, summaries, keys, metas []string) []row {
	filtering := q != ""
	var hits map[int]hit
	if filtering {
		hits = findHits(q, summaries, keys, metas)
	}
	var issues []row
	for i, e := range entries {
		h, ok := hits[i]
		if filtering && !ok {
			continue
		}
		score := 0
		if filtering {
			score = h.score + matchBonus(e, h, q)
		}
		issues = append(issues, row{kind: "issue", e: e, stack: e.it.Stack, match: ok, score: score, idx: h.idx})
	}
	if filtering {
		// equal scores: the most recently updated ticket first
		sort.SliceStable(issues, func(i, j int) bool { return issues[i].e.it.Updated.After(issues[j].e.it.Updated) })
		issues = rank(issues, func(r row) int { return r.score }, func(r row) string { return r.stack })
	}
	var rows []row
	last := ""
	for _, r := range issues {
		if r.stack != last {
			rows = append(rows, row{kind: "header", stack: r.stack})
			last = r.stack
		}
		rows = append(rows, r)
	}
	return rows
}

// firstIssue returns the index of the first selectable row, or -1.
func firstIssue(rows []row) int {
	for i, r := range rows {
		if r.kind == "issue" {
			return i
		}
	}
	return -1
}
