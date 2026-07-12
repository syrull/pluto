package tui

import (
	"os/exec"
	"runtime"
)

// openBrowser best-effort opens url in the user's default browser. Failure is
// silent: the URL is always printed to the transcript so the user can open it
// manually (or complete login on another machine).
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
