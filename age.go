package main

// How long ago something happened, the same in every tool of the family that
// shows it: "5m" in a list column, "5m ago" in a sentence.

import (
	"fmt"
	"time"
)

// compactAge formats the time from t to now as 5m, 3h, 2d, 6w or 2y.
func compactAge(t, now time.Time) string {
	s := int64(now.Sub(t).Seconds())
	if s < 0 {
		s = 0
	}
	const minute, hour, day, week, year = 60, 3600, 86400, 604800, 31536000
	switch {
	case s < hour:
		return fmt.Sprintf("%dm", s/minute)
	case s < day:
		return fmt.Sprintf("%dh", s/hour)
	case s < week:
		return fmt.Sprintf("%dd", s/day)
	case s < year:
		return fmt.Sprintf("%dw", s/week)
	}
	return fmt.Sprintf("%dy", s/year)
}

// relTime is compactAge for a sentence: "2h ago", "just now" under a minute,
// "?" for a time nobody recorded.
func relTime(t time.Time) string {
	switch now := time.Now(); {
	case t.IsZero():
		return "?"
	case now.Sub(t) < time.Minute:
		return "just now"
	default:
		return compactAge(t, now) + " ago"
	}
}
