package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/syrull/pluto/internal/auth"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func stubClient(t *testing.T, rt roundTripFunc) {
	t.Helper()
	orig := httpClient
	httpClient = &http.Client{Transport: rt}
	t.Cleanup(func() { httpClient = orig })
}

func stubToken(t *testing.T, tok auth.OAuthToken, ok bool) {
	t.Helper()
	orig := loadToken
	loadToken = func() (auth.OAuthToken, bool) { return tok, ok }
	t.Cleanup(func() { loadToken = orig })
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

// The original bug: a transport error left resp nil and panicked on resp.Body.
func TestRunReturnsErrorOnTransportFailure(t *testing.T) {
	stubToken(t, auth.OAuthToken{AccessToken: "tok"}, true)
	stubClient(t, func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})

	err := run(context.Background())
	if err == nil {
		t.Fatal("expected error on transport failure, got nil")
	}
	if !strings.Contains(err.Error(), "request failed") {
		t.Fatalf("expected request-failed error, got %v", err)
	}
}

func TestRunOK(t *testing.T) {
	stubToken(t, auth.OAuthToken{AccessToken: "tok"}, true)
	stubClient(t, func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"ok":true}`), nil
	})

	if err := run(context.Background()); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestRunNon200(t *testing.T) {
	stubToken(t, auth.OAuthToken{AccessToken: "tok"}, true)
	stubClient(t, func(*http.Request) (*http.Response, error) {
		return response(http.StatusUnauthorized, `{"error":"nope"}`), nil
	})

	err := run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected status 401 error, got %v", err)
	}
}

func TestRunNoCredentials(t *testing.T) {
	stubToken(t, auth.OAuthToken{}, false)

	err := run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("expected credentials error, got %v", err)
	}
}
