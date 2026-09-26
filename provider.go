package main

// The tracker-neutral side: the issue every provider produces, the provider
// seam, and the plumbing that runs one fetch per source as a tea.Cmd and
// merges the results. A source is one tracker of one stack, so a stack that
// lists two trackers is fetched, and falls back to its cache, per tracker.
// A stack with GitHub owners has one more source, its pull requests
// (pulls.go), whatever tracker its tickets live in.

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	kindJira   = "jira"
	kindGitHub = "github"
	kindPulls  = "pulls" // the PRs of a stack, never an issue tracker
)

// states an issue can be in, whatever its tracker calls them
const (
	stateTodo    = "todo"
	stateDoing   = "doing"
	stateBlocked = "blocked"
)

const (
	bodyWiki     = "wiki" // Jira wiki markup, the default
	bodyMarkdown = "markdown"
)

type issue struct {
	Key           string    `json:"key"` // identity within a source: "PLAT-2099", "repo#12"
	Stack         string    `json:"stack"`
	Source        string    `json:"source,omitempty"` // provider kind; empty in older caches means jira
	URL           string    `json:"url"`
	Summary       string    `json:"summary"`
	Description   string    `json:"description"`           // may be empty
	BodyFormat    string    `json:"body_format,omitempty"` // bodyWiki when empty
	Status        string    `json:"status"`                // the tracker's own name for it
	State         string    `json:"state,omitempty"`       // stateTodo | stateDoing | stateBlocked
	StatusCat     string    `json:"status_cat"`            // Jira: "To Do" | "In Progress" | ...
	Type          string    `json:"type"`
	Priority      string    `json:"priority"`
	Project       string    `json:"project"` // Jira project key, GitHub owner/repo
	ParentKey     string    `json:"parent_key,omitempty"`
	ParentSummary string    `json:"parent_summary,omitempty"`
	ParentURL     string    `json:"parent_url,omitempty"` // the parent's identity: the tree nests by it
	Ghost         bool      `json:"ghost,omitempty"`      // not in my list: fetched as the parent of one that is
	Meta          []string  `json:"meta,omitempty"`       // preview meta parts, when the fields above do not cover them
	Created       time.Time `json:"created"`
	Updated       time.Time `json:"updated"`
}

// number returns the numeric part of the key ("PLAT-2099" → "2099",
// "repo#12" → "12").
func (i issue) number() string {
	if p := strings.LastIndexAny(i.Key, "-#"); p >= 0 {
		return i.Key[p+1:]
	}
	return i.Key
}

func (i issue) sourceKind() string {
	if i.Source == "" {
		return kindJira
	}
	return i.Source
}

// ref is the issue as a PR names it (pullRefs): the key of a Jira ticket,
// owner/repo#N in lower case for a GitHub issue.
func (i issue) ref() string {
	if i.sourceKind() == kindGitHub {
		return strings.ToLower(i.Project) + "#" + i.number()
	}
	return strings.ToUpper(i.Key)
}

// state falls back to the Jira fields for issues cached before State existed.
func (i issue) state() string {
	if i.State != "" {
		return i.State
	}
	return jiraState(i.Status, i.StatusCat)
}

// markdown returns the description as Markdown.
func (i issue) markdown() string {
	if i.BodyFormat == bodyMarkdown {
		return i.Description
	}
	return wikiToMarkdown(i.Description)
}

// metaParts are the tracker-specific parts of the preview's meta line.
func (i issue) metaParts() []string {
	if len(i.Meta) > 0 {
		return i.Meta
	}
	var parts []string
	for _, p := range []string{i.Type, i.Priority} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if i.ParentKey != "" {
		parts = append(parts, "↳ "+i.ParentKey)
	}
	return parts
}

// fetched is what one source returns: issues from a tracker, PRs from the
// pulls source. Both are never set at once.
type fetched struct {
	issues []issue
	pulls  []pull
}

// provider fetches one source of one stack: the user's open issues from a
// tracker, or the stack's pull requests.
type provider interface {
	kind() string
	fetch(ctx context.Context) (fetched, error)
}

type source struct {
	stack string
	p     provider
}

func (s source) id() string { return sourceID(s.stack, s.p.kind()) }

func sourceID(stack, kind string) string { return stack + "/" + kind }

// sourcesOf lists every tracker of every stack, in config order, and the
// pulls source of every stack with GitHub owners after its trackers.
func sourcesOf(stacks []stack) []source {
	var out []source
	for _, s := range stacks {
		for _, kind := range s.Trackers {
			switch kind {
			case kindJira:
				out = append(out, source{s.Name, jiraProvider{s}})
			case kindGitHub:
				out = append(out, source{s.Name, githubProvider{s}})
			}
		}
		if len(s.Orgs) > 0 {
			out = append(out, source{s.Name, pullsProvider{s}})
		}
	}
	return out
}

// ---- merging ----

// mergeStacks builds the full list from per-source results, falling back to
// the cached issues of any source whose fetch failed. Within a stack, issues
// are ordered newest created first (which matches descending key numbers
// within a project); stacks keep their config order.
func mergeStacks(stacks []stack, fresh map[string][]issue, cached []issue) []issue {
	cachedBy := map[string][]issue{}
	for _, it := range cached {
		id := sourceID(it.Stack, it.sourceKind())
		cachedBy[id] = append(cachedBy[id], it)
	}
	var out []issue
	for _, s := range stacks {
		var items []issue
		for _, kind := range s.Trackers {
			id := sourceID(s.Name, kind)
			if got, ok := fresh[id]; ok {
				items = append(items, got...)
			} else {
				items = append(items, cachedBy[id]...)
			}
		}
		sort.SliceStable(items, func(i, j int) bool { return newerIssue(items[i], items[j]) })
		out = append(out, items...)
	}
	return out
}

// mergePulls is mergeStacks for the PRs: a stack whose pulls source answered
// takes the answer (an empty one clears its PRs; a merge state GitHub has
// not computed yet keeps the cached one, carryMergeable), one whose source
// failed or was not fetched keeps the cached ones.
func mergePulls(stacks []stack, fresh map[string][]pull, cached []pull) []pull {
	cachedBy := map[string][]pull{}
	for _, p := range cached {
		cachedBy[p.Stack] = append(cachedBy[p.Stack], p)
	}
	var out []pull
	for _, s := range stacks {
		if got, ok := fresh[sourceID(s.Name, kindPulls)]; ok {
			carryMergeable(got, cachedBy[s.Name])
			out = append(out, got...)
		} else {
			out = append(out, cachedBy[s.Name]...)
		}
	}
	return out
}

// newerIssue orders by creation date (newest first), falling back to key
// number when the dates tie or are missing (cache entries from older
// snapshots may lack Created until the next refresh).
func newerIssue(a, b issue) bool {
	if !a.Created.Equal(b.Created) {
		return a.Created.After(b.Created)
	}
	if a.Project == b.Project {
		na, _ := strconv.Atoi(a.number())
		nb, _ := strconv.Atoi(b.number())
		if na != nb {
			return na > nb
		}
	}
	return a.Updated.After(b.Updated)
}

// backfillParentURLs gives the Jira issues of a snapshot written before
// parent_url existed the URL their parent has on their stack's site, so an
// old cache nests the way a fresh one does.
func backfillParentURLs(issues []issue, stacks []stack) {
	base := map[string]string{}
	for _, s := range stacks {
		base[s.Name] = s.BaseURL
	}
	for i, it := range issues {
		if it.ParentKey == "" || it.ParentURL != "" || it.sourceKind() != kindJira || base[it.Stack] == "" {
			continue
		}
		issues[i].ParentURL = base[it.Stack] + "/browse/" + it.ParentKey
	}
}

// ---- bubbletea plumbing ----

type sourceMsg struct {
	source string // source.id()
	issues []issue
	pulls  []pull
	err    error
}

func fetchSourceCmd(s source) tea.Cmd {
	return func() tea.Msg {
		got, err := s.p.fetch(context.Background())
		return sourceMsg{source: s.id(), issues: got.issues, pulls: got.pulls, err: err}
	}
}

// refreshSynchronously fetches every source inline (used by -dump). Returns
// the merged issues and PRs plus one error per failed source.
func refreshSynchronously(stacks []stack, cached issueCache) ([]issue, []pull, []error) {
	freshIssues := map[string][]issue{}
	freshPulls := map[string][]pull{}
	var errs []error
	for _, s := range sourcesOf(stacks) {
		got, err := s.p.fetch(context.Background())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if s.p.kind() == kindPulls {
			freshPulls[s.id()] = got.pulls
		} else {
			freshIssues[s.id()] = got.issues
		}
	}
	return mergeStacks(stacks, freshIssues, cached.Issues), mergePulls(stacks, freshPulls, cached.Pulls), errs
}
