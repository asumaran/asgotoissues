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
	ToAnswer         bool      `json:"to_answer,omitempty"`       // a review thread not resolved whose last comment is not mine
	Behind           int       `json:"behind,omitempty"`          // commits of the base the head lacks
	Refs             []string  `json:"refs,omitempty"`            // what it says it is for (strong)
	WeakRefs         []string  `json:"weak_refs,omitempty"`       // keys merely mentioned in the body
	Created          time.Time `json:"created"`
	Updated          time.Time `json:"updated"`
}

func (p pull) number() string { return strconv.Itoa(p.Number) }

// ---- attention ----

// The levels a flag can have, worst last: they order the list (attention),
// they are not what the row looks like (tone is).
const (
	levelNone = iota
	levelWarn
	levelOK
	levelBad
)

// The tones a word of a details line is drawn in.
const (
	toneDim = iota
	toneGreen
	toneYellow
	toneRed
	toneBlue
)

// prFlag is one word a PR's row says about it: its state (`in review`),
// something it needs (`conflicts`) or a fact (`behind master`).
type prFlag struct {
	text  string
	level int
	tone  int
}

// state is the PR's own state, the first word of its details: merged,
// closed, draft, changes requested, approved or in review. A change asked
// of me is bad; an approval with nothing in its way is ready (ok); a PR
// that waits for a review is a warning.
func (p pull) state(base *pull) prFlag {
	switch {
	case p.State == prMerged:
		return prFlag{"merged", levelNone, toneGreen}
	case p.State == prClosed:
		return prFlag{"closed", levelNone, toneDim}
	case p.Draft:
		return prFlag{"draft", levelNone, toneDim}
	case p.ReviewDecision == "CHANGES_REQUESTED" || p.ReviewDecision == "" && p.ChangesRequested > 0:
		return prFlag{"changes requested", p.act(), toneRed}
	case p.ReviewDecision == "APPROVED" || p.ReviewDecision == "" && p.Approvals > 0:
		lvl := levelNone
		if !p.blocked(base) && !p.pending() {
			lvl = levelOK
		}
		return prFlag{"approved", lvl, toneGreen}
	}
	lvl := levelNone
	if p.ReviewDecision == "REVIEW_REQUIRED" || p.ReviewRequests > 0 {
		lvl = levelWarn
	}
	return prFlag{"in review", lvl, toneYellow}
}

// act is the level of something someone must act on: bad when that someone
// is me (my PR), a warning on another person's.
func (p pull) act() int {
	if p.Mine {
		return levelBad
	}
	return levelWarn
}

func (p pull) conflicting() bool { return p.Mergeable == "CONFLICTING" || p.MergeState == "DIRTY" }
func (p pull) failing() bool     { return p.Checks == "FAILURE" || p.Checks == "ERROR" }
func (p pull) pending() bool     { return p.Checks == "PENDING" || p.Checks == "EXPECTED" }

// blocked reports whether something keeps it from merging as it is.
func (p pull) blocked(base *pull) bool {
	return p.conflicting() || p.failing() || base != nil && base.State == prMerged
}

// needs is what the PR needs, in the order the row says it: bad (red) when
// it is mine to do, a warning (yellow) on another person's PR. base is the
// PR this one is stacked on (its base branch is the head of another fetched
// PR of the repo), nil when it sits on a plain branch.
func (p pull) needs(base *pull) []prFlag {
	if p.State != prOpen {
		return nil
	}
	var out []prFlag
	add := func(text string, lvl int) {
		tone := toneYellow
		if lvl == levelBad {
			tone = toneRed
		}
		out = append(out, prFlag{text, lvl, tone})
	}
	if p.conflicting() {
		add("conflicts", p.act())
	}
	if p.failing() {
		add("ci failed", p.act())
	}
	if p.ToAnswer && p.Mine {
		add("to answer", levelBad)
	}
	if base != nil && base.State == prMerged {
		add("base merged", p.act())
	}
	if p.ReviewRequested {
		add("review requested", levelBad)
	}
	return out
}

// facts is what else the row says, dim: the base moved (yellow when the
// repo will not merge it so), what it is stacked on, checks still running,
// and who wrote it when it is not me.
func (p pull) facts(base *pull) []prFlag {
	var out []prFlag
	if p.State == prOpen {
		if p.Behind > 0 || p.MergeState == "BEHIND" {
			base := p.Base
			if base == "" {
				base = "base"
			}
			f := prFlag{"behind " + base, levelNone, toneDim}
			if p.MergeState == "BEHIND" {
				f.level, f.tone = levelWarn, toneYellow
			}
			out = append(out, f)
		}
		if base != nil && base.State == prOpen {
			out = append(out, prFlag{"stacked on #" + base.number(), levelNone, toneDim})
		}
		if p.pending() {
			out = append(out, prFlag{"ci pending", levelWarn, toneDim})
		}
	}
	if !p.Mine && p.Author != "" {
		out = append(out, prFlag{"by " + p.Author, levelNone, toneDim})
	}
	return out
}

// flags is everything the row says about a PR after its state: what it
// needs, then the facts.
func (p pull) flags(base *pull) []prFlag {
	return append(p.needs(base), p.facts(base)...)
}

// level is the worst level of a set of flags.
func level(flags []prFlag) int {
	l := levelNone
	for _, f := range flags {
		l = max(l, f.level)
	}
	return l
}

// attention is the PR's level: the worst of its state and its flags.
func (p pull) attention(base *pull) int {
	return max(p.state(base).level, level(p.flags(base)))
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
