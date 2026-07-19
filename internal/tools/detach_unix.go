//go:build unix

package tools

import "syscall"

// detachSysProcAttr places a spawned command in its own session with no
// controlling terminal (setsid). Interactive programs such as sudo, ssh, or a
// git passphrase prompt read secrets straight from /dev/tty, bypassing the
// redirected stdin — with no controlling terminal that read fails cleanly ("no
// tty present") instead of fighting the TUI for the real terminal and leaking the
// prompt text and the keys typed at it into the input box. Returns nil (leaving
// the child attached) only where setsid is unavailable.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
