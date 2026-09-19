package main

// Ticket preview: the wiki-markup description is converted to Markdown and
// rendered by glamour to ANSI for the right-hand column. Rendering runs as a
// tea.Cmd and results are cached per (key, width, updated) — the updated
// component makes stale renders unreachable after a refresh without explicit
// invalidation.

import (
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
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
		if err != nil {
			content = md // raw text beats nothing
		}
		return previewMsg{key: key, style: style, content: content}
	}
}

// renderMarkdown renders with a fixed glamour standard style ("dark" or
// "light"). The style is never auto-detected here: bubbletea owns the
// terminal, so the model asks it for the background color (Init →
// RequestBackgroundColor) and passes the answer down. glamour's WithAutoStyle
// would query the terminal itself, and that reply races bubbletea's input
// reader and ends up typed into the filter as literal "rgb:..." text.
func renderMarkdown(body string, width int, style string) (string, error) {
	if strings.TrimSpace(body) == "" {
		return stDim.Render("(no description)"), nil
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(style),
		glamour.WithWordWrap(width),
		glamour.WithEmoji(),
	)
	if err != nil {
		return "", err
	}
	out, err := r.Render(body)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(out, "\n"), nil
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

// relTime formats a timestamp as a compact "2h ago" style age.
func relTime(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
