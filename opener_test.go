package main

import (
	"reflect"
	"testing"
)

func TestOpenerArgv(t *testing.T) {
	t.Setenv("ASTOOL_OPENER", "")
	if got := openerArgv("astool"); len(got) != 0 {
		t.Errorf("unset: %q", got)
	}
	t.Setenv("ASTOOL_OPENER", "  code  -n ")
	if got := openerArgv("astool"); !reflect.DeepEqual(got, []string{"code", "-n"}) {
		t.Errorf("words: %q", got)
	}
}
