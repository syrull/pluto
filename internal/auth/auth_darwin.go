//go:build darwin

package auth

import (
	"bytes"
	"fmt"
	"os/exec"
)

// keychainService is the macOS login-Keychain generic-password service under
// which Claude Code stores its OAuth credentials on darwin.
const keychainService = "Claude Code-credentials"

// keychainCreds returns Claude Code's OAuth credential JSON from the macOS
// login Keychain, or false if it is absent. On macOS `claude setup-token`
// writes credentials here instead of to ~/.claude/.credentials.json.
func keychainCreds() ([]byte, bool) {
	out, err := exec.Command("security", "find-generic-password", "-s", keychainService, "-w").Output()
	if err != nil {
		return nil, false
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// credLocations names the credential sources searched, for error messages.
func credLocations() string {
	return fmt.Sprintf("%s and the macOS Keychain (service %q)", claudeCredsPath(), keychainService)
}
