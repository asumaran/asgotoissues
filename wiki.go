package main

// Jira wiki markup → Markdown, good enough for a read-only preview. Covers
// what shows up in real ticket descriptions: headings, bold/italic/mono,
// bullet and numbered lists (nested), {code}/{noformat}/{quote} blocks,
// links, rules, and Jira's smart-link/mention noise. Anything unknown passes
// through as plain text; the goal is a readable preview, not fidelity.

import (
	"regexp"
	"strings"
)

var (
	wikiHeadingRe = regexp.MustCompile(`^h([1-6])\.\s*(.*)$`)
	wikiListRe    = regexp.MustCompile(`^([*#\-]+)\s+(.*)$`)
	wikiCodeOpen  = regexp.MustCompile(`^\{code(?::([^}]*))?\}(.*)$`)
	wikiLinkRe    = regexp.MustCompile(`\[([^\]|]*)\|([^\]|]+)(?:\|[^\]]*)?\]`)
	wikiBareLink  = regexp.MustCompile(`\[(https?://[^\]\s]+)\]`)
	wikiMonoRe    = regexp.MustCompile(`\{\{(.+?)\}\}`)
	wikiBoldRe    = regexp.MustCompile(`(^|[\s(])\*([^*\n]+?)\*($|[\s).,;:!?])`)
	wikiItalicRe  = regexp.MustCompile(`(^|[\s(])_([^_\n]+?)_($|[\s).,;:!?])`)
	wikiStrikeRe  = regexp.MustCompile(`(^|[\s(])-([^-\n]+?)-($|[\s).,;:!?])`)
	wikiColorRe   = regexp.MustCompile(`\{color(?::[^}]*)?\}`)
	wikiAnchorRe  = regexp.MustCompile(`\{anchor:[^}]*\}`)
	wikiMentionRe = regexp.MustCompile(`\[~(?:accountid:)?([^\]]+)\]`)
	wikiSmartRe   = regexp.MustCompile(`\|smart-link\]`)
	wikiCellSplit = regexp.MustCompile(`\|\|?`)
)

// wikiToMarkdown converts a Jira wiki-markup document to Markdown.
func wikiToMarkdown(src string) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(src, "\n")
	var out []string
	inCode, inNoformat, inQuote := false, false, false
	inTable := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// fenced blocks first: their content is verbatim
		if inCode {
			if strings.Contains(trimmed, "{code}") {
				before := strings.TrimSpace(strings.SplitN(line, "{code}", 2)[0])
				if before != "" {
					out = append(out, before)
				}
				out = append(out, "```")
				inCode = false
				continue
			}
			out = append(out, line)
			continue
		}
		if inNoformat {
			if strings.Contains(trimmed, "{noformat}") {
				out = append(out, "```")
				inNoformat = false
				continue
			}
			out = append(out, line)
			continue
		}
		if m := wikiCodeOpen.FindStringSubmatch(trimmed); m != nil {
			lang := strings.TrimSpace(strings.SplitN(m[1], "|", 2)[0])
			lang = strings.TrimPrefix(lang, "lang=")
			rest := m[2]
			if strings.Contains(rest, "{code}") { // single-line {code}x{code}
				body := strings.TrimSpace(strings.SplitN(rest, "{code}", 2)[0])
				out = append(out, "```"+lang, body, "```")
				continue
			}
			out = append(out, "```"+lang)
			if strings.TrimSpace(rest) != "" {
				out = append(out, rest)
			}
			inCode = true
			continue
		}
		if strings.HasPrefix(trimmed, "{noformat}") {
			rest := strings.TrimPrefix(trimmed, "{noformat}")
			if strings.Contains(rest, "{noformat}") {
				body := strings.TrimSpace(strings.SplitN(rest, "{noformat}", 2)[0])
				out = append(out, "```", body, "```")
				continue
			}
			out = append(out, "```")
			if strings.TrimSpace(rest) != "" {
				out = append(out, rest)
			}
			inNoformat = true
			continue
		}
		if trimmed == "{quote}" {
			inQuote = !inQuote
			continue
		}
		if strings.HasPrefix(trimmed, "{panel") || trimmed == "{panel}" {
			continue // panels become plain flow
		}

		// tables: || header || / | cell |
		if strings.HasPrefix(trimmed, "||") || (strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") && len(trimmed) > 1) {
			header := strings.HasPrefix(trimmed, "||")
			cells := splitCells(trimmed)
			row := "| " + strings.Join(cells, " | ") + " |"
			if header || !inTable {
				out = append(out, inline(row))
				out = append(out, "|"+strings.Repeat(" --- |", len(cells)))
			} else {
				out = append(out, inline(row))
			}
			inTable = true
			continue
		}
		inTable = false

		switch {
		case trimmed == "":
			out = append(out, "")
		case strings.HasPrefix(trimmed, "----"):
			out = append(out, "---")
		default:
			var conv string
			if m := wikiHeadingRe.FindStringSubmatch(trimmed); m != nil {
				conv = strings.Repeat("#", int(m[1][0]-'0')) + " " + inline(m[2])
			} else if m := wikiListRe.FindStringSubmatch(trimmed); m != nil {
				marks := m[1]
				indent := strings.Repeat("  ", len(marks)-1)
				bullet := "-"
				if marks[len(marks)-1] == '#' {
					bullet = "1."
				}
				conv = indent + bullet + " " + inline(m[2])
			} else if strings.HasPrefix(trimmed, "bq. ") {
				conv = "> " + inline(strings.TrimPrefix(trimmed, "bq. "))
			} else {
				conv = inline(line)
			}
			if inQuote {
				conv = "> " + conv
			}
			out = append(out, conv)
		}
	}
	if inCode || inNoformat {
		out = append(out, "```")
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func splitCells(row string) []string {
	row = strings.Trim(row, "|")
	parts := wikiCellSplit.Split(row, -1)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// inline converts span-level markup within one line.
func inline(s string) string {
	s = wikiSmartRe.ReplaceAllString(s, "]")
	s = wikiColorRe.ReplaceAllString(s, "")
	s = wikiAnchorRe.ReplaceAllString(s, "")
	s = wikiMentionRe.ReplaceAllString(s, "@$1")
	s = wikiMonoRe.ReplaceAllString(s, "`$1`")
	s = wikiLinkRe.ReplaceAllString(s, "[$1]($2)")
	s = wikiBareLink.ReplaceAllString(s, "<$1>")
	s = wikiBoldRe.ReplaceAllString(s, "$1**$2**$3")
	s = wikiItalicRe.ReplaceAllString(s, "$1*$2*$3")
	s = wikiStrikeRe.ReplaceAllString(s, "$1~~$2~~$3")
	return s
}
