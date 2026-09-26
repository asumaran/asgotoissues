// asgotoissues: a herdr plugin popup that lists your open issues (Jira
// tickets and GitHub issues, across every stack configured in
// ~/.claude/asdev.local.md), grouped by stack as a tree (a sub-task under
// its story, the story under its epic) with the pull requests linked to each
// ticket and what they need from you, with fuzzy search and a rendered
// preview of the description. Selecting a row opens it in the browser.
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
	order := flag.String("order", "", "with -dump: the order of every level (created, updated, key, attention); the saved setting otherwise")
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
		runDump(os.Stdout, stacks, cache, stale, *query, *show, *order)
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

// runDump prints the state without a TUI: stacks, the tree of tickets (with
// what their PRs need) and their PRs, and (with -query) filter scores. It refreshes synchronously when
// the cache is stale, so it exercises the same fetch path the TUI uses in
// the background. order, when given, beats the saved setting and is not
// saved.
func runDump(w io.Writer, stacks []stack, cache issueCache, stale bool, query, show, order string) {
	if stale {
		merged, pulls, errs := refreshSynchronously(stacks, cache)
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "refresh failed:", e)
		}
		fetchedAt := cache.FetchedAt
		if len(errs) == 0 {
			fetchedAt = time.Now()
		}
		cache = issueCache{FetchedAt: fetchedAt, Issues: merged, Pulls: pulls}
		saveCache(cache)
	}
	fmt.Fprintf(w, "config: %s\n", configPath())
	fmt.Fprintf(w, "cache: %d issues, %d PRs, fetched %s (%s)\n", len(cache.Issues), len(cache.Pulls), relTime(cache.FetchedAt), cacheFile())
	fmt.Fprintf(w, "stacks: %d\n", len(stacks))
	for _, s := range sourcesOf(stacks) {
		st := stackNamed(stacks, s.stack)
		where := st.BaseURL + " (" + st.Type + ")"
		if s.p.kind() != kindJira {
			where = strings.Join(st.Orgs, ", ")
		}
		fmt.Fprintf(w, "  %-16s %-6s %s\n", s.stack, s.p.kind(), where)
	}

	if order == "" {
		order = loadSetting(stateDir(), "order")
	}
	backfillParentURLs(cache.Issues, stacks)
	entries := buildEntries(cache.Issues)
	pulls := cache.Pulls
	unlinked := linkPulls(entries, pulls)
	summaries, keys, metas := corpora(entries)
	if query != "" {
		fmt.Fprintf(w, "query %q:\n", query)
		for _, r := range buildRows(entries, pulls, unlinked, parseOrder(order), prsAll, false, nil, query, summaries, keys, metas) {
			if r.kind != rowIssue || !r.match {
				continue
			}
			fmt.Fprintf(w, "  %5d  %s %s\n", r.score, r.e.it.Key, truncate(r.e.it.Summary, 60))
		}
		return
	}
	fmt.Fprintf(w, "order: %s\n", parseOrder(order))
	for _, r := range buildRows(entries, pulls, unlinked, parseOrder(order), prsAll, false, nil, "", summaries, keys, metas) {
		indent := strings.Repeat("  ", r.depth)
		switch r.kind {
		case rowHeader:
			fmt.Fprintf(w, "%s\n", r.stack)
		case rowGroup:
			fmt.Fprintf(w, "  %s\n", r.text)
		case rowIssue:
			it := r.e.it
			label := it.Type
			if label == "" {
				label = it.sourceKind()
			}
			ghost := ""
			if it.Ghost {
				ghost = " (not in your list)"
			}
			var needs []string
			for _, f := range r.needs {
				needs = append(needs, f.text)
			}
			if r.merged {
				needs = append([]string{"all merged"}, needs...)
			}
			if len(needs) > 0 {
				ghost += " · " + strings.Join(needs, " · ")
			}
			fmt.Fprintf(w, "  %s%s [%s] %s: %s (updated %s, desc %dB)%s\n",
				indent, it.Key, it.Status, label, truncate(it.Summary, 60), relTime(it.Updated), len(it.Description), ghost)
		case rowPull:
			var flags []string
			for _, f := range r.flags {
				flags = append(flags, f.text)
			}
			needs := ""
			if len(flags) > 0 {
				needs = " " + strings.Join(flags, " · ")
			}
			fmt.Fprintf(w, "  %s↳ %s [%s]%s: %s\n", indent, r.p.Key, strings.ToLower(r.p.State), needs, truncate(r.p.Title, 60))
		}
	}

	if show != "" {
		for _, it := range cache.Issues {
			if strings.EqualFold(it.Key, show) {
				fmt.Fprintf(w, "---- %s (as markdown) ----\n%s\n", it.Key, it.markdown())
			}
		}
	}
}

// stackNamed is the stack called name.
func stackNamed(stacks []stack, name string) stack {
	for _, s := range stacks {
		if s.Name == name {
			return s
		}
	}
	return stack{}
}
