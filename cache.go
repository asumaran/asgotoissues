package main

// Disk cache, stale-while-revalidate: the popup renders instantly from the
// last saved snapshot while background fetches refresh it. The file lives in
// the herdr-injected per-plugin state dir; standalone runs (e.g. -dump
// outside herdr) fall back to a fixed path under ~/.config/herdr.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

func stateDir() string {
	if dir := os.Getenv("HERDR_PLUGIN_STATE_DIR"); dir != "" {
		return dir
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		if h, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(h, ".config")
		}
	}
	return filepath.Join(base, "herdr", "gotojira-tui")
}

// cacheFresh is how recent the cached snapshot must be to skip the background
// refresh entirely. It only debounces rapid reopen cycles; older snapshots
// still render immediately while they revalidate.
const cacheFresh = 60 * time.Second

type issueCache struct {
	FetchedAt time.Time `json:"fetched_at"`
	Issues    []issue   `json:"issues"`
}

func cacheFile() string {
	return filepath.Join(stateDir(), "issuecache.json")
}

func loadCache() issueCache {
	var c issueCache
	if data, err := os.ReadFile(cacheFile()); err == nil {
		_ = json.Unmarshal(data, &c)
	}
	return c
}

func saveCache(c issueCache) {
	if data, err := json.Marshal(c); err == nil {
		path := cacheFile()
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, data, 0o644)
	}
}
