// Package auth manages the harness's Anthropic OAuth credentials.
//
// Authentication mirrors pi's Claude Pro/Max flow: the harness runs its own
// PKCE authorization-code flow (see oauth.go), stores the minted access/refresh
// pair, and silently refreshes the access token via the refresh token when it
// expires — so the user does not have to re-login on every expiry.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func storePath() string {
	return filepath.Join(homeDir(), ".pluto", "credentials.json")
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
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

// LoadValid returns a usable token, refreshing it first if it has expired but
// carries a refresh token. It returns false only when no token is stored or a
// refresh is impossible/failed. On a successful refresh the rotated token is
// persisted.
func LoadValid(ctx context.Context) (OAuthToken, bool) {
	tok, ok := Load()
	if !ok {
		return OAuthToken{}, false
	}
	if tok.Valid() {
		return tok, true
	}
	if tok.RefreshToken == "" {
		return OAuthToken{}, false
	}
	refreshed, err := Refresh(ctx, tok.RefreshToken)
	if err != nil {
		return OAuthToken{}, false
	}
	return refreshed, true
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
