package main

// A pull request, as the list shows it under the tickets it belongs to. PRs
// come from GitHub (pulls.go) whatever tracker the ticket lives in, and are
// linked to tickets here, by reference: the keys and issue numbers a PR
// names in its branch, title and body (pullRefs). What a PR needs from me is
// derived from its fetched state (flags), never cached.

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// pull states, as GitHub spells them
const (
	prOpen   = "OPEN"
	prMerged = "MERGED"
	prClosed = "CLOSED"
)

type pull struct {
	URL              string    `json:"url"` // identity
	Number           int       `json:"number"`
	Repo             string    `json:"repo"` // owner/repo, as GitHub spells it
	Stack            string    `json:"stack"`
	Key              string    `json:"key"` // repo#N, owner/repo#N when two owners share the name
	Title            string    `json:"title"`
	Body             string    `json:"body,omitempty"`
	State            string    `json:"state"` // prOpen | prMerged | prClosed
	Draft            bool      `json:"draft,omitempty"`
	Head             string    `json:"head"`
	Base             string    `json:"base"`
	HeadOID          string    `json:"head_oid,omitempty"`
	BaseOID          string    `json:"base_oid,omitempty"`
	Author           string    `json:"author"`
	Mine             bool      `json:"mine,omitempty"`             // the author is whoever gh is logged in as
	ReviewRequested  bool      `json:"review_requested,omitempty"` // my review is asked for (a user or a team request)
	ReviewDecision   string    `json:"review_decision,omitempty"`  // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, "" (no rule)
	Mergeable        string    `json:"mergeable,omitempty"`        // MERGEABLE, CONFLICTING, UNKNOWN
	MergeState       string    `json:"merge_state,omitempty"`      // CLEAN, BLOCKED, BEHIND, DIRTY, UNSTABLE, HAS_HOOKS, UNKNOWN
	Checks           string    `json:"checks,omitempty"`           // the rollup: SUCCESS, FAILURE, ERROR, PENDING, EXPECTED; "" without checks
	Approvals        int       `json:"approvals,omitempty"`        // latest reviews that approve
	ChangesRequested int       `json:"changes_requested,omitempty"`
	ReviewRequests   int       `json:"review_requests,omitempty"` // reviewers still asked
	Refs             []string  `json:"refs,omitempty"`            // what it says it is for (strong)
	WeakRefs         []string  `json:"weak_refs,omitempty"`       // keys merely mentioned in the body
	Created          time.Time `json:"created"`
	Updated          time.Time `json:"updated"`
}

func (p pull) number() string { return strconv.Itoa(p.Number) }

// ---- attention ----

// The levels a flag can have, worst last. A ticket takes the worst level of
// its PRs and its descendants (attentionOf).
const (
	levelNone = iota
	levelWarn
	levelOK
	levelBad
)

// prFlag is one thing a PR's row says about it: `conflicts`, `approved`.
type prFlag struct {
	text  string
	level int
}

// flags is what a PR needs, in the order the row shows them. base is the PR
// this one is stacked on (its base branch is the head of another fetched PR
// of the repo), nil when it sits on a plain branch. A thing someone must act
// on is bad only when that someone is me: my PR, or a review asked of me;
// on another person's PR it is a warning.
func (p pull) flags(base *pull) []prFlag {
	switch p.State {
	case prMerged:
		return []prFlag{{"merged", levelNone}}
	case prClosed:
		return []prFlag{{"closed", levelNone}}
	}
	act := levelWarn
	if p.Mine {
		act = levelBad
	}
	var out []prFlag
	blocked := false // something keeps it from merging as it is
	add := func(text string, level int) {
		out = append(out, prFlag{text, level})
	}
	if p.Mergeable == "CONFLICTING" || p.MergeState == "DIRTY" {
		add("conflicts", act)
		blocked = true
	}
	changes := p.ReviewDecision == "CHANGES_REQUESTED" || p.ReviewDecision == "" && p.ChangesRequested > 0
	if changes {
		add("changes requested", act)
		blocked = true
	}
	if p.Checks == "FAILURE" || p.Checks == "ERROR" {
		add("ci failed", act)
		blocked = true
	}
	if p.ReviewRequested {
		add("review requested", levelBad)
	}
	if base != nil && base.State == prMerged {
		add("base merged", act)
		blocked = true
	}
	if p.MergeState == "BEHIND" {
		add("behind base", levelWarn)
	}
	if base != nil && base.State == prOpen {
		add("on #"+base.number(), levelNone)
	}
	if p.Draft {
		add("draft", levelNone)
	}
	pending := p.Checks == "PENDING" || p.Checks == "EXPECTED"
	if pending {
		add("ci pending", levelWarn)
	}
	approved := p.ReviewDecision == "APPROVED" || p.ReviewDecision == "" && p.Approvals > 0 && p.ChangesRequested == 0
	switch {
	case approved && !blocked && !pending && !p.Draft:
		add("approved", levelOK)
	case !approved && !blocked && !p.Draft && (p.ReviewDecision == "REVIEW_REQUIRED" || p.ReviewRequests > 0):
		add("awaiting review", levelWarn)
	}
	return out
}

// level is the worst level of a set of flags.
func level(flags []prFlag) int {
	l := levelNone
	for _, f := range flags {
		l = max(l, f.level)
	}
	return l
}

// ---- references ----

// ticketRe matches a Jira-style key: a project key of letters and digits
// (two characters at least, a letter first), a dash and digits. It is not
// anchored to a word boundary on purpose: an underscore is a word character,
// and fix_eshop-270 holds a ticket. A key that is no ticket of mine links to
// nothing, so the wide grammar costs nothing.
var (
	ticketRe  = regexp.MustCompile(`(?i)[a-z][a-z0-9]+-[0-9]+`)
	browseRe  = regexp.MustCompile(`/browse/([A-Za-z][A-Za-z0-9]+-[0-9]+)`)
	closingRe = regexp.MustCompile(`(?i)\b(?:close|closes|closed|fix|fixes|fixed|resolve|resolves|resolved)\s*:?\s+((?:[\w.-]+/[\w.-]+)?#[0-9]+)`)
)

// pullRefs is what a PR says it is for. Strong: the ticket keys in its head
// branch and title (where Jira itself reads them), the issues its closing
// keywords name (fixes #12, closes other/repo#3) and closing, the ones
// GitHub resolved. Weak: the keys mentioned in the body, as text or as
// /browse/KEY links ("depends on ESHOP-2707" in a stacked PR, a list of
// related tickets), which count only when nothing strong links. Jira keys
// come back upper case, issue refs as owner/repo#N in lower case; a ref
// found twice comes once.
func pullRefs(repo, head, title, body string, closing []string) (strong, weak []string) {
	seen := map[string]bool{}
	add := func(list *[]string, ref string) {
		if ref == "" || seen[ref] {
			return
		}
		seen[ref] = true
		*list = append(*list, ref)
	}
	for _, s := range []string{head, title} {
		for _, k := range ticketRe.FindAllString(s, -1) {
			add(&strong, strings.ToUpper(k))
		}
	}
	for _, m := range closingRe.FindAllStringSubmatch(body, -1) {
		add(&strong, issueRef(repo, m[1]))
	}
	for _, c := range closing {
		add(&strong, strings.ToLower(c))
	}
	for _, m := range browseRe.FindAllStringSubmatch(body, -1) {
		add(&weak, strings.ToUpper(m[1]))
	}
	for _, k := range ticketRe.FindAllString(body, -1) {
		add(&weak, strings.ToUpper(k))
	}
	return strong, weak
}

// issueRef is a GitHub issue reference as the linking compares it:
// owner/repo#N in lower case, #N resolved against the PR's own repo.
func issueRef(repo, ref string) string {
	if strings.HasPrefix(ref, "#") {
		return strings.ToLower(repo) + ref
	}
	return strings.ToLower(ref)
}
