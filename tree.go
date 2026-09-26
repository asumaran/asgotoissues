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

// ---- options ----

// showMode is what the list lists: the work going on, everything not
// finished, or everything.
type showMode string

const (
	showWorking showMode = "working" // a ticket in progress or with an open PR, and the open PRs
	showPending showMode = "pending" // working, plus blocked and not started tickets
	showAll     showMode = "all"
)

var showModes = []showMode{showWorking, showPending, showAll}

// parseShow reads a saved show, working when it is none of them. merged is
// the setting the show replaced: a saved hide reads as pending.
func parseShow(s, merged string) showMode {
	for _, m := range showModes {
		if string(m) == strings.ToLower(strings.TrimSpace(s)) {
			return m
		}
	}
	if merged == "hide" {
		return showPending
	}
	return defaultShow
}

// defaultShow is the show a fresh state dir opens with; the tests list
// everything.
var defaultShow = showWorking

// groupMode is how the list is laid out: the tree, or by phase (the state
// of the PRs).
type groupMode string

const (
	groupTree  groupMode = "tree"
	groupPhase groupMode = "phase"
)

var groupModes = []groupMode{groupTree, groupPhase}

func parseGroup(s string) groupMode {
	if strings.ToLower(strings.TrimSpace(s)) == string(groupPhase) {
		return groupPhase
	}
	return groupTree
}

// treeOpts is how the rows are built.
type treeOpts struct {
	order     orderMode
	prs       prsMode
	show      showMode
	group     groupMode
	collapsed map[string]bool // rows folded by hand, by foldKey
}

// ---- the tree ----

// node is one ticket in the tree: its entry, its children and what it says.
type node struct {
	e        *entry
	i        int // index of the entry, what hits are keyed by
	depth    int
	parent   *node
	children []*node
	level    int  // attention: the worst of its own PRs (a blocked ticket without one: a warning)
	done     bool // nothing left under it: every PR merged (or none on a ghost) and every child done
	shown    bool // the show mode lists it (as itself or as the container of children it lists)
	best     int  // best score in its subtree, under a query
	hit      bool
}

// active reports whether the ticket is work going on: mine, and in progress
// or with an open PR.
func (n *node) active() bool {
	if n.e.it.Ghost {
		return false
	}
	if n.e.it.state() == stateDoing {
		return true
	}
	for _, p := range n.e.pulls {
		if p.State == prOpen {
			return true
		}
	}
	return false
}

// noPR reports whether a started ticket of mine goes on without a PR: no PR
// and no children (a parent's work is its children's).
func (n *node) noPR() bool {
	st := n.e.it.state()
	return !n.e.it.Ghost && len(n.e.pulls) == 0 && len(n.children) == 0 && (st == stateDoing || st == stateBlocked)
}

// last is the ticket's last activity: its own update or its PRs'.
func (n *node) last() time.Time {
	t := n.e.it.Updated
	for _, p := range n.e.pulls {
		if p.Updated.After(t) {
			t = p.Updated
		}
	}
	return t
}

// pullShown reports whether a PR row is listed under the show and PRs modes.
func pullShown(p *pull, bases map[string]*pull, show showMode, prs prsMode) bool {
	if show != showAll && p.State != prOpen {
		return false
	}
	switch prs {
	case prsOpen:
		return p.State == prOpen
	case prsAttention:
		return p.attention(p.baseIn(bases)) >= levelWarn
	}
	return true
}

// forest is the tickets of every stack as trees, settled: what each needs,
// whether it is done or shown, and its best score under a query.
type forest struct {
	stacks []string
	roots  map[string][]*node
	bases  map[string]*pull
}

func grow(entries []*entry, pulls []pull, show showMode, hits map[int]hit) forest {
	f := forest{roots: map[string][]*node{}, bases: stackedBases(pulls)}
	nodes := make([]*node, len(entries))
	byURL := map[string]*node{}
	for i, e := range entries {
		nodes[i] = &node{e: e, i: i}
		if e.it.URL != "" {
			byURL[e.it.URL] = nodes[i]
		}
	}
	for _, n := range nodes {
		st := n.e.it.Stack
		if _, seen := f.roots[st]; !seen {
			f.stacks = append(f.stacks, st)
			f.roots[st] = nil
		}
		parent := byURL[n.e.it.ParentURL]
		if parent != nil && parent != n && parent.e.it.Stack == st && !descends(parent, n) {
			parent.children = append(parent.children, n)
			n.parent = parent
			continue
		}
		f.roots[st] = append(f.roots[st], n)
	}
	var settle func(n *node, depth int)
	settle = func(n *node, depth int) {
		n.depth = depth
		n.level = ownLevel(n.e, f.bases)
		h, ok := hits[n.i]
		n.hit = ok
		n.best = -1
		if ok {
			n.best = h.score
		}
		merged := len(n.e.pulls) > 0
		for _, p := range n.e.pulls {
			if p.State != prMerged {
				merged = false
			}
		}
		n.done = merged || n.e.it.Ghost && len(n.e.pulls) == 0
		anyShown := false
		for _, c := range n.children {
			settle(c, depth+1)
			n.best = max(n.best, c.best)
			n.done = n.done && c.done
			anyShown = anyShown || c.shown
		}
		switch show {
		case showAll:
			n.shown = true
		case showPending:
			n.shown = !n.done
		default:
			n.shown = !n.done && (n.active() || anyShown)
		}
	}
	for _, st := range f.stacks {
		for _, r := range f.roots[st] {
			settle(r, 0)
		}
	}
	return f
}

// ownLevel is what a ticket's own PRs need, the worst of them, and a
// warning for a blocked ticket of mine without a PR. Nothing is inherited:
// a parent does not take its children's.
func ownLevel(e *entry, bases map[string]*pull) int {
	lvl := levelNone
	for _, p := range e.pulls {
		lvl = max(lvl, p.attention(p.baseIn(bases)))
	}
	if len(e.pulls) == 0 && e.it.state() == stateBlocked && !e.it.Ghost {
		lvl = max(lvl, levelWarn)
	}
	return lvl
}

// foldKey is what a folded row is remembered by: a ticket by its URL, a
// stack, its group of PRs without a ticket and a phase section by name.
func foldKey(r row) string {
	switch r.kind {
	case rowIssue:
		return r.e.it.URL
	case rowHeader:
		return "stack:" + r.stack
	case rowGroup:
		return "group:" + r.stack
	case rowSection:
		return "section:" + r.stack + ":" + r.text
	}
	return ""
}

// buildTree is the list: rows in display order. hits, when the filter has
// terms, says which entries match and how well; then only the hits and their
// ancestors are listed, siblings go by their subtree's best score and the
// stacks by their best hit. Without hits every level goes by order. The
// show mode says which tickets and PRs are listed; a folded row (by hand,
// opts.collapsed) keeps what hangs from it out, unless a query is on (a
// search shows everything).
func buildTree(entries []*entry, pulls []pull, unlinked map[string][]*pull, opts treeOpts, hits map[int]hit) []row {
	filtering := hits != nil
	f := grow(entries, pulls, opts.show, hits)
	if opts.group == groupPhase {
		return buildPhase(f, unlinked, opts, hits)
	}
	folded := func(key string) bool { return !filtering && opts.collapsed[key] }
	listed := func(n *node) bool { return n.shown && (!filtering || n.best >= 0) }

	var cur []row
	// walk lists a ticket and what hangs from it. rails are the guides of
	// its ancestors (rails[j]: a │ in the arrow column of depth j), last
	// whether it is the last ticket under its parent.
	var walk func(n *node, rails []bool, last bool)
	walk = func(n *node, rails []bool, last bool) {
		h := hits[n.i]
		var prRows []row
		if !filtering || n.hit {
			for _, p := range n.e.pulls {
				if pullShown(p, f.bases, opts.show, opts.prs) {
					prRows = append(prRows, pullRow(p, n.e, n.e.it.URL, n.e.it.Stack, n.depth+1, f.bases))
				}
			}
		}
		var kids []*node
		for _, c := range sortNodes(n.children, opts.order, filtering) {
			if listed(c) {
				kids = append(kids, c)
			}
		}
		fold := folded(n.e.it.URL)
		r := row{kind: rowIssue, e: n.e, stack: n.e.it.Stack, depth: n.depth, parent: n.e.it.ParentURL,
			match: n.hit, ctx: filtering && !n.hit, score: h.score, idx: h.idx, level: n.level,
			kids: len(prRows) > 0 || len(kids) > 0, collapsed: fold, noPR: n.noPR(), last: n.last(),
			rails: rails, lastSib: last, kidRail: !fold && len(kids) > 0}
		cur = append(cur, r)
		if fold {
			return
		}
		down := rails
		if n.depth >= 1 {
			down = append(append([]bool(nil), rails...), !last)
		}
		for _, pr := range prRows {
			pr.rails = append(append([]bool(nil), down...), r.kidRail)
			cur = append(cur, pr)
		}
		for i, c := range kids {
			walk(c, down, i == len(kids)-1)
		}
	}
	var groups []stackRows
	for _, st := range f.stacks {
		cur = nil
		var roots []*node
		for _, r := range sortNodes(f.roots[st], opts.order, filtering) {
			if listed(r) {
				roots = append(roots, r)
			}
		}
		for i, r := range roots {
			walk(r, nil, i == len(roots)-1)
		}
		out := cur
		if !filtering {
			var prRows []row
			for _, p := range unlinked[st] {
				if pullShown(p, f.bases, opts.show, opts.prs) {
					r := pullRow(p, nil, "group:"+st, st, 1, f.bases)
					r.rails = []bool{false}
					prRows = append(prRows, r)
				}
			}
			if len(prRows) > 0 {
				g := row{kind: rowGroup, stack: st, text: "PRs without a ticket", kids: true, collapsed: folded("group:" + st)}
				out = append(out, g)
				if !g.collapsed {
					out = append(out, prRows...)
				}
			}
		}
		if len(out) == 0 {
			continue
		}
		best := -1
		for _, r := range f.roots[st] {
			best = max(best, r.best)
		}
		groups = append(groups, stackRows{st, best, out})
	}
	return stackRowsOut(groups, filtering, folded)
}

// stackRowsOut puts a header over every stack's rows; a folded stack keeps
// its header alone.
func stackRowsOut(groups []stackRows, filtering bool, folded func(string) bool) []row {
	if filtering {
		groups = rank(groups, func(g stackRows) int { return g.best }, nil)
	}
	var rows []row
	for _, g := range groups {
		fold := folded("stack:" + g.stack)
		rows = append(rows, row{kind: rowHeader, stack: g.stack, kids: true, collapsed: fold})
		if !fold {
			rows = append(rows, g.rows...)
		}
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

// pullRow is a PR under a ticket (or the unlinked group).
func pullRow(p *pull, e *entry, parent, stack string, depth int, bases map[string]*pull) row {
	base := p.baseIn(bases)
	return row{kind: rowPull, p: p, pe: e, stack: stack, depth: depth, parent: parent,
		level: p.attention(base), state: p.state(base), flags: p.flags(base), needN: len(p.needs(base)), last: p.Updated}
}

// ---- the phase view ----

// The phases, in the order the sections come: the PRs define them, and the
// started tickets without a PR make the last but one.
const (
	phaseReview = "PR in review"
	phaseDraft  = "draft PR"
	phaseNoPR   = "no PR"
	phaseMerged = "PRs merged"
)

var phases = []string{phaseReview, phaseDraft, phaseNoPR, phaseMerged}

// buildPhase lists every stack by phase: a section per PR state with one
// row per PR (a PR under two tickets once per ticket), each with the path
// of tickets it hangs from, and a section of the tickets without a PR.
// Empty sections are left out.
func buildPhase(f forest, unlinked map[string][]*pull, opts treeOpts, hits map[int]hit) []row {
	filtering := hits != nil
	folded := func(key string) bool { return !filtering && opts.collapsed[key] }
	var groups []stackRows
	for _, st := range f.stacks {
		sections := map[string][]row{}
		addPull := func(p *pull, n *node, path []pathKey) {
			if p.State == prClosed || !pullShown(p, f.bases, opts.show, opts.prs) {
				return
			}
			sec := phaseReview
			switch {
			case p.State == prMerged:
				sec = phaseMerged
			case p.Draft:
				sec = phaseDraft
			}
			var e *entry
			parent := "group:" + st
			if n != nil {
				e, parent = n.e, n.e.it.URL
			}
			r := pullRow(p, e, parent, st, 0, f.bases)
			r.path = path
			sections[sec] = append(sections[sec], r)
		}
		var walk func(n *node, path []pathKey)
		walk = func(n *node, path []pathKey) {
			here := append(append([]pathKey(nil), path...), pathKey{n.e.it.Key, n.depth})
			if !filtering || n.hit {
				for _, p := range n.e.pulls {
					addPull(p, n, here)
				}
				if !n.e.it.Ghost && len(n.e.pulls) == 0 && len(n.children) == 0 {
					if opts.show == showAll || opts.show == showPending || n.active() {
						h := hits[n.i]
						sections[phaseNoPR] = append(sections[phaseNoPR], row{kind: rowIssue, e: n.e, stack: st, parent: n.e.it.ParentURL,
							match: n.hit, score: h.score, idx: h.idx, level: n.level, noPR: n.noPR(), last: n.last(), path: path})
					}
				}
			}
			for _, c := range sortNodes(n.children, opts.order, filtering) {
				if !filtering || c.best >= 0 {
					walk(c, here)
				}
			}
		}
		for _, r := range sortNodes(f.roots[st], opts.order, filtering) {
			if !filtering || r.best >= 0 {
				walk(r, nil)
			}
		}
		if !filtering {
			for _, p := range unlinked[st] {
				addPull(p, nil, nil)
			}
		}
		var out []row
		for _, name := range phases {
			items := sections[name]
			if len(items) == 0 {
				continue
			}
			fold := folded("section:" + st + ":" + name)
			out = append(out, row{kind: rowSection, stack: st, text: name, count: len(items), kids: true, collapsed: fold})
			if !fold {
				out = append(out, items...)
			}
		}
		if len(out) == 0 {
			continue
		}
		best := -1
		for _, r := range f.roots[st] {
			best = max(best, r.best)
		}
		groups = append(groups, stackRows{st, best, out})
	}
	return stackRowsOut(groups, filtering, folded)
}

// pathKey is a ticket of a phase row's path: its key and its depth (the
// key's color).
type pathKey struct {
	key   string
	depth int
}

// ---- folding by level ----

// foldLevel is the folds that show the tree down to level (1: the roots
// alone, folded), 0 for none: every ticket from depth level-1 down that has
// something under it, and every group of PRs without a ticket at level 1.
func foldLevel(rows []row, level int) map[string]bool {
	out := map[string]bool{}
	if level <= 0 {
		return out
	}
	for _, r := range rows {
		switch {
		case r.kind == rowIssue && r.kids && r.depth >= level-1:
			out[foldKey(r)] = true
		case r.kind == rowGroup && level == 1:
			out[foldKey(r)] = true
		}
	}
	return out
}

// deepest is how many levels the unfolded tree has: the depth of its
// deepest ticket with something under it, plus one.
func deepest(rows []row) int {
	d := 0
	for _, r := range rows {
		if (r.kind == rowIssue && r.kids) || r.kind == rowGroup {
			d = max(d, r.depth+1)
		}
	}
	return d
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

// updated is the ticket's last activity (its PRs' included), or, for a
// ghost, the newest in its subtree.
func (n *node) updated() time.Time {
	if n.e.it.Ghost {
		return n.newest()
	}
	return n.last()
}

// newest is the latest activity over the node and everything under it.
func (n *node) newest() time.Time {
	t := n.last()
	for _, c := range n.children {
		if ct := c.newest(); ct.After(t) {
			t = ct
		}
	}
	return t
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
