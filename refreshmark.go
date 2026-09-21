package main

// What the edge over the input says about a list that is fetched in the
// background and shown from a cache meanwhile.
//
// This file is the same in every tool of the family that needs it.

// refreshMark is "refreshing…" while the fetch runs and, after one that
// failed, a standing "refresh failed": the list is the cached one. The error
// itself takes the help line until the next key.
func refreshMark(refreshing, stale bool) string {
	switch {
	case refreshing:
		return stDim.Render("refreshing…")
	case stale:
		return stError.Render("refresh failed")
	}
	return ""
}
