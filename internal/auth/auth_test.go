package auth

import (
	"context"
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

// stubPoster replaces the token endpoint transport for a test.
func stubPoster(t *testing.T, fn func(ctx context.Context, body map[string]any) (OAuthToken, error)) {
	t.Helper()
	prev := tokenPoster
	tokenPoster = fn
	t.Cleanup(func() { tokenPoster = prev })
}

func TestTokenValid(t *testing.T) {
	tests := []struct {
		name string
		tok  OAuthToken
		want bool
	}{
		{"empty", OAuthToken{}, false},
		{"no expiry", OAuthToken{AccessToken: "x"}, true},
		{"future", OAuthToken{AccessToken: "x", ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}, true},
		{"past", OAuthToken{AccessToken: "x", ExpiresAt: time.Now().Add(-time.Hour).UnixMilli()}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.tok.Valid(); got != tc.want {
				t.Fatalf("Valid() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	want := OAuthToken{
		AccessToken:  "access-abc",
		RefreshToken: "refresh-xyz",
		ExpiresAt:    time.Now().Add(time.Hour).UnixMilli(),
		Scopes:       []string{"user:inference"},
	}
	if err := save(want); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, ok := Load()
	if !ok {
		t.Fatal("Load returned no token after save")
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Fatalf("loaded token = %+v", got)
	}

	info, err := os.Stat(filepath.Join(home, ".pluto", "credentials.json"))
	if err != nil {
		t.Fatalf("store file missing: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("store perms = %o, want 600", perm)
	}
}

func TestLoadMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, ok := Load(); ok {
		t.Fatal("Load should report no token when store is absent")
	}
}

func TestGeneratePKCE(t *testing.T) {
	a, err := generatePKCE()
	if err != nil {
		t.Fatalf("generatePKCE: %v", err)
	}
	b, err := generatePKCE()
	if err != nil {
		t.Fatalf("generatePKCE: %v", err)
	}
	if a.verifier == "" || a.challenge == "" {
		t.Fatal("empty verifier/challenge")
	}
	if a.verifier == a.challenge {
		t.Fatal("challenge must differ from verifier (S256)")
	}
	if a.verifier == b.verifier {
		t.Fatal("verifier must be random per call")
	}
}

func TestParseAuthorizationInput(t *testing.T) {
	tests := []struct {
		name, in, code, state string
	}{
		{"bare code", "abc123", "abc123", ""},
		{"code#state", "abc#st", "abc", "st"},
		{"query", "code=abc&state=st", "abc", "st"},
		{"redirect url", "http://localhost:53692/callback?code=abc&state=st", "abc", "st"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, state := parseAuthorizationInput(tc.in)
			if code != tc.code || state != tc.state {
				t.Fatalf("parse(%q) = (%q,%q), want (%q,%q)", tc.in, code, state, tc.code, tc.state)
			}
		})
	}
}

func TestRefreshPersistsRotatedToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var gotBody map[string]any
	stubPoster(t, func(_ context.Context, body map[string]any) (OAuthToken, error) {
		gotBody = body
		return OAuthToken{
			AccessToken:  "new-access",
			RefreshToken: "new-refresh",
			ExpiresAt:    time.Now().Add(time.Hour).UnixMilli(),
		}, nil
	})

	tok, err := Refresh(context.Background(), "old-refresh")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if gotBody["grant_type"] != "refresh_token" || gotBody["refresh_token"] != "old-refresh" || gotBody["client_id"] != oauthClientID {
		t.Fatalf("refresh request body = %+v", gotBody)
	}
	if tok.AccessToken != "new-access" || tok.RefreshToken != "new-refresh" {
		t.Fatalf("refreshed token = %+v", tok)
	}
	// Rotated token must be persisted.
	stored, ok := Load()
	if !ok || stored.AccessToken != "new-access" {
		t.Fatalf("stored token after refresh = %+v (ok=%v)", stored, ok)
	}
}

func TestLoadValidRefreshesExpired(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Seed an expired token that carries a refresh token.
	writeJSON(t, storePath(), OAuthToken{
		AccessToken:  "stale",
		RefreshToken: "refresh-xyz",
		ExpiresAt:    time.Now().Add(-time.Hour).UnixMilli(),
	})

	stubPoster(t, func(_ context.Context, _ map[string]any) (OAuthToken, error) {
		return OAuthToken{
			AccessToken:  "fresh",
			RefreshToken: "refresh-2",
			ExpiresAt:    time.Now().Add(time.Hour).UnixMilli(),
		}, nil
	})

	tok, ok := LoadValid(context.Background())
	if !ok {
		t.Fatal("LoadValid should refresh an expired token with a refresh token")
	}
	if tok.AccessToken != "fresh" {
		t.Fatalf("LoadValid token = %+v, want fresh", tok)
	}
}

func TestLoadValidNoRefreshToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeJSON(t, storePath(), OAuthToken{
		AccessToken: "stale",
		ExpiresAt:   time.Now().Add(-time.Hour).UnixMilli(),
	})
	if _, ok := LoadValid(context.Background()); ok {
		t.Fatal("LoadValid must fail on expired token with no refresh token")
	}
}

func TestToTokenAppliesSkew(t *testing.T) {
	tr := tokenResponse{AccessToken: "a", RefreshToken: "r", ExpiresIn: 3600}
	tok := tr.toToken()
	// expires_in 3600s minus 5min skew => ~3300s in the future.
	deltaMs := tok.ExpiresAt - time.Now().UnixMilli()
	minMs := int64((3300 - 5) * 1000)
	maxMs := int64((3300 + 5) * 1000)
	if deltaMs < minMs || deltaMs > maxMs {
		t.Fatalf("expiry delta = %dms, want ~%dms (skew applied)", deltaMs, 3300*1000)
	}
}
