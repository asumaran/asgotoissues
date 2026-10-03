package main

// The divider between the list and the preview, one file per layout: the
// columns layout's list share of the width (split-columns) and the rows
// layout's list share of the body (split-rows). shift+left and shift+right
// move the one of the effective layout and the position is remembered
// between runs, per layout. The limits, the step and split-columns' default
// are asgitlog's; split-rows' default is lower (see resizeList, ui.go).

import (
	"charm.land/bubbles/v2/viewport"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	splitMin, splitMax, splitStep = 30, 85, 5

	splitColumnsFile    = "split-columns"
	splitColumnsDefault = 75 // list 25%, preview 75%
	splitRowsFile       = "split-rows"
	splitRowsDefault    = 50 // tree rows take two lines; asgitlog's 70 would leave little to see
)

// loadSplit reads the preview's share of the width or the body, in percent,
// from file in dir; def when there is none, or it does not parse.
func loadSplit(dir, file string, def int) int {
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || n < splitMin || n > splitMax {
		return def
	}
	return n
}

// saveSplit is best effort: a read-only state dir only costs the persistence.
func saveSplit(dir, file string, split int) {
	if dir == "" || os.MkdirAll(dir, 0o755) != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, file), []byte(strconv.Itoa(split)+"\n"), 0o644)
}

// stepSplit moves the divider one step. The setting is the preview's share,
// so growing the list shrinks it.
func stepSplit(split int, grow bool) int {
	step := splitStep
	if grow {
		step = -splitStep
	}
	return max(splitMin, min(splitMax, split+step))
}

// splitWidths divides the width inside the frame between the list and the
// preview, minus the divider between them. The list keeps a floor, so on a
// narrow screen it is the preview that gives.
func splitWidths(innerW, split int) (listW, detailsW int) {
	listW = max(10, innerW-1-innerW*split/100)
	return listW, max(12, innerW-1-listW)
}

// moveSplit moves the divider of file one step and remembers where it was
// left.
func moveSplit(dir, file string, split int, grow bool) int {
	split = stepSplit(split, grow)
	saveSplit(dir, file, split)
	return split
}

// sizePanes gives the list and the preview their share of the main section.
func sizePanes(list, prev *viewport.Model, listW, prevW, height int) {
	list.SetWidth(listW)
	list.SetHeight(height)
	prev.SetWidth(prevW)
	prev.SetHeight(height)
}
