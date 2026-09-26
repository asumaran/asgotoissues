package main

// The list as a tree: under each stack its tickets hang from their parents
// (a sub-task from its story, the story from its epic, whether or not the
// parent is mine: a parent that is not is a ghost the provider fetched as
// context), every ticket carries the PRs linked to it, and the PRs of mine
// that name no ticket close the stack under a group of their own. Each
// level is ordered by the chosen mode, and with a query the tree stays a
// tree: the hits and the ancestors that lead to them.

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// orderMode is how the tickets of a level are ordered.
type orderMode string

const (
	orderCreated   orderMode = "created"
	orderUpdated   orderMode = "updated"
	orderKey       orderMode = "key"
	orderAttention orderMode = "attention"
)

var orderModes = []orderMode{orderCreated, orderUpdated, orderKey, orderAttention}

// parseOrder reads a saved or given order, created when it is none of them.
func parseOrder(s string) orderMode {
	for _, o := range orderModes {
		if string(o) == strings.ToLower(strings.TrimSpace(s)) {
			return o
		}
	}
	return orderCreated
}

// prsMode is which PR rows the list shows.
type prsMode string

const (
	prsAll       prsMode = "all"
	prsOpen      prsMode = "open"
	prsAttention prsMode = "attention"
)

var prsModes = []prsMode{prsAll, prsOpen, prsAttention}

func parsePrs(s string) prsMode {
	for _, p := range prsModes {
		if string(p) == strings.ToLower(strings.TrimSpace(s)) {
			return p
		}
	}
	return prsAll
}

// ---- linking ----

// linkPulls hangs every PR from the tickets of its stack it names (pullRefs):
// the strong references, or, when none of them is a ticket of the list, the
// weak ones. A PR that names several tickets hangs from each. It returns, per
// stack, the PRs of mine that hang from nothing. The PRs of a ticket are
// ordered open first, then newest activity first.
func linkPulls(entries []*entry, pulls []pull) map[string][]*pull {
	byRef := map[string]map[string][]*entry{} // stack → ref → entries
	for _, e := range entries {
		e.pulls = nil
		if byRef[e.it.Stack] == nil {
			byRef[e.it.Stack] = map[string][]*entry{}
		}
		byRef[e.it.Stack][e.it.ref()] = append(byRef[e.it.Stack][e.it.ref()], e)
	}
	unlinked := map[string][]*pull{}
	for i := range pulls {
		p := &pulls[i]
		linked := linkTo(byRef[p.Stack], p, p.Refs)
		if !linked {
			linked = linkTo(byRef[p.Stack], p, p.WeakRefs)
		}
		if !linked && p.Mine {
			unlinked[p.Stack] = append(unlinked[p.Stack], p)
		}
	}
	for _, e := range entries {
		sort.SliceStable(e.pulls, func(i, j int) bool { return newerPull(*e.pulls[i], *e.pulls[j]) })
	}
	return unlinked
}

func linkTo(byRef map[string][]*entry, p *pull, refs []string) bool {
	linked := false
	for _, ref := range refs {
		for _, e := range byRef[ref] {
			if !hasPull(e.pulls, p.URL) {
				e.pulls = append(e.pulls, p)
			}
			linked = true
		}
	}
	return linked
}

func hasPull(pulls []*pull, url string) bool {
	for _, p := range pulls {
		if p.URL == url {
			return true
		}
	}
	return false
}

// newerPull orders the PRs of a ticket: open before merged, then the most
// recently updated first.
func newerPull(a, b pull) bool {
	if (a.State == prOpen) != (b.State == prOpen) {
		return a.State == prOpen
	}
	return a.Updated.After(b.Updated)
}

// stackedBases maps repo@head to the PR whose head that is, so a PR whose
// base is another PR's branch knows what it sits on.
func stackedBases(pulls []pull) map[string]*pull {
	out := map[string]*pull{}
	for i := range pulls {
		p := &pulls[i]
		if p.Head != "" {
			out[strings.ToLower(p.Repo)+"@"+p.Head] = p
		}
	}
	return out
}

func (p *pull) baseIn(bases map[string]*pull) *pull {
	if p == nil || p.Base == "" {
		return nil
	}
	return bases[strings.ToLower(p.Repo)+"@"+p.Base]
}

// ---- the tree ----

// node is one ticket in the tree: its entry, its children and what it needs.
type node struct {
	e        *entry
	i        int // index of the entry, what hits are keyed by
	children []*node
	level    int      // attention: the worst of its PRs and its descendants
	needs    []prFlag // what its PRs and its descendants' need, once each, worst first
	merged   bool     // it has PRs and every one of them is merged: move it
	done     bool     // nothing left under it: every PR merged (or none on a ghost) and every child done
	best     int      // best score in its subtree, under a query
	hit      bool
}

// buildTree is the list: rows in display order. hits, when the filter has
// terms, says which entries match and how well; then only the hits and their
// ancestors are listed, siblings go by their subtree's best score and the
// stacks by their best hit. Without hits every level goes by order.
// hideMerged leaves out the merged PRs and the tickets with nothing left
// under them (every PR merged, every child done): the work that is done.
// collapsed names, by URL, the tickets folded by hand: their PRs and
// children are not listed, unless a query is on (a search shows everything).
func buildTree(entries []*entry, pulls []pull, unlinked map[string][]*pull, order orderMode, prs prsMode, hideMerged bool, collapsed map[string]bool, hits map[int]hit) []row {
	filtering := hits != nil
	bases := stackedBases(pulls)

	// Nodes, then the parents: a ticket hangs from the node its ParentURL
	// names, in its own stack; anything else is a root.
	nodes := make([]*node, len(entries))
	byURL := map[string]*node{}
	for i, e := range entries {
		nodes[i] = &node{e: e, i: i}
		if e.it.URL != "" {
			byURL[e.it.URL] = nodes[i]
		}
	}
	var stacks []string
	roots := map[string][]*node{}
	for _, n := range nodes {
		st := n.e.it.Stack
		if _, seen := roots[st]; !seen {
			stacks = append(stacks, st)
			roots[st] = nil
		}
		parent := byURL[n.e.it.ParentURL]
		if parent != nil && parent != n && parent.e.it.Stack == st && !descends(parent, n) {
			parent.children = append(parent.children, n)
			continue
		}
		roots[st] = append(roots[st], n)
	}

	// What each node needs, its subtree included, and its best score.
	var settle func(n *node)
	settle = func(n *node) {
		n.level, n.needs, n.merged = ownAttention(n.e, bases)
		h, ok := hits[n.i]
		n.hit = ok
		n.best = -1
		if ok {
			n.best = h.score
		}
		n.done = n.merged || n.e.it.Ghost && len(n.e.pulls) == 0
		for _, c := range n.children {
			settle(c)
			n.level = max(n.level, c.level)
			n.needs = mergeNeeds(n.needs, c.needs)
			n.best = max(n.best, c.best)
			n.done = n.done && c.done
		}
	}
	for _, st := range stacks {
		for _, r := range roots[st] {
			settle(r)
		}
	}

	// The rows, one stack after the other.
	// A row hangs from its parent where the parent's title starts: one
	// space past the key, from wherever the parent itself is set in.
	var cur []row // the rows of the stack being walked
	var walk func(n *node, depth, col int)
	walk = func(n *node, depth, col int) {
		if filtering && n.best < 0 || hideMerged && n.done {
			return
		}
		h := hits[n.i]
		fold := !filtering && collapsed[n.e.it.URL]
		cur = append(cur, row{kind: rowIssue, e: n.e, stack: n.e.it.Stack, depth: depth, col: col, parent: n.e.it.ParentURL,
			match: n.hit, ctx: filtering && !n.hit, score: h.score, idx: h.idx, level: n.level, needs: n.needs, merged: n.merged,
			kids: len(n.e.pulls) > 0 || len(n.children) > 0, collapsed: fold})
		if fold {
			return
		}
		under := col + ansi.StringWidth(n.e.it.Key) + 1
		if !filtering || n.hit {
			for _, p := range n.e.pulls {
				if r, ok := pullRow(p, n.e.it.URL, n.e.it.Stack, depth+1, under, bases, prs, hideMerged); ok {
					cur = append(cur, r)
				}
			}
		}
		for _, c := range sortNodes(n.children, order, filtering) {
			walk(c, depth+1, under)
		}
	}
	var groups []stackRows
	for _, st := range stacks {
		cur = nil
		for _, r := range sortNodes(roots[st], order, filtering) {
			walk(r, 0, 0)
		}
		out := cur
		if len(out) == 0 {
			continue
		}
		if !filtering && len(unlinked[st]) > 0 {
			var prRows []row
			for _, p := range unlinked[st] {
				if r, ok := pullRow(p, "group:"+st, st, 1, 2, bases, prs, hideMerged); ok {
					prRows = append(prRows, r)
				}
			}
			if len(prRows) > 0 {
				out = append(out, row{kind: rowGroup, stack: st, text: "PRs without a ticket"})
				out = append(out, prRows...)
			}
		}
		best := -1
		for _, r := range roots[st] {
			best = max(best, r.best)
		}
		groups = append(groups, stackRows{st, best, out})
	}
	if filtering {
		groups = rank(groups, func(g stackRows) int { return g.best }, nil)
	}
	var rows []row
	for _, g := range groups {
		rows = append(rows, row{kind: rowHeader, stack: g.stack})
		rows = append(rows, g.rows...)
	}
	return rows
}

type stackRows struct {
	stack string
	best  int
	rows  []row
}

// descends reports whether n is under a (a cycle guard: a parent that is its
// own descendant stays a root).
func descends(n, a *node) bool {
	for _, c := range a.children {
		if c == n || descends(n, c) {
			return true
		}
	}
	return false
}

// pullRow is a PR under a ticket (or the unlinked group), or nothing when
// the prs mode, or hideMerged, hides it.
func pullRow(p *pull, parent, stack string, depth, col int, bases map[string]*pull, prs prsMode, hideMerged bool) (row, bool) {
	flags := p.flags(p.baseIn(bases))
	lvl := level(flags)
	if hideMerged && p.State == prMerged {
		return row{}, false
	}
	switch prs {
	case prsOpen:
		if p.State != prOpen {
			return row{}, false
		}
	case prsAttention:
		if lvl < levelWarn {
			return row{}, false
		}
	}
	return row{kind: rowPull, p: p, stack: stack, depth: depth, col: col, parent: parent, level: lvl, flags: flags}, true
}

// ownAttention is what a ticket's own PRs need: the worst of their levels,
// their flags that need something (once each, worst first), a warning for a
// blocked ticket without a PR, and whether every PR is merged.
func ownAttention(e *entry, bases map[string]*pull) (lvl int, needs []prFlag, merged bool) {
	merged = len(e.pulls) > 0
	for _, p := range e.pulls {
		flags := p.flags(p.baseIn(bases))
		lvl = max(lvl, level(flags))
		needs = mergeNeeds(needs, flags)
		if p.State != prMerged {
			merged = false
		}
	}
	if merged {
		lvl = max(lvl, levelOK)
	}
	if e.it.state() == stateBlocked && !e.it.Ghost {
		lvl = max(lvl, levelWarn)
	}
	return lvl, needs, merged
}

// mergeNeeds adds to needs the flags of more that say something is needed
// (a level above none), once each, and keeps the worst first.
func mergeNeeds(needs, more []prFlag) []prFlag {
	for _, f := range more {
		if f.level == levelNone {
			continue
		}
		dup := false
		for _, n := range needs {
			if n.text == f.text {
				dup = true
				break
			}
		}
		if !dup {
			needs = append(needs, f)
		}
	}
	sort.SliceStable(needs, func(i, j int) bool { return needs[i].level > needs[j].level })
	return needs
}

// ---- ordering ----

// sortNodes orders one level. Under a query the best subtree score goes
// first; otherwise the mode decides. Ties keep the order the tickets came in.
func sortNodes(nodes []*node, order orderMode, filtering bool) []*node {
	out := append([]*node(nil), nodes...)
	if filtering {
		sort.SliceStable(out, func(i, j int) bool { return out[i].best > out[j].best })
		return out
	}
	less := nodeLess(order)
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

func nodeLess(order orderMode) func(a, b *node) bool {
	switch order {
	case orderUpdated:
		return func(a, b *node) bool {
			ua, ub := a.updated(), b.updated()
			if !ua.Equal(ub) {
				return ua.After(ub)
			}
			return newerIssue(a.e.it, b.e.it)
		}
	case orderKey:
		return func(a, b *node) bool {
			if a.e.it.Project != b.e.it.Project {
				return a.e.it.Project < b.e.it.Project
			}
			na, _ := strconv.Atoi(a.e.it.number())
			nb, _ := strconv.Atoi(b.e.it.number())
			if na != nb {
				return na < nb
			}
			return a.e.it.Key < b.e.it.Key
		}
	case orderAttention:
		return func(a, b *node) bool {
			if a.level != b.level {
				return a.level > b.level
			}
			if ra, rb := stateRank(a.e.it.state()), stateRank(b.e.it.state()); ra != rb {
				return ra < rb
			}
			return a.updated().After(b.updated())
		}
	default:
		return func(a, b *node) bool {
			ca, cb := a.created(), b.created()
			if !ca.Equal(cb) {
				return ca.After(cb)
			}
			return newerIssue(a.e.it, b.e.it)
		}
	}
}

// created is the ticket's creation, or, for a ghost, the newest in its
// subtree: an old epic sits with the work under it, not at the bottom.
func (n *node) created() time.Time {
	if n.e.it.Ghost {
		return n.latest(func(it issue) time.Time { return it.Created })
	}
	return n.e.it.Created
}

func (n *node) updated() time.Time {
	if n.e.it.Ghost {
		return n.latest(func(it issue) time.Time { return it.Updated })
	}
	return n.e.it.Updated
}

// latest is the newest of f over the node and everything under it.
func (n *node) latest(f func(issue) time.Time) time.Time {
	t := f(n.e.it)
	for _, c := range n.children {
		if ct := c.latest(f); ct.After(t) {
			t = ct
		}
	}
	return t
}

// stateRank is the order of the states when attention ties: blocked, doing,
// to do.
func stateRank(state string) int {
	switch state {
	case stateBlocked:
		return 0
	case stateDoing:
		return 1
	}
	return 2
}
