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
func headerOf(r *row, width int) string {
	if r.kind == rowPull {
		return pullHeader(r.p, r.flags, width)
	}
	return previewHeader(r.e.it, r.merged, width)
}

// previewHeader is the instant (non-glamour) header above a ticket's body:
// the title, the status with the meta line, the parent it hangs from, and
// what it needs.
func previewHeader(it issue, merged bool, width int) string {
	parts := append([]string{it.Key}, it.metaParts()...)
	if !it.Created.IsZero() {
		parts = append(parts, "created "+relTime(it.Created))
	}
	parts = append(parts, "updated "+relTime(it.Updated))
	meta := strings.Join(parts, " · ")
	title := truncate(it.Summary, width)
	status := stateStyle(it.state()).Render("[" + it.Status + "]")
	lines := []string{stTitle.Render(title), status + " " + stDim.Render(truncate(meta, width-ansi.StringWidth(it.Status)-3))}
	if it.ParentKey != "" && it.ParentSummary != "" {
		lines = append(lines, stDim.Render(truncate("↳ "+it.ParentKey+" "+it.ParentSummary, width)))
	}
	switch {
	case it.Ghost:
		lines = append(lines, stWarn.Render("not in your list"))
	case merged:
		lines = append(lines, stOK.Render("✓ all PRs merged"))
	}
	return strings.Join(lines, "\n")
}

// pullHeader is the header above a PR's body: the title, where it is
// (repo, branches, author), what it needs, and the review, checks and merge
// facts under it.
func pullHeader(p *pull, flags []prFlag, width int) string {
	where := p.Key + " · " + p.Head
	if p.Base != "" {
		where += " → " + p.Base
	}
	if p.Author != "" {
		where += " · by " + p.Author
	}
	lines := []string{stTitle.Render(truncate(p.Title, width)), stDim.Render(truncate(where, width))}
	state := stateOfPull(p)
	if fl := flagsLine(flags, lipgloss.NewStyle()); fl != "" {
		state += " " + fl
	}
	lines = append(lines, truncate(state, width))
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

// stateOfPull is the PR's state as a colored bracket, like a ticket's status.
func stateOfPull(p *pull) string {
	switch {
	case p.State == prMerged:
		return stOK.Render("[merged]")
	case p.State == prClosed:
		return stDim.Render("[closed]")
	case p.Draft:
		return stDim.Render("[draft]")
	}
	return stDoing.Render("[open]")
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
