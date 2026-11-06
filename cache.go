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
	FetchedAt time.Time            `json:"fetched_at"`
	Issues    []issue              `json:"issues"`
	Pulls     []pull               `json:"pulls,omitempty"`
	Sources   map[string]time.Time `json:"sources,omitempty"` // the last complete fetch of each source with its own interval, by source id
}

// stamp records that a source answered now. Only the sources with their own
// interval are stamped: the others follow FetchedAt.
func (c *issueCache) stamp(id string, at time.Time) {
	if c.Sources == nil {
		c.Sources = map[string]time.Time{}
	}
	c.Sources[id] = at
}

// due splits the sources into the ones to fetch now and the ones whose own
// interval has not run out (their cached issues stand). A source without an
// interval is always due; one with it is due when it was never stamped, when
// its stamp is older than the interval, or when force says so.
func (c issueCache) due(sources []source, now time.Time, force bool) (due, skipped []source) {
	for _, s := range sources {
		if s.every > 0 && !force {
			if at, ok := c.Sources[s.id()]; ok && now.Sub(at) < s.every {
				skipped = append(skipped, s)
				continue
			}
		}
		due = append(due, s)
	}
	return due, skipped
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
