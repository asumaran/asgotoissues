package main

// Filtering and ranking. Entries (one per ticket, pre-grouped by stack) carry
// three corpora matched independently per keystroke — summary, key, and
// metadata (status, type, project, stack, parent key and summary, the keys
// and branches of the linked PRs) — so typing "2099" finds PLAT-2099, "uat"
// finds tickets in UAT, "subtask" finds sub-tasks, and a PR's number or
// branch finds its ticket. Only summary matches produce highlight indexes.
// The rows themselves are built by the tree (tree.go).

import (
	"strings"
	"time"
)

// entry is one selectable ticket and the PRs linked to it (linkPulls).
type entry struct {
	it    issue
	pulls []*pull
}

// row kinds
const (
	rowHeader  = "header"  // a stack
	rowGroup   = "group"   // the PRs without a ticket, under their stack
	rowSection = "section" // a phase, in the phase view
	rowIssue   = "issue"
	rowPull    = "pull"
)

type row struct {
	kind      string
	e         *entry // issue rows
	p         *pull  // pull rows
	pe        *entry // pull rows: the ticket it hangs from (nil in the group)
	stack     string
	text      string // group and section rows
	count     int    // section rows: how many rows it holds
	depth     int    // nesting: a root ticket is 0, a PR is one past its ticket
	parent    string // what it hangs from (the parent's URL), for the row's identity
	match     bool   // a hit
	ctx       bool   // an ancestor listed to lead to a hit, not a hit itself
	score     int
	idx       []int     // matched byte offsets in the summary, for highlighting
	kids      bool      // it has something to fold
	collapsed bool      // folded: what hangs from it is not listed
	level     int       // attention: a ticket's own PRs', a PR's own
	noPR      bool      // issue rows: started, and neither PRs nor children
	last      time.Time // the last activity
	state     prFlag    // pull rows: the PR's state
	flags     []prFlag  // pull rows: what the PR needs, then the facts
	needN     int       // pull rows: how many of flags are needs
	path      []pathKey // phase rows: the tickets it hangs from, root first

	// The tree's guides (tree view): rails[j] draws a │ in the arrow column
	// of depth j; a ticket under a ticket takes a branch (├ or └, lastSib)
	// in its parent's arrow column, and kidRail runs from its own arrow down
	// to its sub-tickets.
	rails   []bool
	lastSib bool
	kidRail bool
}

// selectable reports whether the cursor may sit on the row: every row but
// nothing, the headers and sections included (they fold).
func (r row) selectable() bool { return r.kind != "" }

// opens reports whether the row is something to open.
func (r row) opens() bool { return r.kind == rowIssue || r.kind == rowPull }

// url is what the row opens.
func (r row) url() string {
	switch r.kind {
	case rowIssue:
		return r.e.it.URL
	case rowPull:
		return r.p.URL
	}
	return ""
}

// id tells a row from every other, a PR under two tickets included.
func (r row) id() string {
	if !r.opens() {
		return foldKey(r)
	}
	return r.parent + "|" + r.url()
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

// corpora returns the three parallel search texts for entries, as they are
// shown: the matcher folds case itself, scores a camelCase boundary, and its
// offsets are bytes into the very string the row highlights.
//
// title is the title a row shows (the short one when there is one): it is
// the first corpus, whose offsets the row highlights; the tracker's own
// title goes with the metadata, so both are searched.
func corpora(entries []*entry, title func(e *entry) string) (summaries, keys, metas []string) {
	for _, e := range entries {
		t := title(e)
		summaries = append(summaries, t)
		keys = append(keys, e.it.Key+" "+e.it.number())
		parts := []string{e.it.Status, e.it.Type, e.it.Project, e.it.Stack, e.it.ParentKey, e.it.ParentSummary, strings.Join(e.it.Meta, " ")}
		if t != e.it.Summary {
			parts = append(parts, e.it.Summary)
		}
		for _, p := range e.pulls {
			parts = append(parts, p.Key, p.number(), p.Head)
		}
		metas = append(metas, strings.Join(parts, " "))
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
		hits[i] = hit{score: h.Score, idx: h.Any[0], key: h.Field == 1}
	}
	return hits
}

// matchBonus biases ranking beyond the raw fuzzy score: an exact key or
// number jumps to the obvious ticket (a linked PR's too), key hits beat
// summary hits on ties, exact stack/project names bubble their group up.
func matchBonus(e *entry, h hit, q string) int {
	bonus := 0
	q = strings.ToLower(strings.TrimSpace(q)) // the bonuses compare whole words, whatever their case
	lk := strings.ToLower(e.it.Key)
	switch {
	case q == lk:
		bonus += 30
	case q == e.it.number():
		bonus += 20
	case strings.HasPrefix(lk, q) && strings.ContainsAny(q, "-#"):
		bonus += 10
	default:
		for _, p := range e.pulls {
			if q == strings.ToLower(p.Key) || q == p.number() {
				bonus += 20
				break
			}
		}
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
// tickets as a tree with their PRs (buildTree). When filtering, the hits
// are scored and the tree keeps them and the ancestors that lead to them,
// best match first.
func buildRows(entries []*entry, pulls []pull, unlinked map[string][]*pull, opts treeOpts, q string, summaries, keys, metas []string) []row {
	var hits map[int]hit
	if hasTerms(q) {
		hits = findHits(q, summaries, keys, metas)
		for i, h := range hits {
			h.score += matchBonus(entries[i], h, q)
			hits[i] = h
		}
	}
	return buildTree(entries, pulls, unlinked, opts, hits)
}

// firstIssue returns the index of the first ticket or PR, else the first row
// the cursor may sit on, or -1.
func firstIssue(rows []row) int {
	for i, r := range rows {
		if r.opens() {
			return i
		}
	}
	for i, r := range rows {
		if r.selectable() {
			return i
		}
	}
	return -1
}

// firstHit is the first row that matched the query, or the first selectable
// one when none did (a query that matches nothing lists nothing).
func firstHit(rows []row) int {
	for i, r := range rows {
		if r.match {
			return i
		}
	}
	return firstIssue(rows)
}
