package main

// The divider between the list and the preview. shift+left and shift+right
// move it one step and the position is remembered between runs. The limits,
// the step and the default are asgitlog's.

import (
	"charm.land/bubbles/v2/viewport"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	splitMin, splitMax, splitStep = 30, 85, 5

	splitDefault = 75 // list 25%, preview 75%
	splitFile    = "split-columns"
)

// loadSplit reads the preview's share of the width, in percent, from dir.
func loadSplit(dir string) int {
	data, err := os.ReadFile(filepath.Join(dir, splitFile))
	if err != nil {
		return splitDefault
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || n < splitMin || n > splitMax {
		return splitDefault
	}
	return n
}

// saveSplit is best effort: a read-only state dir only costs the persistence.
func saveSplit(dir string, split int) {
	if dir == "" || os.MkdirAll(dir, 0o755) != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, splitFile), []byte(strconv.Itoa(split)+"\n"), 0o644)
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

// moveSplit moves the divider one step and remembers where it was left.
func moveSplit(dir string, split int, grow bool) int {
	split = stepSplit(split, grow)
	saveSplit(dir, split)
	return split
}

// sizePanes gives the list and the preview their share of the main section.
func sizePanes(list, prev *viewport.Model, listW, prevW, height int) {
	list.SetWidth(listW)
	list.SetHeight(height)
	prev.SetWidth(prevW)
	prev.SetHeight(height)
}
