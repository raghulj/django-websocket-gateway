package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// AuthResult is the validated outcome of a Django auth call.
//
// The struct combines fields returned by Django (UserID, Username,
// AllowedChannels) with the SessionKey supplied as input. SessionKey is
// echoed back into the result because the hub needs it at register time to
// auto-subscribe the client to its _session:{key} control channel.
type AuthResult struct {
	UserID          int64    `json:"user_id"`
	Username        string   `json:"username"`
	AllowedChannels []string `json:"allowed_channels"`
	// SessionKey is the session ID supplied to Validate, NOT a value returned
	// by Django. The hub uses it to address the per-session control channel.
	SessionKey string `json:"-"`
}

// authResponse mirrors the JSON body returned by /internal/ws-auth/.
type authResponse struct {
	Authenticated   bool     `json:"authenticated"`
	UserID          int64    `json:"user_id"`
	Username        string   `json:"username"`
	AllowedChannels []string `json:"allowed_channels"`
}

// Sentinel errors. Use errors.Is to distinguish them at call sites.
var (
	// ErrUnauthenticated indicates Django rejected the session (HTTP 401 or
	// {"authenticated": false}). Connections should be closed with code 4401.
	ErrUnauthenticated = errors.New("websocket-auth: unauthenticated")
	// ErrAuthFailed wraps any other failure mode (transport error, 5xx,
	// malformed JSON, timeout). The connection should be refused with a
	// non-permanent close code; the client may retry.
	ErrAuthFailed = errors.New("websocket-auth: failed")
)

// Authenticator is a stateless client for Django's /internal/ws-auth/.
//
// One Authenticator is constructed at startup with NewAuthenticator and
// shared across all goroutines; the underlying http.Client is safe for
// concurrent use.
type Authenticator struct {
	httpClient *http.Client
	url        string
	secret     string
}

// NewAuthenticator builds an Authenticator from cfg.
//
// The HTTP client uses cfg.AuthTimeout as the per-request deadline. Connections
// are reused via the default transport's connection pool.
func NewAuthenticator(cfg *Config) *Authenticator {
	timeout := cfg.AuthTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Authenticator{
		httpClient: &http.Client{Timeout: timeout},
		url:        cfg.DjangoAuthURL,
		secret:     cfg.InternalAuthSecret,
	}
}

// Validate asks Django whether sessionID is currently valid and, if so,
// returns the user identity and list of allowed channels.
//
// The shared secret is sent in the X-Internal-Auth header. The session ID is
// sent in X-Forwarded-Session so it never appears as a cookie or in the URL
// (and never in any log emitted by this package — only a short SHA-256
// digest of the session ID is logged when something goes wrong).
//
// Returns ErrUnauthenticated when Django says the session is invalid, and
// ErrAuthFailed when the call itself fails (transport, 5xx, malformed JSON).
// Both errors are sentinel-wrapped; use errors.Is to distinguish.
func (a *Authenticator) Validate(ctx context.Context, sessionID string) (*AuthResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, bytes.NewReader(nil))
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrAuthFailed, err)
	}
	req.Header.Set("X-Internal-Auth", a.secret)
	req.Header.Set("X-Forwarded-Session", sessionID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		slog.Warn(
			"ws-auth call failed",
			"auth_url", a.url,
			"session_hash", sessionHash(sessionID),
			"reason", "transport",
		)
		return nil, fmt.Errorf("%w: transport: %v", ErrAuthFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		slog.Warn(
			"ws-auth read body failed",
			"auth_url", a.url,
			"session_hash", sessionHash(sessionID),
			"reason", "read_body",
		)
		return nil, fmt.Errorf("%w: read body: %v", ErrAuthFailed, err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthenticated
	}
	if resp.StatusCode >= 500 || resp.StatusCode < 200 {
		slog.Warn(
			"ws-auth non-2xx",
			"auth_url", a.url,
			"status", resp.StatusCode,
			"session_hash", sessionHash(sessionID),
		)
		return nil, fmt.Errorf("%w: status %d", ErrAuthFailed, resp.StatusCode)
	}

	var parsed authResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		slog.Warn(
			"ws-auth malformed body",
			"auth_url", a.url,
			"session_hash", sessionHash(sessionID),
			"reason", "json",
		)
		return nil, fmt.Errorf("%w: malformed json: %v", ErrAuthFailed, err)
	}
	if !parsed.Authenticated {
		return nil, ErrUnauthenticated
	}
	return &AuthResult{
		UserID:          parsed.UserID,
		Username:        parsed.Username,
		AllowedChannels: parsed.AllowedChannels,
		SessionKey:      sessionID,
	}, nil
}

// sessionHash returns the first 8 hex chars of SHA-256(sessionID). The full
// session ID would identify the user across logs, so only a fingerprint is
// emitted — enough to correlate within a short window, not enough to replay.
func sessionHash(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return fmt.Sprintf("%x", sum)[:8]
}
