package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureAfterLoginCopiesClaudeToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	future := time.Now().Add(time.Hour).UnixMilli()
	writeJSON(t, filepath.Join(home, ".claude", ".credentials.json"), claudeCredsFile{
		ClaudeAIOAuth: OAuthToken{
			AccessToken:  "access-abc",
			RefreshToken: "refresh-xyz",
			ExpiresAt:    future,
			Scopes:       []string{"user:inference", "user:sessions:claude_code"},
		},
	})

	got, err := CaptureAfterLogin()
	if err != nil {
		t.Fatalf("CaptureAfterLogin: %v", err)
	}
	if got.AccessToken != "access-abc" || got.RefreshToken != "refresh-xyz" {
		t.Fatalf("captured token = %+v", got)
	}

	loaded, ok := Load()
	if !ok {
		t.Fatal("Load returned no token after capture")
	}
	if loaded.AccessToken != "access-abc" {
		t.Fatalf("loaded token = %+v", loaded)
	}
	info, err := os.Stat(filepath.Join(home, ".pluto", "credentials.json"))
	if err != nil {
		t.Fatalf("store file missing: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("store perms = %o, want 600", perm)
	}
}

func TestCaptureAfterLoginNoClaudeFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := CaptureAfterLogin(); err == nil {
		t.Fatal("expected error when no claude credentials exist")
	}
}

func TestTokenValid(t *testing.T) {
	cases := []struct {
		name string
		tok  OAuthToken
		want bool
	}{
		{"empty", OAuthToken{}, false},
		{"no expiry", OAuthToken{AccessToken: "x"}, true},
		{"future", OAuthToken{AccessToken: "x", ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}, true},
		{"past", OAuthToken{AccessToken: "x", ExpiresAt: time.Now().Add(-time.Hour).UnixMilli()}, false},
	}
	for _, c := range cases {
		if got := c.tok.Valid(); got != c.want {
			t.Errorf("%s: Valid() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLoadMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, ok := Load(); ok {
		t.Fatal("Load should return false with no store")
	}
}
