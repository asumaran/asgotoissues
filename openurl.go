package main

// Opening a URL in the browser.
//
// This file is the same in every tool of the family that opens one.

import (
	"os/exec"
	"runtime"
	"strings"
)

// openURL hands url to the browser and reports whether that worked.
// <TOOL>_OPENER (opener.go) replaces the browser. On macOS a Chrome that is
// already up gets a new tab in its front window, so the page lands in the
// window being looked at, not in whichever one the system picks.
func openURL(tool, url string) error {
	if url == "" {
		return nil
	}
	if argv := openerArgv(tool); len(argv) > 0 {
		return exec.Command(argv[0], append(argv[1:], url)...).Run()
	}
	if runtime.GOOS != "darwin" {
		return exec.Command("xdg-open", url).Run()
	}
	if openInChromeFrontWindow(url) == nil {
		return nil
	}
	return exec.Command("open", url).Run()
}

func openInChromeFrontWindow(url string) error {
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(url)
	script := `tell application "Google Chrome"
	if not running then error "not running"
	if (count of windows) = 0 then error "no windows"
	tell front window to make new tab with properties {URL:"` + esc + `"}
	activate
end tell`
	return exec.Command("osascript", "-e", script).Run()
}
