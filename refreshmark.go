package main

// What the foot says about a list that is fetched in the background and shown
// from a cache meanwhile. The status is the foot's context (footLine's info):
// one persistent place, next to the messages that take the same line, instead
// of a mark on an edge nobody reads.
//
// This file is the same in every tool of the family that needs it.

import "charm.land/bubbles/v2/spinner"

// newRefreshSpinner is the spinner the mark animates with while a fetch runs:
// the braille dots, dim like the text after them. It only ticks while the
// model keeps scheduling its Tick (see the spinner.TickMsg case in Update).
func newRefreshSpinner() spinner.Model {
	return spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(stDim))
}

// refreshMark is the spinner and "refreshing…" while the fetch runs and,
// after one that failed, a standing "refresh failed": the list is the cached
// one. The error itself takes the same line until the next key; the mark
// outlives it.
func refreshMark(sp spinner.Model, refreshing, stale bool) string {
	switch {
	case refreshing:
		return sp.View() + stDim.Render(" refreshing…")
	case stale:
		return stError.Render("refresh failed")
	}
	return ""
}
