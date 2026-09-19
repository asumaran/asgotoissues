package main

// With a query in the filter the list is a search result, not the full list
// with rows taken out: the best match goes first and the cursor sits on it.
// Without a query every tool keeps its own order (newest first, config
// order, the order of the diff).
//
// This file is the same in every tool of the family that ranks its matches.

import "sort"

// rank orders items best score first. In a grouped list the groups are
// ordered by their best item and keep their items together; a flat list
// passes a nil group. Equal scores keep the order the items came in, which is
// the list's own.
func rank[T any](items []T, score func(T) int, group func(T) string) []T {
	sort.SliceStable(items, func(i, j int) bool { return score(items[i]) > score(items[j]) })
	if group == nil {
		return items
	}
	var order []string
	byGroup := map[string][]T{}
	for _, it := range items {
		g := group(it)
		if _, seen := byGroup[g]; !seen {
			order = append(order, g)
		}
		byGroup[g] = append(byGroup[g], it)
	}
	out := items[:0:0]
	for _, g := range order {
		out = append(out, byGroup[g]...)
	}
	return out
}
