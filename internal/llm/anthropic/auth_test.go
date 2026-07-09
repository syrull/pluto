package anthropic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolvePrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Seed a stored login token (lowest precedence).
	future := time.Now().Add(time.Hour).UnixMilli()
	store := filepath.Join(home, ".pluto", "credentials.json")
	if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"accessToken": "stored-tok", "expiresAt": future})
	if err := os.WriteFile(store, data, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("env oauth wins", func(t *testing.T) {
		t.Setenv("ANTHROPIC_OAUTH_TOKEN", "env-oauth")
		t.Setenv("ANTHROPIC_API_KEY", "env-key")
		c := resolveCredentials()
		if c.mode != authOAuth || c.token != "env-oauth" {
			t.Fatalf("got mode=%d token=%q, want oauth env-oauth", c.mode, c.token)
		}
	})

	t.Run("api key beats store", func(t *testing.T) {
		t.Setenv("ANTHROPIC_OAUTH_TOKEN", "")
		t.Setenv("ANTHROPIC_API_KEY", "env-key")
		c := resolveCredentials()
		if c.mode != authAPIKey || c.token != "env-key" {
			t.Fatalf("got mode=%d token=%q, want apikey env-key", c.mode, c.token)
		}
	})

	t.Run("store used when no env", func(t *testing.T) {
		t.Setenv("ANTHROPIC_OAUTH_TOKEN", "")
		t.Setenv("ANTHROPIC_API_KEY", "")
		c := resolveCredentials()
		if c.mode != authOAuth || c.token != "stored-tok" {
			t.Fatalf("got mode=%d token=%q, want oauth stored-tok", c.mode, c.token)
		}
	})
}

func TestReauthPicksUpStoredToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	// Provider constructed with an API key that we then clear to simulate a
	// pre-existing anthropic provider.
	p := &Provider{model: DefaultModel, creds: credentials{mode: authAPIKey, token: "old"}}

	// No creds anywhere now → Reauth should fail.
	if err := p.Reauth(); err == nil {
		t.Fatal("expected Reauth to fail with no credentials")
	}

	// Persist a store token (as /login would) and reauth again.
	future := time.Now().Add(time.Hour).UnixMilli()
	store := filepath.Join(home, ".pluto", "credentials.json")
	_ = os.MkdirAll(filepath.Dir(store), 0o700)
	data, _ := json.Marshal(map[string]any{"accessToken": "new-tok", "expiresAt": future})
	_ = os.WriteFile(store, data, 0o600)

	if err := p.Reauth(); err != nil {
		t.Fatalf("Reauth after store write: %v", err)
	}
	if p.creds.mode != authOAuth || p.creds.token != "new-tok" {
		t.Fatalf("after reauth creds = mode %d token %q", p.creds.mode, p.creds.token)
	}
}
