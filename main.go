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
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// summarizeAll writes every missing short title, one call after another,
// and says how many it wrote.
func summarizeAll(m *model) (int, error) {
	n := 0
	for {
		var batch []summaryItem
		for _, b := range pendingSummaries(m.entries, m.summ) {
			fresh := false
			for _, it := range b {
				if it.Summary == "" && !m.summTried[it.ID] {
					fresh = true
				}
			}
			if fresh {
				batch = b
				break
			}
		}
		if batch == nil {
			return n, nil
		}
		for _, it := range batch {
			m.summTried[it.ID] = true
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		got, err := summarize(ctx, batch)
		cancel()
		if err != nil {
			return n, err
		}
		for k, v := range got {
			m.summ[k] = v
		}
		settleHashes(m.entries, m.summ, got)
		saveSummaries(m.summ)
		n += len(got)
		m.summaries, m.keysC, m.metas = corpora(m.entries, m.titleOf)
	}
}

// version is the release tag; overridden at build time via
// -ldflags "-X main.version=vX.Y.Z" (see scripts/release.sh and CI).
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the embedded version")
	dump := flag.Bool("dump", false, "print configured stacks and issues (no TUI)")
	query := flag.String("query", "", "with -dump: print the matches and their scores instead of the list")
	show := flag.String("show", "", "with -dump: print this issue's description as Markdown")
	order := flag.String("order", "", "with -dump: the order of every level (created, updated, key, attention); the saved setting otherwise")
	summarizeNow := flag.Bool("summarize", false, "with -dump: write the missing short titles first (runs the summarizer)")
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
		runDump(os.Stdout, stacks, cache, stale, *query, *show, *order, *summarizeNow)
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
func runDump(w io.Writer, stacks []stack, cache issueCache, stale bool, query, show, order string, summarizeNow bool) {
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
	m := newModel(stacks, cache, false)
	m.order, m.show, m.group, m.rowsM, m.prs = parseOrder(order), showAll, groupTree, rowsTwo, prsAll
	if summarizeNow {
		n, err := summarizeAll(&m)
		fmt.Fprintf(w, "short titles: %d written\n", n)
		if err != nil {
			fmt.Fprintln(os.Stderr, "short titles:", err)
		}
	}
	if query != "" {
		fmt.Fprintf(w, "query %q:\n", query)
		for _, r := range buildRows(m.entries, m.pulls, m.unlinked, m.opts(), query, m.summaries, m.keysC, m.metas) {
			if r.kind != rowIssue || !r.match {
				continue
			}
			fmt.Fprintf(w, "  %5d  %s %s\n", r.score, r.e.it.Key, truncate(r.e.it.Summary, 60))
		}
		return
	}
	var mine []pull
	for _, p := range m.pulls {
		if p.Mine && p.State == prOpen {
			mine = append(mine, p)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	m.local = localStates(ctx, localRoots(), mine)
	cancel()
	fmt.Fprintf(w, "order: %s\n", m.order)
	m.cursor = -1
	m.applyFilter()
	m.cursor = -1
	for _, r := range m.rows {
		for _, l := range m.rowLines(r, false, 400) {
			fmt.Fprintln(w, strings.TrimRight(ansi.Strip(l), " "))
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
