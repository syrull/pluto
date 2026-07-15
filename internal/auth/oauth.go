package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/syrull/pluto/internal/debug"
)

// Anthropic OAuth (Claude Pro/Max) constants. These mirror the Claude Code CLI
// client that the subscription token is minted for; they are public values, not
// secrets. Ported verbatim from pi's anthropic OAuth flow.
const (
	oauthClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	oauthAuthorizeURL = "https://claude.ai/oauth/authorize"
	oauthTokenURL     = "https://platform.claude.com/v1/oauth/token"
	oauthCallbackHost = "127.0.0.1"
	oauthCallbackPort = 53692
	oauthCallbackPath = "/callback"
	// oauthRedirectURI is the exact redirect registered against the client. The
	// callback server listens on oauthCallbackHost, but the redirect_uri string
	// sent to the authorize/token endpoints MUST use "localhost" to match the
	// registered value.
	oauthRedirectURI = "http://localhost:53692/callback"
	oauthScopes      = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	// expirySkew shortens the stored expiry so a long turn does not die
	// mid-request; matches pi's 5-minute skew.
	expirySkew       = 5 * time.Minute
	oauthHTTPTimeout = 30 * time.Second
)

// pkce holds a generated PKCE verifier and its S256 challenge.
type pkce struct {
	verifier  string
	challenge string
}

// generatePKCE produces a random verifier and its SHA-256 challenge, both
// base64url-encoded without padding (RFC 7636).
func generatePKCE() (pkce, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return pkce{}, fmt.Errorf("auth: generate pkce verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return pkce{verifier: verifier, challenge: challenge}, nil
}

// AuthorizeURL builds the authorization URL for a login attempt. The returned
// Flow carries the PKCE verifier (also used as the state parameter) needed to
// complete the exchange.
func AuthorizeURL() (string, *Flow, error) {
	p, err := generatePKCE()
	if err != nil {
		return "", nil, err
	}
	q := url.Values{
		"code":                  {"true"},
		"client_id":             {oauthClientID},
		"response_type":         {"code"},
		"redirect_uri":          {oauthRedirectURI},
		"scope":                 {oauthScopes},
		"code_challenge":        {p.challenge},
		"code_challenge_method": {"S256"},
		"state":                 {p.verifier},
	}
	debug.Info("auth", "login flow started", "redirect_uri", oauthRedirectURI)
	return oauthAuthorizeURL + "?" + q.Encode(), &Flow{pkce: p}, nil
}

// Flow is an in-progress OAuth login. It holds the PKCE verifier tying the
// authorization request to the token exchange.
type Flow struct {
	pkce pkce
}

// callbackResult carries the code/state captured by the local callback server.
type callbackResult struct {
	code  string
	state string
}

// WaitForCallback runs the local callback server until it receives the OAuth
// redirect, ctx is cancelled, or the deadline elapses, then exchanges the code
// for tokens and persists them. It returns the captured token on success.
//
// If the browser is on another machine, the caller can instead collect the
// redirect URL / code manually and call Complete.
func (f *Flow) WaitForCallback(ctx context.Context) (OAuthToken, error) {
	debug.Info("auth", "waiting for oauth callback", "path", "callback")
	res, err := f.listenForCode(ctx)
	if err != nil {
		debug.Warn("auth", "callback failed", "err", err)
		return OAuthToken{}, err
	}
	if res.state != "" && res.state != f.pkce.verifier {
		debug.Warn("auth", "oauth state mismatch", "path", "callback")
		return OAuthToken{}, fmt.Errorf("auth: oauth state mismatch")
	}
	return f.Complete(ctx, res.code)
}

// listenForCode starts the callback HTTP server and blocks until it captures a
// code, ctx is done, or the timeout fires.
func (f *Flow) listenForCode(ctx context.Context) (callbackResult, error) {
	addr := net.JoinHostPort(oauthCallbackHost, fmt.Sprintf("%d", oauthCallbackPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return callbackResult{}, fmt.Errorf("auth: start callback server on %s: %w", addr, err)
	}

	resultCh := make(chan callbackResult, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(oauthCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			writeHTML(w, http.StatusBadRequest, "Anthropic authentication did not complete. Error: "+e)
			errCh <- fmt.Errorf("auth: authorization error: %s", e)
			return
		}
		code, state := q.Get("code"), q.Get("state")
		if code == "" || state == "" {
			writeHTML(w, http.StatusBadRequest, "Missing code or state parameter.")
			return
		}
		if state != f.pkce.verifier {
			writeHTML(w, http.StatusBadRequest, "State mismatch.")
			errCh <- fmt.Errorf("auth: oauth state mismatch")
			return
		}
		writeHTML(w, http.StatusOK, "Anthropic authentication completed. You can close this window.")
		resultCh <- callbackResult{code: code, state: state}
	})

	srv := &http.Server{Handler: mux}
	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			errCh <- serveErr
		}
	}()
	defer srv.Close()

	select {
	case res := <-resultCh:
		return res, nil
	case err := <-errCh:
		return callbackResult{}, err
	case <-ctx.Done():
		return callbackResult{}, ctx.Err()
	}
}

// Complete exchanges an authorization code (or a pasted redirect URL / raw
// code) for tokens and persists them. It is the manual-entry counterpart to
// WaitForCallback.
func (f *Flow) Complete(ctx context.Context, input string) (OAuthToken, error) {
	code, state := parseAuthorizationInput(input)
	if state != "" && state != f.pkce.verifier {
		debug.Warn("auth", "oauth state mismatch", "path", "paste")
		return OAuthToken{}, fmt.Errorf("auth: oauth state mismatch")
	}
	if code == "" {
		debug.Warn("auth", "missing authorization code", "path", "paste")
		return OAuthToken{}, fmt.Errorf("auth: missing authorization code")
	}
	tok, err := exchangeCode(ctx, code, f.pkce.verifier)
	if err != nil {
		debug.Warn("auth", "code exchange failed", "err", err)
		return OAuthToken{}, err
	}
	if err := save(tok); err != nil {
		return OAuthToken{}, err
	}
	debug.Info("auth", "login success", "token", debug.Redact(tok.AccessToken),
		"refresh", debug.Redact(tok.RefreshToken), "scopes", strings.Join(tok.Scopes, " "), "expires_at", tok.ExpiresAt)
	return tok, nil
}

// parseAuthorizationInput extracts a code and optional state from a full
// redirect URL, a "code#state" fragment, a query string, or a bare code.
func parseAuthorizationInput(input string) (code, state string) {
	v := strings.TrimSpace(input)
	if v == "" {
		return "", ""
	}
	if u, err := url.Parse(v); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return u.Query().Get("code"), u.Query().Get("state")
	}
	if strings.Contains(v, "#") {
		parts := strings.SplitN(v, "#", 2)
		return parts[0], parts[1]
	}
	if strings.Contains(v, "code=") {
		if q, err := url.ParseQuery(v); err == nil {
			return q.Get("code"), q.Get("state")
		}
	}
	return v, ""
}

// tokenResponse is the token endpoint's JSON payload for both the
// authorization_code and refresh_token grants.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope,omitempty"`
}

// toToken converts a token endpoint response into a stored OAuthToken, applying
// the expiry skew.
func (r tokenResponse) toToken() OAuthToken {
	expiresAt := time.Now().Add(time.Duration(r.ExpiresIn)*time.Second - expirySkew).UnixMilli()
	var scopes []string
	if r.Scope != "" {
		scopes = strings.Fields(r.Scope)
	}
	return OAuthToken{
		AccessToken:  r.AccessToken,
		RefreshToken: r.RefreshToken,
		ExpiresAt:    expiresAt,
		Scopes:       scopes,
	}
}

// exchangeCode swaps an authorization code for tokens (authorization_code grant).
func exchangeCode(ctx context.Context, code, verifier string) (OAuthToken, error) {
	body := map[string]any{
		"grant_type":    "authorization_code",
		"client_id":     oauthClientID,
		"code":          code,
		"state":         verifier,
		"redirect_uri":  oauthRedirectURI,
		"code_verifier": verifier,
	}
	return postToken(ctx, body)
}

// Refresh exchanges a refresh token for a fresh access/refresh pair
// (refresh_token grant) and persists the result. It is the counterpart pi calls
// automatically when the stored token is past expiry.
func Refresh(ctx context.Context, refreshToken string) (OAuthToken, error) {
	if refreshToken == "" {
		return OAuthToken{}, fmt.Errorf("auth: no refresh token")
	}
	debug.Info("auth", "refreshing token", "refresh", debug.Redact(refreshToken))
	body := map[string]any{
		"grant_type":    "refresh_token",
		"client_id":     oauthClientID,
		"refresh_token": refreshToken,
	}
	tok, err := postToken(ctx, body)
	if err != nil {
		debug.Warn("auth", "refresh failed", "err", err)
		return OAuthToken{}, err
	}
	if err := save(tok); err != nil {
		return OAuthToken{}, err
	}
	debug.Info("auth", "token refreshed", "token", debug.Redact(tok.AccessToken), "expires_at", tok.ExpiresAt)
	return tok, nil
}

// tokenPoster is the HTTP client used for token endpoint calls; a variable so
// tests can stub the transport.
var tokenPoster = func(ctx context.Context, body map[string]any) (OAuthToken, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return OAuthToken{}, fmt.Errorf("auth: marshal token request: %w", err)
	}
	reqCtx, cancel := context.WithTimeout(ctx, oauthHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, oauthTokenURL, strings.NewReader(string(payload)))
	if err != nil {
		return OAuthToken{}, fmt.Errorf("auth: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return OAuthToken{}, fmt.Errorf("auth: token request to %s failed: %w", oauthTokenURL, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return OAuthToken{}, fmt.Errorf("auth: token request failed: status=%d body=%s", resp.StatusCode, string(respBody))
	}
	var tr tokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return OAuthToken{}, fmt.Errorf("auth: parse token response: %w body=%s", err, string(respBody))
	}
	if tr.AccessToken == "" {
		return OAuthToken{}, fmt.Errorf("auth: token response missing access_token: %s", string(respBody))
	}
	return tr.toToken(), nil
}

// postToken sends a token request through the configurable poster.
func postToken(ctx context.Context, body map[string]any) (OAuthToken, error) {
	return tokenPoster(ctx, body)
}

func writeHTML(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, "<!doctype html><html><body><p>%s</p></body></html>", msg)
}
