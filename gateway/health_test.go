package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func TestHealthz_OK(t *testing.T) {
	mr := miniredis.RunT(t)
	hub := newFakeHub()
	sub, err := NewRedisSubscriber(&Config{RedisURL: "redis://" + mr.Addr()}, hub.deliver)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	shuttingDown.Store(false)

	rr := httptest.NewRecorder()
	HealthzHandler(sub)(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "ok" {
		t.Errorf("body = %q, want ok", got)
	}
}

func TestHealthz_ShuttingDown(t *testing.T) {
	mr := miniredis.RunT(t)
	hub := newFakeHub()
	sub, _ := NewRedisSubscriber(&Config{RedisURL: "redis://" + mr.Addr()}, hub.deliver)
	defer sub.Close()
	shuttingDown.Store(true)
	defer shuttingDown.Store(false)

	rr := httptest.NewRecorder()
	HealthzHandler(sub)(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestHealthz_RedisUnreachable(t *testing.T) {
	hub := newFakeHub()
	sub, _ := NewRedisSubscriber(&Config{RedisURL: "redis://127.0.0.1:1"}, hub.deliver)
	defer sub.Close()
	shuttingDown.Store(false)

	rr := httptest.NewRecorder()
	HealthzHandler(sub)(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}
