package main

// Folds saved between runs: which rows are folded by hand (a stack, a
// ticket, the group of PRs without a ticket, a phase section) and the level
// tab/shift+tab left the tree at, so a restart shows the tree the way it was
// left instead of fully unfolded. One file, folds.json, written atomically
// (jsonfile.go): {"level": n, "keys": [...]}, the keys sorted so the file is
// deterministic.
//
// It is a snapshot, not a depth policy: a ticket that arrives after a
// restart (new, or unfolded by a live refresh) shows unfolded, the same as
// it does today after a refresh while the popup is open. pruneFolds keeps a
// restart from wedging the tree closed on rows that no longer exist: a
// structural key (stack, group, section) is kept only when it is still
// built from the configured stacks, and a ticket's URL only when some entry,
// ghost included, still holds it. Renaming a stack or a phase drops its
// fold; that is accepted. Only a complete refresh (finishRefresh, no
// fetchErrs) calls it: a cache read or a partial refresh trusts nothing
// enough to prune.

import (
	"path/filepath"
	"sort"
)

const foldsFile = "folds.json"

// foldsSnapshot is folds.json's shape.
type foldsSnapshot struct {
	Level int      `json:"level"`
	Keys  []string `json:"keys"`
}

// loadFolds reads the saved folds: none when the file is missing, does not
// parse, or its level is negative.
func loadFolds(dir string) (map[string]bool, int) {
	s := foldsSnapshot{Level: -1}
	readJSONFile(filepath.Join(dir, foldsFile), &s)
	if s.Level < 0 {
		return map[string]bool{}, 0
	}
	collapsed := make(map[string]bool, len(s.Keys))
	for _, k := range s.Keys {
		collapsed[k] = true
	}
	return collapsed, s.Level
}

// saveFolds writes the folds, keys sorted.
func saveFolds(dir string, collapsed map[string]bool, level int) {
	keys := make([]string, 0, len(collapsed))
	for k := range collapsed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	writeJSONFile(filepath.Join(dir, foldsFile), foldsSnapshot{Level: level, Keys: keys})
}

// pruneFolds drops a fold key whose row no longer exists, then saves.
// Structural keys (a stack, its group of PRs without a ticket, one of its
// phase sections) are valid only when built from the stacks configured now;
// a ticket's URL only when some entry (a ghost included) still holds it.
// Nothing is split on ':': a key is checked against the whole valid set, not
// parsed apart.
func (m *model) pruneFolds() {
	valid := map[string]bool{}
	for _, s := range m.stacks {
		valid["stack:"+s.Name] = true
		valid["group:"+s.Name] = true
		for _, ph := range phases {
			valid["section:"+s.Name+":"+ph] = true
		}
	}
	for _, e := range m.entries {
		if e.it.URL != "" {
			valid[e.it.URL] = true
		}
	}
	for k := range m.collapsed {
		if !valid[k] {
			delete(m.collapsed, k)
		}
	}
	saveFolds(stateDir(), m.collapsed, m.depthNow)
}
