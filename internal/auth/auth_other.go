//go:build !darwin

package auth

// keychainCreds is a no-op off darwin; Claude Code stores credentials in a file
// (~/.claude/.credentials.json) on those platforms.
func keychainCreds() ([]byte, bool) { return nil, false }

// credLocations names the credential sources searched, for error messages.
func credLocations() string { return claudeCredsPath() }
