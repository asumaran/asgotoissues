package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenURLUsesTheToolsOpener(t *testing.T) {
	log := filepath.Join(t.TempDir(), "argv")
	stub := filepath.Join(t.TempDir(), "opener")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho \"$1\" > "+log+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASTOOL_OPENER", stub)
	if err := openURL("astool", "https://example.com/x?a=1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(log); strings.TrimSpace(string(got)) != "https://example.com/x?a=1" {
		t.Errorf("the opener got %q", got)
	}
	_ = os.Remove(log)
	if err := openURL("astool", ""); err != nil {
		t.Errorf("an empty URL is not an error: %v", err)
	}
	if _, err := os.Stat(log); err == nil {
		t.Errorf("an empty URL must open nothing")
	}
	t.Setenv("ASTOOL_OPENER", filepath.Join(t.TempDir(), "missing"))
	if err := openURL("astool", "https://example.com"); err == nil {
		t.Errorf("an opener that cannot run must report it")
	}
}
