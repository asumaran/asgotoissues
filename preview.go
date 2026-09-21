package main

// Ticket preview: the wiki-markup description is converted to Markdown and
// rendered by glamour to ANSI for the right-hand column. Rendering runs as a
// tea.Cmd and results are cached per (key, width, updated) — the updated
// component makes stale renders unreachable after a refresh without explicit
// invalidation.

import (
	"github.com/charmbracelet/x/ansi"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type previewMsg struct {
	key     string
	style   string // glamour style the render used; dropped if it changed since
	content string
}

func previewKey(it issue, width int) string {
	return it.URL + "|" + strconv.Itoa(width) + "|" + strconv.FormatInt(it.Updated.Unix(), 10)
}

func renderPreviewCmd(it issue, width int, style string) tea.Cmd {
	key := previewKey(it, width)
	return func() tea.Msg {
		md := it.markdown()
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

// previewHeader is the instant (non-glamour) header above the rendered body.
func previewHeader(it issue, width int) string {
	parts := append([]string{it.Key}, it.metaParts()...)
	if !it.Created.IsZero() {
		parts = append(parts, "created "+relTime(it.Created))
	}
	parts = append(parts, "updated "+relTime(it.Updated))
	meta := strings.Join(parts, " · ")
	title := truncate(it.Summary, width)
	status := stateStyle(it.state()).Render("[" + it.Status + "]")
	return stTitle.Render(title) + "\n" + status + " " + stDim.Render(truncate(meta, width-ansi.StringWidth(it.Status)-3))
}
