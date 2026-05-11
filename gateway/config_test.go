package main

import (
	"strings"
	"testing"
	"time"
)

// requiredEnv is the minimum environment that lets Load() succeed.
// Individual tests delete one key at a time to exercise missing-required-key paths.
func requiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("DJANGO_AUTH_URL", "http://django:8000/internal/ws-auth/")
	t.Setenv("INTERNAL_AUTH_SECRET", "this-is-a-valid-thirty-two-character-secret-and-more")
	t.Setenv("ALLOWED_ORIGINS", "https://app.example.com,https://admin.example.com")
}

func TestLoad_DefaultsWhenOnlyRequiredEnvSet(t *testing.T) {
	requiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr default = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.MaxConnectionsPerUser != 10 {
		t.Errorf("MaxConnectionsPerUser = %d, want 10", cfg.MaxConnectionsPerUser)
	}
	if cfg.MaxConnectionsTotal != 50000 {
		t.Errorf("MaxConnectionsTotal = %d, want 50000", cfg.MaxConnectionsTotal)
	}
	if cfg.MaxMessageSize != 8192 {
		t.Errorf("MaxMessageSize = %d, want 8192", cfg.MaxMessageSize)
	}
	if cfg.ConnectionMaxLifetime != 12*time.Hour {
		t.Errorf("ConnectionMaxLifetime = %v, want 12h", cfg.ConnectionMaxLifetime)
	}
	if cfg.PingInterval != 30*time.Second {
		t.Errorf("PingInterval = %v, want 30s", cfg.PingInterval)
	}
	if cfg.PongTimeout != 60*time.Second {
		t.Errorf("PongTimeout = %v, want 60s", cfg.PongTimeout)
	}
	if len(cfg.AllowedOrigins) != 2 {
		t.Errorf("AllowedOrigins count = %d, want 2", len(cfg.AllowedOrigins))
	}
	if cfg.AllowedOrigins[0] != "https://app.example.com" {
		t.Errorf("AllowedOrigins[0] = %q", cfg.AllowedOrigins[0])
	}
}

func TestLoad_MissingRequiredEnvNamesTheKey(t *testing.T) {
	cases := []string{"REDIS_URL", "DJANGO_AUTH_URL", "INTERNAL_AUTH_SECRET", "ALLOWED_ORIGINS"}
	for _, missing := range cases {
		t.Run(missing, func(t *testing.T) {
			requiredEnv(t)
			t.Setenv(missing, "")

			_, err := Load()
			if err == nil {
				t.Fatalf("expected error when %s is unset", missing)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("error %q does not name %s", err, missing)
			}
		})
	}
}

func TestLoad_ShortSecretRejected_NoValueLeak(t *testing.T) {
	requiredEnv(t)
	const shortSecret = "way-too-short"
	t.Setenv("INTERNAL_AUTH_SECRET", shortSecret)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for short secret")
	}
	msg := err.Error()
	if !strings.Contains(msg, "INTERNAL_AUTH_SECRET must be at least 32 characters") {
		t.Errorf("error message %q does not match the documented wording", msg)
	}
	if strings.Contains(msg, shortSecret) {
		t.Errorf("error leaks the secret value: %q", msg)
	}
}

func TestLoad_BadDurationNamesTheField(t *testing.T) {
	requiredEnv(t)
	t.Setenv("PING_INTERVAL", "garbage")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for bad PING_INTERVAL")
	}
	if !strings.Contains(err.Error(), "PING_INTERVAL") {
		t.Errorf("error %q does not name PING_INTERVAL", err)
	}
}

func TestLoad_AllowedOriginsParsesCommaSeparated(t *testing.T) {
	requiredEnv(t)
	t.Setenv("ALLOWED_ORIGINS", "https://a.example,https://b.example,https://c.example")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"https://a.example", "https://b.example", "https://c.example"}
	if len(cfg.AllowedOrigins) != len(want) {
		t.Fatalf("AllowedOrigins = %v, want %v", cfg.AllowedOrigins, want)
	}
	for i, origin := range want {
		if cfg.AllowedOrigins[i] != origin {
			t.Errorf("AllowedOrigins[%d] = %q, want %q", i, cfg.AllowedOrigins[i], origin)
		}
	}
}

func TestLoad_LogLevelParsing(t *testing.T) {
	requiredEnv(t)
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel.String() != "DEBUG" {
		t.Errorf("LogLevel = %s, want DEBUG", cfg.LogLevel)
	}
}
