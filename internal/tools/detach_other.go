//go:build !unix

package tools

import "syscall"

// detachSysProcAttr is a no-op on platforms without setsid; the command runs with
// the default attributes.
func detachSysProcAttr() *syscall.SysProcAttr { return nil }
