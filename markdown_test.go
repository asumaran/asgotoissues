package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderMarkdown(t *testing.T) {
	got, err := renderMarkdown("# Plan\n\n* first\n* second with `code`\n", 40, "dark")
	if err != nil {
		t.Fatal(err)
	}
	plain := ansi.Strip(got)
	if !strings.Contains(plain, "Plan") || !strings.Contains(plain, "first") || !strings.Contains(plain, "code") {
		t.Errorf("rendered = %q", plain)
	}
	if strings.HasPrefix(got, "\n") || strings.HasSuffix(got, "\n") {
		t.Errorf("the blank lines around the document are the caller's business: %q", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(l); w > 40+4 { // glamour's own margin
			t.Errorf("line is %d cells wide: %q", w, ansi.Strip(l))
		}
	}
	for _, body := range []string{"", "  \n\t"} {
		if got, err := renderMarkdown(body, 40, "light"); got != "" || err != nil {
			t.Errorf("an empty body renders as nothing: %q, %v", got, err)
		}
	}
	if _, err := renderMarkdown("text", 40, "no-such-style"); err == nil {
		t.Errorf("an unknown style is an error, not a silent default")
	}
}
