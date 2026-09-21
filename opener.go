package main

// What opens the selection: a browser, an editor, `claude`.
//
// This file is the same in every tool of the family that opens something.

import (
	"os"
	"strings"
)

// openerArgv is the command <TOOL>_OPENER names, as words, or nil when the
// variable is unset and the tool's own default applies. The value is a
// command line, not a path: `code -n` and a wrapper with flags both work, and
// a path with spaces does not. The tests and the pty checks point it at a
// logging stub.
func openerArgv(tool string) []string {
	return strings.Fields(os.Getenv(strings.ToUpper(tool) + "_OPENER"))
}
