// Package main implements the django-websocket-gateway WebSocket gateway.
//
// The gateway runs as a standalone process alongside Django. It accepts
// browser WebSocket connections, validates each one with Django via the
// /internal/ws-auth/ endpoint, and fans out messages from a Redis pub/sub
// backplane to subscribed clients.
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds every runtime parameter the gateway needs.
//
// All fields are populated from environment variables by Load. The struct is
// immutable after construction — pass it by pointer to avoid copies, never
// mutate it.
type Config struct {
	// ListenAddr is the TCP address the HTTP server binds to, e.g. ":8080".
	ListenAddr string
	// RedisURL is the redis://... URL of the pub/sub backplane.
	RedisURL string
	// DjangoAuthURL is the absolute URL of Django's /internal/ws-auth/ endpoint.
	DjangoAuthURL string
	// InternalAuthSecret is the shared secret sent in the X-Internal-Auth
	// header on every call to Django. Never log this value.
	InternalAuthSecret string
	// AllowedOrigins is the list of Origin header values accepted on the
	// WebSocket upgrade. Empty means "no upgrade is permitted".
	AllowedOrigins []string
	// MaxConnectionsPerUser caps simultaneous WebSocket connections from one user.
	MaxConnectionsPerUser int
	// MaxConnectionsTotal caps simultaneous WebSocket connections process-wide.
	MaxConnectionsTotal int
	// MaxMessageSize is the maximum allowed size, in bytes, of an inbound frame.
	MaxMessageSize int64
	// ConnectionMaxLifetime is the upper bound on the lifetime of a single
	// WebSocket connection. The server closes it cleanly when reached.
	ConnectionMaxLifetime time.Duration
	// PingInterval is how often the server sends a WebSocket ping.
	PingInterval time.Duration
	// PongTimeout is the read deadline used to detect dead connections.
	PongTimeout time.Duration
	// LogLevel sets the slog log level. "debug", "info", "warn", "error".
	LogLevel slog.Level
}

// ErrShortSecret is returned by Load when INTERNAL_AUTH_SECRET is shorter
// than the minimum length. The wrapped error message intentionally does
// not echo the secret value (hard rule #3).
var ErrShortSecret = errors.New("INTERNAL_AUTH_SECRET must be at least 32 characters")

const minSecretLength = 32

// Load reads the gateway configuration from the process environment.
//
// Required environment variables:
//
//	REDIS_URL              - pub/sub backplane URL
//	DJANGO_AUTH_URL        - absolute URL of /internal/ws-auth/
//	INTERNAL_AUTH_SECRET   - shared secret, ≥ 32 characters
//	ALLOWED_ORIGINS        - comma-separated list of allowed Origin values
//
// Optional environment variables (with defaults):
//
//	LISTEN_ADDR              :8080
//	MAX_CONNECTIONS_PER_USER 10
//	MAX_CONNECTIONS_TOTAL    50000
//	MAX_MESSAGE_SIZE         8192
//	CONNECTION_MAX_LIFETIME  12h
//	PING_INTERVAL            30s
//	PONG_TIMEOUT             60s
//	LOG_LEVEL                info
//
// Load returns an error that names the offending variable when validation
// fails. The error never contains the value of INTERNAL_AUTH_SECRET.
func Load() (*Config, error) {
	cfg := &Config{
		ListenAddr:            envOrDefault("LISTEN_ADDR", ":8080"),
		RedisURL:              os.Getenv("REDIS_URL"),
		DjangoAuthURL:         os.Getenv("DJANGO_AUTH_URL"),
		InternalAuthSecret:    os.Getenv("INTERNAL_AUTH_SECRET"),
		MaxConnectionsPerUser: 10,
		MaxConnectionsTotal:   50000,
		MaxMessageSize:        8192,
		ConnectionMaxLifetime: 12 * time.Hour,
		PingInterval:          30 * time.Second,
		PongTimeout:           60 * time.Second,
		LogLevel:              slog.LevelInfo,
	}

	for _, kv := range []struct {
		name  string
		value string
	}{
		{"REDIS_URL", cfg.RedisURL},
		{"DJANGO_AUTH_URL", cfg.DjangoAuthURL},
		{"INTERNAL_AUTH_SECRET", cfg.InternalAuthSecret},
	} {
		if kv.value == "" {
			return nil, fmt.Errorf("%s is required", kv.name)
		}
	}

	if len(cfg.InternalAuthSecret) < minSecretLength {
		// Wrap a sentinel error rather than including the offending value.
		return nil, ErrShortSecret
	}

	originsRaw := os.Getenv("ALLOWED_ORIGINS")
	if originsRaw == "" {
		return nil, fmt.Errorf("ALLOWED_ORIGINS is required")
	}
	for _, origin := range strings.Split(originsRaw, ",") {
		if trimmed := strings.TrimSpace(origin); trimmed != "" {
			cfg.AllowedOrigins = append(cfg.AllowedOrigins, trimmed)
		}
	}
	if len(cfg.AllowedOrigins) == 0 {
		return nil, fmt.Errorf("ALLOWED_ORIGINS is required")
	}

	if err := parseIntInto("MAX_CONNECTIONS_PER_USER", &cfg.MaxConnectionsPerUser); err != nil {
		return nil, err
	}
	if err := parseIntInto("MAX_CONNECTIONS_TOTAL", &cfg.MaxConnectionsTotal); err != nil {
		return nil, err
	}
	if err := parseInt64Into("MAX_MESSAGE_SIZE", &cfg.MaxMessageSize); err != nil {
		return nil, err
	}
	if err := parseDurationInto("CONNECTION_MAX_LIFETIME", &cfg.ConnectionMaxLifetime); err != nil {
		return nil, err
	}
	if err := parseDurationInto("PING_INTERVAL", &cfg.PingInterval); err != nil {
		return nil, err
	}
	if err := parseDurationInto("PONG_TIMEOUT", &cfg.PongTimeout); err != nil {
		return nil, err
	}
	if err := parseLogLevelInto("LOG_LEVEL", &cfg.LogLevel); err != nil {
		return nil, err
	}

	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseIntInto(name string, dest *int) error {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("%s: invalid integer: %w", name, err)
	}
	*dest = v
	return nil
}

func parseInt64Into(name string, dest *int64) error {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("%s: invalid integer: %w", name, err)
	}
	*dest = v
	return nil
}

func parseDurationInto(name string, dest *time.Duration) error {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("%s: invalid duration: %w", name, err)
	}
	*dest = v
	return nil
}

func parseLogLevelInto(name string, dest *slog.Level) error {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	switch strings.ToLower(raw) {
	case "debug":
		*dest = slog.LevelDebug
	case "info":
		*dest = slog.LevelInfo
	case "warn", "warning":
		*dest = slog.LevelWarn
	case "error":
		*dest = slog.LevelError
	default:
		return fmt.Errorf("%s: unknown log level %q", name, raw)
	}
	return nil
}
