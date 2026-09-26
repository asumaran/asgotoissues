package main

// Disk cache, stale-while-revalidate: the popup renders instantly from the
// last saved snapshot while background fetches refresh it. The file lives in
// the herdr-injected per-plugin state dir; standalone runs (e.g. -dump
// outside herdr) use that same directory (statedir.go).

import (
	"path/filepath"
	"time"
)

// cacheFresh is how recent the cached snapshot must be to skip the background
// refresh entirely. It only debounces rapid reopen cycles; older snapshots
// still render immediately while they revalidate.
const cacheFresh = 60 * time.Second

// issueCache is the snapshot on disk. Its shape is a contract: a snapshot
// written by an older version (no pulls, issues without their newer fields)
// still loads and renders.
type issueCache struct {
	FetchedAt time.Time `json:"fetched_at"`
	Issues    []issue   `json:"issues"`
	Pulls     []pull    `json:"pulls,omitempty"`
}

func cacheFile() string {
	return filepath.Join(stateDir(), "issuecache.json")
}

func loadCache() issueCache {
	var c issueCache
	readJSONFile(cacheFile(), &c)
	return c
}

func saveCache(c issueCache) { writeJSONFile(cacheFile(), c) }

// stateDir is where asgotoissues keeps its runtime state.
func stateDir() string { return stateDirFor("asgotoissues") }
