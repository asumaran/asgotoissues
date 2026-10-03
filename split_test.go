package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSplitPersists(t *testing.T) {
	for _, tc := range []struct {
		file string
		def  int
	}{{splitColumnsFile, splitColumnsDefault}, {splitRowsFile, splitRowsDefault}} {
		dir := filepath.Join(t.TempDir(), "state")
		if got := loadSplit(dir, tc.file, tc.def); got != tc.def {
			t.Errorf("%s: no file: split = %d, want %d", tc.file, got, tc.def)
		}
		saveSplit(dir, tc.file, 60)
		if got := loadSplit(dir, tc.file, tc.def); got != 60 {
			t.Errorf("%s: saved 60, loaded %d", tc.file, got)
		}
		// Anything out of range or unreadable falls back to the default.
		for _, bad := range []string{"5", "99", "wide", ""} {
			if err := os.WriteFile(filepath.Join(dir, tc.file), []byte(bad), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := loadSplit(dir, tc.file, tc.def); got != tc.def {
				t.Errorf("%s: %q: split = %d, want %d", tc.file, bad, got, tc.def)
			}
		}
	}
}

// TestSplitFilesAreIndependent: moving one layout's divider never touches
// the other's file.
func TestSplitFilesAreIndependent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	moveSplit(dir, splitColumnsFile, splitColumnsDefault, true)
	if got := loadSplit(dir, splitRowsFile, splitRowsDefault); got != splitRowsDefault {
		t.Errorf("moving split-columns touched split-rows: %d", got)
	}
	if got := loadSplit(dir, splitColumnsFile, splitColumnsDefault); got == splitColumnsDefault {
		t.Errorf("split-columns did not move")
	}
}

func TestStepSplitClamps(t *testing.T) {
	if got := stepSplit(75, true); got != 70 {
		t.Errorf("growing the list from 75 = %d, want 70", got)
	}
	if got := stepSplit(75, false); got != 80 {
		t.Errorf("shrinking the list from 75 = %d, want 80", got)
	}
	if got := stepSplit(splitMax, false); got != splitMax {
		t.Errorf("past the max = %d", got)
	}
	if got := stepSplit(splitMin, true); got != splitMin {
		t.Errorf("past the min = %d", got)
	}
}

func TestSplitWidthsFillTheFrame(t *testing.T) {
	for _, innerW := range []int{40, 92, 148} {
		for split := splitMin; split <= splitMax; split += splitStep {
			listW, detailsW := splitWidths(innerW, split)
			if listW+1+detailsW != innerW {
				t.Errorf("innerW %d, split %d: %d + 1 + %d", innerW, split, listW, detailsW)
			}
		}
	}
	if listW, detailsW := splitWidths(100, splitColumnsDefault); listW != 24 || detailsW != 75 {
		t.Errorf("default = %d | %d, want 24 | 75", listW, detailsW)
	}
}
