package main

import (
	"testing"
	"time"
)

func TestCompactAgeAndRelTime(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{
		0:                      "0m",
		5 * time.Minute:        "5m",
		3 * time.Hour:          "3h",
		2 * 24 * time.Hour:     "2d",
		6 * 7 * 24 * time.Hour: "6w",
		800 * 24 * time.Hour:   "2y",
		-time.Hour:             "0m", // a clock ahead of ours
	} {
		if got := compactAge(now.Add(-d), now); got != want {
			t.Errorf("compactAge(%v) = %q, want %q", d, got, want)
		}
	}
	if got := relTime(time.Time{}); got != "?" {
		t.Errorf("relTime(zero) = %q", got)
	}
	if got := relTime(time.Now().Add(-10 * time.Second)); got != "just now" {
		t.Errorf("relTime(10s) = %q", got)
	}
	if got := relTime(time.Now().Add(-3*time.Hour - time.Minute)); got != "3h ago" {
		t.Errorf("relTime(3h) = %q", got)
	}
}
