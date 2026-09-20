package main

// Opening a URL in the browser.
//
// This file is the same in every tool of the family that opens one.

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// openURL hands url to the browser. <TOOL>_OPENER, the variable every tool of
// the family reads for what opens its selection, replaces the browser (the
// tests log the argv instead). On macOS a Chrome that is already up gets a new
// tab in its front window, so the page lands in the window being looked at,
// not in whichever one the system picks.
func openURL(tool, url string) {
	if url == "" {
		return
	}
	if b := os.Getenv(strings.ToUpper(tool) + "_OPENER"); b != "" {
		_ = exec.Command(b, url).Run()
		return
	}
	if runtime.GOOS != "darwin" {
		_ = exec.Command("xdg-open", url).Run()
		return
	}
	if openInChromeFrontWindow(url) == nil {
		return
	}
	_ = exec.Command("open", url).Run()
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
