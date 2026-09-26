package main

// The preview: an instant (non-glamour) header for the selected row, a
// ticket or a PR, and its Markdown body rendered by glamour. Rendering runs
// as a tea.Cmd and results are cached per (URL, width, updated) — the updated
// component makes stale renders unreachable after a refresh without explicit
// invalidation. The header is drawn live every frame: a PR's flags move
// without its updated date moving.

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func previewKey(url string, width int, updated time.Time) string {
	return url + "|" + strconv.Itoa(width) + "|" + strconv.FormatInt(updated.Unix(), 10)
}

// rowPreviewKey is the render a row shows.
func rowPreviewKey(r *row, width int) string {
	if r.kind == rowPull {
		return previewKey(r.p.URL, width, r.p.Updated)
	}
	return previewKey(r.e.it.URL, width, r.e.it.Updated)
}

// rowMarkdown is the body a row renders.
func rowMarkdown(r *row) string {
	if r.kind == rowPull {
		return r.p.Body
	}
	return r.e.it.markdown()
}

func renderPreviewCmd(key, md string, width int, style string) tea.Cmd {
	return func() tea.Msg {
		content, err := renderMarkdown(md, width, style)
		switch {
		case err != nil:
			content = md // raw text beats nothing
		case content == "":
			content = stDim.Render("(no description)")
		}
		return previewMsg{key: key, style: style, content: content}
	}
}

// headerOf is the header of a row's preview.
func (m *model) headerOf(r *row, width int) string {
	switch r.kind {
	case rowPull:
		return pullHeader(r.p, append([]prFlag{r.state}, r.flags...), m.summ[r.p.URL].Text, width)
	case rowIssue:
		return previewHeader(r.e.it, m.summ[r.e.it.URL].Text, width)
	}
	return m.stackHeader(r, width)
}

// stackHeader is the preview of a stack, its group of PRs or a phase: how
// many of my tickets are in each state and how many PRs in each phase (the
// list itself shows no counts).
func (m *model) stackHeader(r *row, width int) string {
	var todo, doing, blocked int
	for _, e := range m.entries {
		if e.it.Stack != r.stack || e.it.Ghost {
			continue
		}
		switch e.it.state() {
		case stateDoing:
			doing++
		case stateBlocked:
			blocked++
		default:
			todo++
		}
	}
	var review, draft, merged int
	for _, p := range m.pulls {
		if p.Stack != r.stack {
			continue
		}
		switch {
		case p.State == prMerged:
			merged++
		case p.State == prOpen && p.Draft:
			draft++
		case p.State == prOpen:
			review++
		}
	}
	title := r.stack
	if r.kind != rowHeader {
		title += " · " + r.text
	}
	lines := []string{stTitle.Render(truncate(title, width))}
	count := func(n int, what string, st lipgloss.Style) string {
		return st.Render(strconv.Itoa(n)) + stDim.Render(" "+what)
	}
	lines = append(lines,
		count(doing, "in progress", stDoing)+stDim.Render(" · ")+count(blocked, "blocked", stBlocked)+stDim.Render(" · ")+count(todo, "to do", stTodo),
		count(review, "PRs in review", stWarn)+stDim.Render(" · ")+count(draft, "draft", stDim)+stDim.Render(" · ")+count(merged, "merged", stOK))
	return strings.Join(lines, "\n")
}

// previewHeader is the instant (non-glamour) header above a ticket's body:
// the title, its short title under it, the status with the meta line and
// the parent it hangs from.
func previewHeader(it issue, short string, width int) string {
	parts := append([]string{it.Key}, it.metaParts()...)
	if !it.Created.IsZero() {
		parts = append(parts, "created "+relTime(it.Created))
	}
	parts = append(parts, "updated "+relTime(it.Updated))
	meta := strings.Join(parts, " · ")
	lines := []string{stTitle.Render(truncate(it.Summary, width))}
	if short != "" && short != it.Summary {
		lines = append(lines, stDim.Render(truncate("≈ "+short, width)))
	}
	status := stateStyle(it.state()).Render("[" + strings.ToLower(it.Status) + "]")
	lines = append(lines, status+" "+stDim.Render(truncate(meta, width-ansi.StringWidth(it.Status)-3)))
	if it.ParentKey != "" && it.ParentSummary != "" {
		lines = append(lines, stDim.Render(truncate("↳ "+it.ParentKey+" "+it.ParentSummary, width)))
	}
	if it.Ghost {
		lines = append(lines, stWarn.Render("not in your list"))
	}
	return strings.Join(lines, "\n")
}

// pullHeader is the header above a PR's body: the title, where it is
// (repo, branches, author), what it needs, and the review, checks and merge
// facts under it.
func pullHeader(p *pull, flags []prFlag, short string, width int) string {
	where := p.Key + " · " + p.Head
	if p.Base != "" {
		where += " → " + p.Base
	}
	if p.Author != "" {
		where += " · by " + p.Author
	}
	lines := []string{stTitle.Render(truncate(p.Title, width))}
	if short != "" && short != p.Title {
		lines = append(lines, stDim.Render(truncate("≈ "+short, width)))
	}
	lines = append(lines, stDim.Render(truncate(where, width)), truncate(flagsLine(flags, lipgloss.NewStyle()), width))
	facts := []string{"review " + reviewFact(p)}
	if p.Checks != "" {
		facts = append(facts, "checks "+strings.ToLower(p.Checks))
	}
	if p.MergeState != "" && p.MergeState != "UNKNOWN" {
		facts = append(facts, "merge "+strings.ToLower(p.MergeState))
	}
	facts = append(facts, "created "+relTime(p.Created), "updated "+relTime(p.Updated))
	lines = append(lines, stDim.Render(truncate(strings.Join(facts, " · "), width)))
	return strings.Join(lines, "\n")
}

// reviewFact is the review state in words.
func reviewFact(p *pull) string {
	var parts []string
	switch p.ReviewDecision {
	case "APPROVED":
		parts = append(parts, "approved")
	case "CHANGES_REQUESTED":
		parts = append(parts, "changes requested")
	case "REVIEW_REQUIRED":
		parts = append(parts, "required")
	default:
		parts = append(parts, "no rule")
	}
	if p.Approvals > 0 {
		parts = append(parts, plural(p.Approvals, "approval"))
	}
	if p.ReviewRequests > 0 {
		parts = append(parts, plural(p.ReviewRequests, "reviewer")+" pending")
	}
	return strings.Join(parts, ", ")
}
