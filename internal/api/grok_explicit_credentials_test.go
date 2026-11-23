package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type grokExplicitTransport func(*http.Request) (*http.Response, error)

func (f grokExplicitTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGrokExplicitCredentialsIgnoreGlobalLogin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"https://accounts.x.ai/sign-in":{"key":"global-token","email":"other@example.com"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if creds := DetectGrokCredentials(logger); creds == nil || creds.AccessToken != "global-token" {
		t.Fatal("invalid global auth fixture")
	}
	client := NewGrokClient("account-token", logger, WithGrokExplicitCredentials(), WithGrokHTTPTransport(grokExplicitTransport(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer account-token" {
			t.Errorf("unexpected authorization header")
		}
		return nil, errors.New("isolated transport")
	})))
	creds := client.resolveCredentials()
	if creds.AccessToken != "account-token" || creds.Email != "" {
		t.Fatal("explicit account borrowed global identity")
	}
	snap, err := client.FetchSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.LocalSessions != nil || snap.Email != "" {
		t.Fatal("explicit account imported local identity or sessions")
	}
	client.SetToken("")
	if _, err := client.FetchSnapshot(context.Background()); !errors.Is(err, ErrGrokUnauthorized) {
		t.Fatalf("empty explicit token must not discover global login: %v", err)
	}
}
