package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestAuthenticator(t *testing.T, url string) *Authenticator {
	t.Helper()
	return NewAuthenticator(&Config{
		DjangoAuthURL:      url,
		InternalAuthSecret: "this-is-a-valid-thirty-two-character-secret-and-more",
		AuthTimeout:        500 * time.Millisecond,
	})
}

func TestAuthenticator_Validate_HappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.Header.Get("X-Internal-Auth") == "" {
			t.Error("missing X-Internal-Auth header")
		}
		if r.Header.Get("X-Forwarded-Session") != "abc123" {
			t.Errorf("X-Forwarded-Session = %s, want abc123", r.Header.Get("X-Forwarded-Session"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"authenticated": true, "user_id": 42, "username": "alice", "allowed_channels": ["user-42", "org-1"]}`))
	}))
	defer server.Close()

	a := newTestAuthenticator(t, server.URL)
	result, err := a.Validate(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if result.UserID != 42 {
		t.Errorf("UserID = %d, want 42", result.UserID)
	}
	if result.Username != "alice" {
		t.Errorf("Username = %q, want alice", result.Username)
	}
	if result.SessionKey != "abc123" {
		t.Errorf("SessionKey = %q, want abc123 (echoed from input)", result.SessionKey)
	}
	if len(result.AllowedChannels) != 2 || result.AllowedChannels[0] != "user-42" {
		t.Errorf("AllowedChannels = %v", result.AllowedChannels)
	}
}

func TestAuthenticator_Validate_401_ReturnsUnauthenticated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"authenticated": false}`))
	}))
	defer server.Close()

	a := newTestAuthenticator(t, server.URL)
	_, err := a.Validate(context.Background(), "x")
	if !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("err = %v, want ErrUnauthenticated", err)
	}
}

func TestAuthenticator_Validate_200_AuthenticatedFalse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"authenticated": false}`))
	}))
	defer server.Close()

	a := newTestAuthenticator(t, server.URL)
	_, err := a.Validate(context.Background(), "x")
	if !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("err = %v, want ErrUnauthenticated", err)
	}
}

func TestAuthenticator_Validate_5xx_ReturnsAuthFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	a := newTestAuthenticator(t, server.URL)
	_, err := a.Validate(context.Background(), "x")
	if !errors.Is(err, ErrAuthFailed) {
		t.Errorf("err = %v, want ErrAuthFailed", err)
	}
}

func TestAuthenticator_Validate_MalformedJSON_ReturnsAuthFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{not valid json`))
	}))
	defer server.Close()

	a := newTestAuthenticator(t, server.URL)
	_, err := a.Validate(context.Background(), "x")
	if !errors.Is(err, ErrAuthFailed) {
		t.Errorf("err = %v, want ErrAuthFailed", err)
	}
}

func TestAuthenticator_Validate_TransportTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer server.Close()

	a := newTestAuthenticator(t, server.URL)
	start := time.Now()
	_, err := a.Validate(context.Background(), "x")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, ErrAuthFailed) {
		t.Errorf("err = %v, want ErrAuthFailed", err)
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("Validate took %v, expected <1.5s", elapsed)
	}
}

func TestAuthenticator_Validate_LogsDoNotContainSecretOrSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	const secret = "this-is-a-valid-thirty-two-character-secret-and-more"
	const sessionID = "super-secret-session-cookie-value-12345"

	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	originalLogger := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(originalLogger)

	a := NewAuthenticator(&Config{
		DjangoAuthURL:      server.URL,
		InternalAuthSecret: secret,
		AuthTimeout:        500 * time.Millisecond,
	})
	_, _ = a.Validate(context.Background(), sessionID)

	logs := buf.String()
	if strings.Contains(logs, secret) {
		t.Errorf("log captured secret value:\n%s", logs)
	}
	if strings.Contains(logs, sessionID) {
		t.Errorf("log captured raw session id:\n%s", logs)
	}
}
