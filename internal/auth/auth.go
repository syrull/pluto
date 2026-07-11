// Package auth manages the harness's Anthropic OAuth credentials.
package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func claudeCredsPath() string {
	return filepath.Join(homeDir(), ".claude", ".credentials.json")
}

func storePath() string {
	return filepath.Join(homeDir(), ".pluto", "credentials.json")
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

type claudeCredsFile struct {
	ClaudeAIOAuth OAuthToken `json:"claudeAiOauth"`
}

// OAuthToken is a Claude Code OAuth credential.
type OAuthToken struct {
	AccessToken  string   `json:"accessToken"`
	RefreshToken string   `json:"refreshToken,omitempty"`
	ExpiresAt    int64    `json:"expiresAt,omitempty"` // epoch millis
	Scopes       []string `json:"scopes,omitempty"`
}

// Valid reports whether the token has an access token and is not past expiry.
func (t OAuthToken) Valid() bool {
	if t.AccessToken == "" {
		return false
	}
	if t.ExpiresAt == 0 {
		return true // no expiry info; assume usable
	}
	return time.Now().UnixMilli() < t.ExpiresAt
}

// Load returns the stored harness token, or false if none is stored.
func Load() (OAuthToken, bool) {
	return readToken(storePath())
}

// LoginCommand returns the *exec.Cmd that performs the interactive login.
func LoginCommand() *exec.Cmd {
	return exec.Command("claude", "setup-token")
}

// CaptureAfterLogin reads the token Claude Code just wrote and copies it into the harness store.
func CaptureAfterLogin() (OAuthToken, error) {
	tok, ok := readClaudeToken()
	if !ok || tok.AccessToken == "" {
		return OAuthToken{}, fmt.Errorf("auth: no credentials found after login (checked %s)", credLocations())
	}
	if err := save(tok); err != nil {
		return OAuthToken{}, err
	}
	return tok, nil
}

// readKeychain reads Claude Code's OAuth credential blob from the OS secret
// store (the macOS login Keychain on darwin; a no-op elsewhere). It is a
// variable so tests can stub the platform lookup.
var readKeychain = keychainCreds

// readClaudeToken loads the token Claude Code wrote at login. On macOS the
// credentials live in the login Keychain, on Linux in a JSON file; the keychain
// is authoritative on darwin (freshly written by `claude setup-token`), so it
// wins over any stale file.
func readClaudeToken() (OAuthToken, bool) {
	if data, ok := readKeychain(); ok {
		if tok, ok := parseClaudeCreds(data); ok {
			return tok, true
		}
	}
	if data, err := os.ReadFile(claudeCredsPath()); err == nil {
		if tok, ok := parseClaudeCreds(data); ok {
			return tok, true
		}
	}
	return OAuthToken{}, false
}

func parseClaudeCreds(data []byte) (OAuthToken, bool) {
	var f claudeCredsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return OAuthToken{}, false
	}
	return f.ClaudeAIOAuth, f.ClaudeAIOAuth.AccessToken != ""
}

func readToken(path string) (OAuthToken, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return OAuthToken{}, false
	}
	var t OAuthToken
	if err := json.Unmarshal(data, &t); err != nil {
		return OAuthToken{}, false
	}
	return t, t.AccessToken != ""
}

func save(t OAuthToken) error {
	path := storePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("auth: create store dir: %w", err)
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: marshal token: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("auth: write store: %w", err)
	}
	return nil
}
