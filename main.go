// asgotoissues: a herdr plugin popup that lists your open issues (Jira
// tickets and GitHub issues, across every stack configured in
// ~/.claude/asdev.local.md), grouped by stack, with fuzzy search and a
// rendered preview of the description. Selecting an issue opens it in the
// browser.
//
// Data comes straight from the trackers (the Jira REST API with netrc / token
// auth, GitHub through the gh CLI), cached stale-while-revalidate so the
// popup renders instantly.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// version is the release tag; overridden at build time via
// -ldflags "-X main.version=vX.Y.Z" (see scripts/release.sh and CI).
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the embedded version")
	dump := flag.Bool("dump", false, "print configured stacks and issues (no TUI)")
	query := flag.String("query", "", "with -dump: print the matches and their scores instead of the list")
	show := flag.String("show", "", "with -dump: print this issue's description as Markdown")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: asgotoissues [flags]")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	stacks, err := loadStacks()
	if err != nil {
		fatal("asgotoissues", err.Error())
	}
	cache := loadCache()
	stale := time.Since(cache.FetchedAt) >= cacheFresh

	if *dump {
		runDump(os.Stdout, stacks, cache, stale, *query, *show)
		return
	}

	// Alt screen and mouse mode are declared per frame by View().
	res, err := tea.NewProgram(newModel(stacks, cache, stale)).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "asgotoissues:", err)
		os.Exit(1)
	}
	if url := res.(model).openURL; url != "" {
		if err := openURL("asgotoissues", url); err != nil {
			fmt.Fprintf(os.Stderr, "asgotoissues: open %s: %v\n", url, err)
			os.Exit(1)
		}
	}
}

// runDump prints the state without a TUI: stacks, grouped tickets and (with
// -query) filter scores. It refreshes synchronously when the cache is stale,
// so it exercises the same fetch path the TUI uses in the background.
func runDump(w io.Writer, stacks []stack, cache issueCache, stale bool, query, show string) {
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
	fmt.Fprintf(w, "config: %s\n", configPath())
	fmt.Fprintf(w, "cache: %d issues, fetched %s (%s)\n", len(cache.Issues), relTime(cache.FetchedAt), cacheFile())
	fmt.Fprintf(w, "stacks: %d\n", len(stacks))
	for _, s := range stacks {
		for _, kind := range s.Trackers {
			where := s.BaseURL + " (" + s.Type + ")"
			if kind == kindGitHub {
				where = strings.Join(s.Orgs, ", ")
			}
			fmt.Fprintf(w, "  %-16s %-6s %s\n", s.Name, kind, where)
		}
	}

	entries := buildEntries(cache.Issues)
	if query != "" {
		q := query
		summaries, keys, metas := corpora(entries)
		fmt.Fprintf(w, "query %q:\n", query)
		for _, r := range buildRows(entries, q, summaries, keys, metas) {
			if r.kind != "issue" {
				continue
			}
			fmt.Fprintf(w, "  %5d  %s %s\n", r.score, r.e.it.Key, truncate(r.e.it.Summary, 60))
		}
		return
	}
	last := ""
	for _, e := range entries {
		if e.it.Stack != last {
			fmt.Fprintf(w, "%s\n", e.it.Stack)
			last = e.it.Stack
		}
		label := e.it.Type
		if label == "" {
			label = e.it.sourceKind()
		}
		fmt.Fprintf(w, "  %-12s [%s] %s: %s (updated %s, desc %dB)\n",
			e.it.Key, e.it.Status, label, truncate(e.it.Summary, 60), relTime(e.it.Updated), len(e.it.Description))
	}

	if show != "" {
		for _, it := range cache.Issues {
			if strings.EqualFold(it.Key, show) {
				fmt.Fprintf(w, "---- %s (as markdown) ----\n%s\n", it.Key, it.markdown())
			}
		}
	}
}
