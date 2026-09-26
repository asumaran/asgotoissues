package main

import (
	"strings"
	"testing"
	"time"
)

// treeIssues is a stack with an epic that is not mine (a ghost), a story
// under it, a sub-task under the story, a ticket of its own, and a GitHub
// stack with an issue and its parent.
func treeIssues() []issue {
	at := func(d int) time.Time { return time.Unix(int64(1000+d), 0) }
	return []issue{
		{Key: "W-10", Stack: "work", URL: "https://w/browse/W-10", Summary: "story", Project: "W", ParentKey: "W-1", ParentURL: "https://w/browse/W-1", Status: "In Progress", State: stateDoing, Created: at(10), Updated: at(10)},
		{Key: "W-11", Stack: "work", URL: "https://w/browse/W-11", Summary: "sub-task", Project: "W", ParentKey: "W-10", ParentURL: "https://w/browse/W-10", Status: "Open", Created: at(11), Updated: at(30)},
		{Key: "W-12", Stack: "work", URL: "https://w/browse/W-12", Summary: "alone", Project: "W", ParentKey: "W-99", ParentURL: "https://w/browse/W-99", Status: "Blocked", State: stateBlocked, Created: at(12), Updated: at(12)},
		{Key: "W-1", Stack: "work", URL: "https://w/browse/W-1", Summary: "epic", Project: "W", Status: "Open", Ghost: true, Created: at(1), Updated: at(1)},
		{Key: "app#5", Stack: "home", URL: "https://github.com/me/app/issues/5", Source: kindGitHub, Project: "me/app", Summary: "child", ParentKey: "app#2", ParentURL: "https://github.com/me/app/issues/2", Status: "open", Created: at(5), Updated: at(5)},
		{Key: "app#2", Stack: "home", URL: "https://github.com/me/app/issues/2", Source: kindGitHub, Project: "me/app", Summary: "parent", Status: "open", Created: at(2), Updated: at(2)},
	}
}

func treePulls() []pull {
	return []pull{
		{URL: "u1", Number: 1, Repo: "me/front", Key: "front#1", Stack: "work", Title: "one", State: prOpen, Mine: true, Head: "feat/W-11", Refs: []string{"W-11"}, Mergeable: "CONFLICTING", Updated: time.Unix(1, 0)},
		{URL: "u2", Number: 2, Repo: "me/infra", Key: "infra#2", Stack: "work", Title: "two", State: prMerged, Mine: true, Refs: []string{"W-11", "W-12"}, Updated: time.Unix(2, 0)},
		{URL: "u3", Number: 3, Repo: "me/front", Key: "front#3", Stack: "work", Title: "three", State: prOpen, Mine: true, WeakRefs: []string{"W-10", "W-404"}, ReviewDecision: "APPROVED", Updated: time.Unix(3, 0)},
		{URL: "u4", Number: 4, Repo: "me/front", Key: "front#4", Stack: "work", Title: "four", State: prOpen, Mine: true, Refs: []string{"NOPE-1"}, Updated: time.Unix(4, 0)},
		{URL: "u5", Number: 5, Repo: "me/front", Key: "front#5", Stack: "work", Title: "five", State: prOpen, Refs: []string{"NOPE-2"}, Updated: time.Unix(5, 0)},
		{URL: "u6", Number: 6, Repo: "me/app", Key: "app#6", Stack: "home", Title: "six", State: prOpen, Mine: true, Refs: []string{"me/app#5"}, Updated: time.Unix(6, 0)},
		{URL: "u7", Number: 7, Repo: "me/app", Key: "app#7", Stack: "other", Title: "seven", State: prOpen, Mine: true, Refs: []string{"W-11"}, Updated: time.Unix(7, 0)},
	}
}

func treeModel() (entries []*entry, pulls []pull, unlinked map[string][]*pull) {
	entries = buildEntries(treeIssues())
	pulls = treePulls()
	unlinked = linkPulls(entries, pulls)
	return entries, pulls, unlinked
}

func rowKeys(rows []row) string {
	var out []string
	for _, r := range rows {
		ind := strings.Repeat(">", r.depth)
		switch r.kind {
		case rowHeader:
			out = append(out, "H:"+r.stack)
		case rowGroup:
			out = append(out, "G")
		case rowIssue:
			out = append(out, ind+r.e.it.Key)
		case rowPull:
			out = append(out, ind+"↳"+r.p.Key)
		}
	}
	return strings.Join(out, " ")
}

// TestLinkPulls: a PR hangs from every ticket of its stack it names; the
// weak references count only when nothing strong links; a PR of mine that
// links nothing goes to the unlinked group, another person's is dropped; a
// PR of another stack never links here.
func TestLinkPulls(t *testing.T) {
	entries, _, unlinked := treeModel()
	by := map[string][]*pull{}
	for _, e := range entries {
		by[e.it.Key] = e.pulls
	}
	keys := func(ps []*pull) string {
		var out []string
		for _, p := range ps {
			out = append(out, p.Key)
		}
		return strings.Join(out, " ")
	}
	if got := keys(by["W-11"]); got != "front#1 infra#2" {
		t.Errorf("W-11 pulls = %q, want the open one first, then the merged one (never the other stack's)", got)
	}
	if got := keys(by["W-12"]); got != "infra#2" {
		t.Errorf("W-12 pulls = %q", got)
	}
	if got := keys(by["W-10"]); got != "front#3" {
		t.Errorf("W-10 pulls = %q, want the weak link", got)
	}
	if got := keys(by["app#5"]); got != "app#6" {
		t.Errorf("app#5 pulls = %q, want the github ref", got)
	}
	if got := keys(unlinked["work"]); got != "front#4" {
		t.Errorf("unlinked work = %q, want my PR without a ticket alone", got)
	}
	if len(unlinked["home"]) != 0 || len(unlinked["other"]) == 0 {
		t.Errorf("unlinked = %v", unlinked)
	}
}

// TestBuildTreeNests: a ticket hangs from the ticket its ParentURL names
// (the ghost epic included, three levels down), one whose parent is not in
// the list is a root, PRs come right under their ticket before its
// children, the group of unlinked PRs closes the stack, and the GitHub
// stack nests the same way.
func TestBuildTreeNests(t *testing.T) {
	entries, pulls, unlinked := treeModel()
	rows := buildTree(entries, pulls, unlinked, orderCreated, prsAll, false, nil, nil)
	want := "H:work W-12 >↳infra#2 W-1 >W-10 >>↳front#3 >>W-11 >>>↳front#1 >>>↳infra#2 G >↳front#4 H:home app#2 >app#5 >>↳app#6"
	if got := rowKeys(rows); got != want {
		t.Errorf("rows = %s\nwant   %s", got, want)
	}
	for _, r := range rows {
		if r.kind == rowPull && r.p.Key == "infra#2" && r.parent == "" {
			t.Errorf("a PR row hangs from its ticket: %+v", r)
		}
	}
	// The row's identity tells the two rows of infra#2 apart.
	ids := map[string]bool{}
	for _, r := range rows {
		if r.selectable() {
			ids[r.id()] = true
		}
	}
	if len(ids) != 12 {
		t.Errorf("%d distinct ids, want every selectable row its own", len(ids))
	}
}

// TestBuildTreeAttention: a ticket's level is the worst of its PRs and its
// descendants', a blocked ticket without one is a warning, and every PR
// merged reads as done.
func TestBuildTreeAttention(t *testing.T) {
	entries, pulls, unlinked := treeModel()
	rows := buildTree(entries, pulls, unlinked, orderCreated, prsAll, false, nil, nil)
	lvl := map[string]int{}
	merged := map[string]bool{}
	for _, r := range rows {
		if r.kind == rowIssue {
			lvl[r.e.it.Key] = r.level
			merged[r.e.it.Key] = r.merged
		}
	}
	for key, want := range map[string]int{"W-11": levelBad, "W-10": levelBad, "W-1": levelBad, "W-12": levelOK, "app#5": levelNone, "app#2": levelNone} {
		if lvl[key] != want {
			t.Errorf("%s level = %d, want %d", key, lvl[key], want)
		}
	}
	if !merged["W-12"] || merged["W-11"] {
		t.Errorf("all merged: W-12 %v, W-11 %v", merged["W-12"], merged["W-11"])
	}
	// A blocked ticket without a PR is a warning.
	alone := buildEntries([]issue{{Key: "B-1", Stack: "s", URL: "b1", State: stateBlocked}})
	if rows := buildTree(alone, nil, nil, orderCreated, prsAll, false, nil, nil); rows[1].level != levelWarn {
		t.Errorf("blocked without a PR: level %d", rows[1].level)
	}
}

// TestOrderModes: every level goes by the chosen order; a ghost by its
// children; a saved value that is none of them reads as created.
func TestOrderModes(t *testing.T) {
	entries, pulls, unlinked := treeModel()
	roots := func(order orderMode) string {
		var out []string
		for _, r := range buildTree(entries, pulls, unlinked, order, prsOpen, false, nil, nil) {
			if r.kind == rowIssue && r.depth == 0 && r.stack == "work" {
				out = append(out, r.e.it.Key)
			}
		}
		return strings.Join(out, " ")
	}
	for order, want := range map[orderMode]string{
		orderCreated:   "W-12 W-1", // the ghost epic counts its newest descendant (W-11, created after W-12? no: W-12 is newer)
		orderUpdated:   "W-1 W-12", // W-11 was updated last, and it is under the epic
		orderKey:       "W-1 W-12",
		orderAttention: "W-1 W-12", // bad (conflicts under the epic) before ok (all merged)
	} {
		if got := roots(order); got != want {
			t.Errorf("%s: roots = %q, want %q", order, got, want)
		}
	}
	if parseOrder("nonsense") != orderCreated || parseOrder(" Updated ") != orderUpdated {
		t.Errorf("parseOrder falls back to created and folds case")
	}
	if parsePrs("x") != prsAll || parsePrs("open") != prsOpen {
		t.Errorf("parsePrs falls back to all")
	}
}

// TestPrsModes: open hides the merged PR rows, attention keeps only the
// ones that need something.
func TestPrsModes(t *testing.T) {
	entries, pulls, unlinked := treeModel()
	prs := func(mode prsMode) string {
		var out []string
		for _, r := range buildTree(entries, pulls, unlinked, orderCreated, mode, false, nil, nil) {
			if r.kind == rowPull {
				out = append(out, r.p.Key)
			}
		}
		return strings.Join(out, " ")
	}
	if got := prs(prsAll); got != "infra#2 front#3 front#1 infra#2 front#4 app#6" {
		t.Errorf("all = %q", got)
	}
	if got := prs(prsOpen); got != "front#3 front#1 front#4 app#6" {
		t.Errorf("open = %q", got)
	}
	if got := prs(prsAttention); got != "front#3 front#1" {
		t.Errorf("attention = %q, want the conflicting one and the approved one", got)
	}
}

// TestHideMerged: hiding the merged work leaves out the merged PR rows and
// the tickets with nothing left under them: W-12 (its only PR is merged)
// goes, W-11 stays without its merged PR, the ghost epic stays for the
// work under it, and my open PR without a ticket stays.
func TestHideMerged(t *testing.T) {
	entries, pulls, unlinked := treeModel()
	rows := buildTree(entries, pulls, unlinked, orderCreated, prsAll, true, nil, nil)
	want := "H:work W-1 >W-10 >>↳front#3 >>W-11 >>>↳front#1 G >↳front#4 H:home app#2 >app#5 >>↳app#6"
	if got := rowKeys(rows); got != want {
		t.Errorf("rows = %s\nwant   %s", got, want)
	}
	// A ghost whose only child is done goes with it.
	done := buildEntries([]issue{
		{Key: "E-1", Stack: "s", URL: "e1", Ghost: true},
		{Key: "E-2", Stack: "s", URL: "e2", ParentURL: "e1"},
	})
	ps := []pull{{URL: "u", Repo: "r/r", Key: "r#1", Stack: "s", State: prMerged, Mine: true, Refs: []string{"E-2"}}}
	un := linkPulls(done, ps)
	if rows := buildTree(done, ps, un, orderCreated, prsAll, true, nil, nil); len(rows) != 0 {
		t.Errorf("a done subtree under a ghost is hidden whole: %s", rowKeys(rows))
	}
	if rows := buildTree(done, ps, un, orderCreated, prsAll, false, nil, nil); rowKeys(rows) != "H:s E-1 >E-2 >>↳r#1" {
		t.Errorf("shown otherwise: %s", rowKeys(rows))
	}
}

// TestCollapsed: a folded ticket keeps its row, marked, and lists neither
// its PRs nor its children; a query unfolds everything.
func TestCollapsed(t *testing.T) {
	entries, pulls, unlinked := treeModel()
	folded := map[string]bool{"https://w/browse/W-10": true}
	rows := buildTree(entries, pulls, unlinked, orderCreated, prsAll, false, folded, nil)
	if got := rowKeys(rows); got != "H:work W-12 >↳infra#2 W-1 >W-10 G >↳front#4 H:home app#2 >app#5 >>↳app#6" {
		t.Errorf("rows = %s", got)
	}
	for _, r := range rows {
		if r.kind == rowIssue && (r.e.it.Key == "W-10") != r.collapsed {
			t.Errorf("%s collapsed = %v", r.e.it.Key, r.collapsed)
		}
		if r.kind == rowIssue && !r.kids { // every ticket of the fixture has a PR or a child
			t.Errorf("%s has something to fold", r.e.it.Key)
		}
	}
	s, k, m := corpora(entries)
	rows = buildRows(entries, pulls, unlinked, orderCreated, prsAll, false, folded, "sub-task", s, k, m)
	if got := rowKeys(rows); got != "H:work W-1 >W-10 >>W-11 >>>↳front#1 >>>↳infra#2" {
		t.Errorf("a query shows what is folded: %s", got)
	}
}

// TestBuildTreeQueryKeepsAncestors: with a query the tree stays a tree: the
// hits and the ancestors that lead to them (as context), PR rows under the
// hits only, siblings by their subtree's best score, the stacks by their
// best hit, and the first hit is where the cursor goes.
func TestBuildTreeQueryKeepsAncestors(t *testing.T) {
	entries, pulls, unlinked := treeModel()
	s, k, m := corpora(entries)
	rows := buildRows(entries, pulls, unlinked, orderCreated, prsAll, false, nil, "sub-task", s, k, m)
	if got := rowKeys(rows); got != "H:work W-1 >W-10 >>W-11 >>>↳front#1 >>>↳infra#2" {
		t.Errorf("rows = %s", got)
	}
	if !rows[3].match || rows[1].match || !rows[1].ctx || rows[1].idx != nil {
		t.Errorf("W-11 is the hit, W-1 the context: %+v %+v", rows[1], rows[3])
	}
	if i := firstHit(rows); i != 3 {
		t.Errorf("first hit = %d, want W-11", i)
	}
	// A PR's key finds its ticket, and the stack of the best hit goes first.
	rows = buildRows(entries, pulls, unlinked, orderCreated, prsAll, false, nil, "app#6", s, k, m)
	if got := rowKeys(rows); !strings.HasPrefix(got, "H:home app#2 >app#5 >>↳app#6") {
		t.Errorf("rows = %s", got)
	}
	if rows := buildRows(entries, pulls, unlinked, orderCreated, prsAll, false, nil, "zzzz", s, k, m); len(rows) != 0 {
		t.Errorf("no hit, no rows: %s", rowKeys(rows))
	}
}

// TestBuildTreeCycle: a parent that is its own descendant stays a root, so
// a corrupt snapshot cannot loop the walk.
func TestBuildTreeCycle(t *testing.T) {
	entries := buildEntries([]issue{
		{Key: "A-1", Stack: "s", URL: "a1", ParentURL: "a2"},
		{Key: "A-2", Stack: "s", URL: "a2", ParentURL: "a1"},
		{Key: "A-3", Stack: "s", URL: "a3", ParentURL: "a3"},
	})
	rows := buildTree(entries, nil, nil, orderKey, prsAll, false, nil, nil)
	if got := rowKeys(rows); got != "H:s A-2 >A-1 A-3" {
		t.Errorf("rows = %s", got)
	}
}

// TestMergePullsFallsBackPerStack: a stack whose source answered takes the
// answer, an empty one included; one that did not keeps its cached PRs; a
// merge state GitHub has not computed yet keeps the cached one while the
// branches did not move.
func TestMergePullsFallsBackPerStack(t *testing.T) {
	stacks := []stack{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	cached := []pull{
		{URL: "a1", Stack: "a", Mergeable: "CONFLICTING", MergeState: "DIRTY", HeadOID: "h", BaseOID: "b"},
		{URL: "b1", Stack: "b"},
		{URL: "c1", Stack: "c"},
	}
	fresh := map[string][]pull{
		"a/pulls": {{URL: "a1", Stack: "a", Mergeable: "UNKNOWN", HeadOID: "h", BaseOID: "b"}, {URL: "a2", Stack: "a", Mergeable: "UNKNOWN", HeadOID: "h2"}},
		"c/pulls": {},
	}
	got := mergePulls(stacks, fresh, cached)
	var urls []string
	for _, p := range got {
		urls = append(urls, p.URL)
	}
	if strings.Join(urls, " ") != "a1 a2 b1" {
		t.Errorf("merged = %v, want a's fresh PRs, b's cached ones and nothing of c", urls)
	}
	if got[0].Mergeable != "CONFLICTING" || got[0].MergeState != "DIRTY" {
		t.Errorf("an unknown merge state keeps the cached one: %+v", got[0])
	}
	if got[1].Mergeable != "UNKNOWN" {
		t.Errorf("a new PR has nothing to carry: %+v", got[1])
	}
}

// TestBackfillParentURLs: a snapshot from before parent_url gets the parent's
// URL on its stack's site, for Jira issues only.
func TestBackfillParentURLs(t *testing.T) {
	issues := []issue{
		{Key: "A-2", Stack: "a", ParentKey: "A-1"},
		{Key: "A-3", Stack: "a", ParentKey: "A-1", ParentURL: "kept"},
		{Key: "x#2", Stack: "a", Source: kindGitHub, ParentKey: "x#1"},
		{Key: "B-2", Stack: "b", ParentKey: "B-1"},
	}
	backfillParentURLs(issues, []stack{{Name: "a", BaseURL: "https://a.example"}, {Name: "b"}})
	want := []string{"https://a.example/browse/A-1", "kept", "", ""}
	for i, w := range want {
		if issues[i].ParentURL != w {
			t.Errorf("%s: parent url = %q, want %q", issues[i].Key, issues[i].ParentURL, w)
		}
	}
}
