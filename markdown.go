package main

// Markdown in the preview. This file is the same in every tool of the family
// that renders Markdown.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
)

// renderMarkdown renders body for width cells with a fixed glamour standard
// style ("dark" or "light"); an empty body renders as nothing. The style is
// never auto-detected here: bubbletea owns the terminal, so the model asks it
// for the background color (Init → RequestBackgroundColor) and passes the
// answer down. glamour's WithAutoStyle would query the terminal itself, and
// that reply races bubbletea's input reader and ends up typed into the filter
// as literal "rgb:..." text. The blank lines glamour puts around the document
// are trimmed: spacing is the caller's.
func renderMarkdown(body string, width int, style string) (string, error) {
	if strings.TrimSpace(body) == "" {
		return "", nil
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
	return strings.Trim(out, "\n"), nil
}

// glamourStyle is the standard style for the background the terminal reported.
func glamourStyle(msg tea.BackgroundColorMsg) string {
	if msg.IsDark() {
		return "dark"
	}
	return "light"
}

// setPreviewStyle switches the glamour style once the terminal background is
// known. Cached renders carry the old palette, so they are dropped and the
// current preview is rendered again.
func (m *model) setPreviewStyle(style string) tea.Cmd {
	if style == m.previewStyle {
		return nil
	}
	m.previewStyle = style
	m.renders = map[string]string{}
	m.prevKey = ""
	return m.updatePreview()
}
