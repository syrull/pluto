// Package anthropic implements an llm.Provider backed by the Anthropic Messages API.
package anthropic

import (
	"context"
	"net/http"
	"os"

	"github.com/pluto/harness/internal/auth"
)

// authMode distinguishes the two credential styles.
type authMode int

const (
	authNone authMode = iota
	// authOAuth is a Claude Code subscription token. It authenticates as the
	// Claude Code CLI: Bearer auth + the oauth beta header, and the system
	// prompt MUST lead with the Claude Code identity block (the token is scoped
	// to that identity).
	authOAuth
	// authAPIKey is a standard Anthropic API key.
	authAPIKey
)

// claudeCodeIdentity is the exact first system block the OAuth token requires.
const claudeCodeIdentity = "You are Claude Code, Anthropic's official CLI for Claude."

// credentials holds a resolved token and how to apply it to requests.
type credentials struct {
	mode  authMode
	token string
}

// resolveCredentials reads credentials in precedence order: env vars, then stored token.
func resolveCredentials() credentials {
	if tok := os.Getenv("ANTHROPIC_OAUTH_TOKEN"); tok != "" {
		return credentials{mode: authOAuth, token: tok}
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		return credentials{mode: authAPIKey, token: key}
	}
	if tok, ok := auth.LoadValid(context.Background()); ok {
		return credentials{mode: authOAuth, token: tok.AccessToken}
	}
	return credentials{mode: authNone}
}

// ok reports whether usable credentials were found.
func (c credentials) ok() bool { return c.mode != authNone }

// apply sets the auth and version headers appropriate to the credential mode.
func (c credentials) apply(h http.Header) {
	h.Set("anthropic-version", "2023-06-01")
	h.Set("content-type", "application/json")
	switch c.mode {
	case authOAuth:
		h.Set("authorization", "Bearer "+c.token)
		// The OAuth token is minted for the Claude Code client; this beta gate
		// is what the CLI sends and what the token is authorized against.
		h.Set("anthropic-beta", "oauth-2025-04-20")
	case authAPIKey:
		h.Set("x-api-key", c.token)
	}
}

// requiresClaudeCodeIdentity reports whether the system prompt requires the Claude Code identity.
func (c credentials) requiresClaudeCodeIdentity() bool { return c.mode == authOAuth }
