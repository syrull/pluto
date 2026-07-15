package auth

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/syrull/pluto/internal/debug"
)

// TestAuthLogRedactsTokens is the hard-requirement guard: no secret material
// (access/refresh tokens) may ever reach the debug log from the auth path.
func TestAuthLogRedactsTokens(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	logPath := filepath.Join(t.TempDir(), "debug.log")
	t.Setenv("PLUTO_DEBUG", "1")
	t.Setenv("PLUTO_DEBUG_FILE", logPath)
	t.Setenv("PLUTO_DEBUG_LEVEL", "trace")
	t.Setenv("PLUTO_DEBUG_COMPONENTS", "")
	_ = debug.Close()
	if _, err := debug.Init(); err != nil {
		t.Fatalf("debug.Init: %v", err)
	}
	t.Cleanup(func() { _ = debug.Close() })

	const (
		oldRefresh = "refresh-OLD-SECRET-TOKEN-zzz"
		newAccess  = "access-NEW-SECRET-TOKEN-aaa"
		newRefresh = "refresh-NEW-SECRET-TOKEN-bbb"
	)
	stubPoster(t, func(_ context.Context, _ map[string]any) (OAuthToken, error) {
		return OAuthToken{
			AccessToken:  newAccess,
			RefreshToken: newRefresh,
			ExpiresAt:    time.Now().Add(time.Hour).UnixMilli(),
			Scopes:       []string{"user:inference"},
		}, nil
	})

	if _, err := Refresh(context.Background(), oldRefresh); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Exercise the login-completion path too.
	f := &Flow{pkce: pkce{verifier: "v"}}
	if _, err := f.Complete(context.Background(), "some-code#v"); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	_ = debug.Close()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	out := string(data)
	for _, secret := range []string{oldRefresh, newAccess, newRefresh} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret %q leaked into debug log:\n%s", secret, out)
		}
	}
	// The auth path should still have logged something (redacted) so we know the
	// events fired and the redaction — not silence — is what kept secrets out.
	if !strings.Contains(out, "[auth]") || !strings.Contains(out, "<redacted") {
		t.Fatalf("expected redacted auth events in log:\n%s", out)
	}
}
