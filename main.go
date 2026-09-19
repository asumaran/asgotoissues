// gotojira: a herdr plugin popup that lists the Jira tickets assigned to you
// (open ones, across every stack configured in ~/.claude/asdev.local.md),
// grouped by stack, with fuzzy search and a rendered preview of the
// description. Selecting a ticket opens it in the browser.
//
// Data comes straight from the Jira REST API (netrc / token auth), cached
// stale-while-revalidate so the popup renders instantly.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

// version is the release tag; overridden at build time via
// -ldflags "-X main.version=vX.Y.Z" (see scripts/release.sh and CI).
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the embedded version")
	dump := flag.Bool("dump", false, "print configured stacks and tickets (no TUI)")
	query := flag.String("query", "", "with -dump: print filter scores for this query")
	show := flag.String("show", "", "with -dump: print the Markdown conversion of this ticket's description")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	stacks, err := loadStacks()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gotojira:", err)
		os.Exit(1)
	}
	cache := loadCache()
	stale := time.Since(cache.FetchedAt) >= cacheFresh

	if *dump {
		runDump(stacks, cache, stale, *query, *show)
		return
	}

	m := model{
		stacks:       stacks,
		cache:        cache,
		refreshing:   stale,
		pending:      len(stacks),
		ti:           newFilterInput(promptText()),
		listVP:       viewport.New(viewport.WithWidth(50), viewport.WithHeight(20)),
		prevVP:       viewport.New(viewport.WithWidth(40), viewport.WithHeight(17)),
		help:         help.New(),
		keys:         defaultKeys(),
		renders:      map[string]string{},
		previewStyle: "dark",
		width:        94,
		height:       24,
	}
	if !stale {
		m.pending = 0
	}
	m.setEntries(cache.Issues)
	m.applyFilter()
	m.resize()
	m.renderList()

	// The alt screen is declared per frame by View().
	res, err := tea.NewProgram(m).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	openInBrowser(res.(model).openURL)
}

// runDump prints the state without a TUI: stacks, grouped tickets and (with
// -query) filter scores. It refreshes synchronously when the cache is stale,
// so it exercises the same fetch path the TUI uses in the background.
func runDump(stacks []stack, cache issueCache, stale bool, query, show string) {
	if stale {
		merged, errs := refreshSynchronously(stacks, cache.Issues)
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "refresh failed:", e)
		}
		fetchedAt := cache.FetchedAt
		if len(errs) == 0 {
			fetchedAt = time.Now()
		}
		cache = issueCache{FetchedAt: fetchedAt, Issues: merged}
		saveCache(cache)
	}
	fmt.Printf("config: %s\n", configPath())
	fmt.Printf("cache: %d tickets, fetched %s (%s)\n", len(cache.Issues), relTime(cache.FetchedAt), cacheFile())
	fmt.Printf("stacks: %d\n", len(stacks))
	for _, s := range stacks {
		fmt.Printf("  %-16s %s (%s)\n", s.Name, s.BaseURL, s.Type)
	}

	entries := buildEntries(cache.Issues)
	last := ""
	for _, e := range entries {
		if e.it.Stack != last {
			fmt.Printf("%s\n", e.it.Stack)
			last = e.it.Stack
		}
		fmt.Printf("  %-12s [%s] %s: %s (updated %s, desc %dB)\n",
			e.it.Key, e.it.Status, e.it.Type, truncate(e.it.Summary, 60), relTime(e.it.Updated), len(e.it.Description))
	}

	if show != "" {
		for _, it := range cache.Issues {
			if strings.EqualFold(it.Key, show) {
				fmt.Printf("---- %s (wiki → markdown) ----\n%s\n", it.Key, wikiToMarkdown(it.Description))
			}
		}
	}

	if query != "" {
		q := strings.ToLower(query)
		summaries, keys, metas := corpora(entries)
		fmt.Printf("query %q:\n", query)
		for _, r := range buildRows(entries, q, summaries, keys, metas) {
			if r.kind != "issue" {
				continue
			}
			fmt.Printf("  %5d  %s %s\n", r.score, r.e.it.Key, truncate(r.e.it.Summary, 60))
		}
	}
}
