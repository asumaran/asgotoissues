package main

import (
	"strings"
	"testing"
)

type ranked struct {
	name, group string
	score       int
}

func names(rs []ranked) string {
	var out []string
	for _, r := range rs {
		out = append(out, r.name)
	}
	return strings.Join(out, " ")
}

func TestRankPutsTheBestMatchFirst(t *testing.T) {
	score := func(r ranked) int { return r.score }
	flat := []ranked{{"newest", "", 10}, {"older", "", 90}, {"oldest", "", 10}}
	if got := names(rank(flat, score, nil)); got != "older newest oldest" {
		t.Errorf("flat = %s, want the best first and ties in the list's own order", got)
	}
	grouped := []ranked{{"a1", "a", 10}, {"a2", "a", 40}, {"b1", "b", 90}, {"b2", "b", 5}, {"c1", "c", 40}}
	got := names(rank(grouped, score, func(r ranked) string { return r.group }))
	if got != "b1 b2 a2 a1 c1" {
		t.Errorf("grouped = %s, want groups by their best item, each kept together", got)
	}
	if len(rank([]ranked(nil), score, nil)) != 0 {
		t.Errorf("nothing in, nothing out")
	}
}
