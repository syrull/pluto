//go:build unix

package tools

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestDetachSysProcAttrSetsSid(t *testing.T) {
	attr := detachSysProcAttr()
	if attr == nil || !attr.Setsid {
		t.Fatalf("detachSysProcAttr() = %+v, want a non-nil attr with Setsid true", attr)
	}
}

// TestDetachedCommandStartsNewSession proves a spawned command lands in its own
// session with no shared controlling terminal, which is what stops an interactive
// prompt (sudo, ssh) from leaking into the TUI's terminal. setsid makes the child
// a session leader, which is also a process-group leader, so its process-group id
// equals its own pid and differs from the caller's.
func TestDetachedCommandStartsNewSession(t *testing.T) {
	cmd := exec.Command("sleep", "1")
	cmd.SysProcAttr = detachSysProcAttr()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start detached command: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	pid := cmd.Process.Pid
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatalf("getpgid(%d): %v", pid, err)
	}
	if pgid != pid {
		t.Fatalf("child process-group id = %d, want %d (its own pid — a fresh session)", pgid, pid)
	}
	if self, _ := syscall.Getpgid(0); pgid == self {
		t.Fatal("detached child must not share the caller's process group/controlling terminal")
	}
}
