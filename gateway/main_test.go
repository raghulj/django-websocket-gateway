package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/coder/websocket"
)

// startGatewayStack stands up a fake Django, a miniredis, a hub, and a
// gateway HTTP server. Returns the URL of the gateway's /ws/ endpoint.
func startGatewayStack(t *testing.T, allowedOrigins []string) (gatewayURL, djangoURL string, mr *miniredis.Miniredis, hub *Hub) {
	t.Helper()
	mr = miniredis.RunT(t)

	django := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := map[string]any{
			"authenticated":    true,
			"user_id":          42,
			"username":         "alice",
			"allowed_channels": []string{"user-42"},
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(django.Close)

	cfg := &Config{
		RedisURL:              "redis://" + mr.Addr(),
		DjangoAuthURL:         django.URL,
		InternalAuthSecret:    "test-secret-that-is-thirty-two-characters-long-enough",
		AllowedOrigins:        allowedOrigins,
		MaxConnectionsPerUser: 10,
		MaxConnectionsTotal:   100,
		MaxMessageSize:        4096,
		ConnectionMaxLifetime: 10 * time.Second,
		PingInterval:          1 * time.Second,
		PongTimeout:           5 * time.Second,
		AuthTimeout:           1 * time.Second,
	}

	sub, err := NewRedisSubscriber(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Close() })

	hub = NewHub(cfg, sub)
	sub.SetDeliver(func(m incomingMessage) { hub.incoming <- m })
	sub.Start()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go hub.Run(ctx)

	auth := NewAuthenticator(cfg)
	gw := httptest.NewServer(wsHandler(cfg, hub, auth))
	t.Cleanup(gw.Close)

	gatewayURL = "ws" + strings.TrimPrefix(gw.URL, "http") + "/ws/"
	djangoURL = django.URL
	return
}

func TestGateway_EndToEnd_PublishReachesClient(t *testing.T) {
	gw, _, mr, _ := startGatewayStack(t, []string{"http://example.com"})

	jar, _ := newCookieJar(t, gw, "sess-end-to-end")
	clientConn, _, err := websocket.Dial(context.Background(), gw, &websocket.DialOptions{
		HTTPClient: &http.Client{Jar: jar},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = clientConn.CloseNow() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"action": "subscribe", "channel": "user-42"})
	if err := clientConn.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}

	// Wait for the subscribe to land (control + user channel observable in
	// miniredis subscriber count).
	waitForSubscribers(t, mr, "user-42", 1)

	mr.Publish("user-42", `{"channel":"user-42","payload":{"hello":"world"}}`)

	_, data, err := clientConn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("frame not json: %v", err)
	}
	payload, _ := got["payload"].(map[string]any)
	if payload["hello"] != "world" {
		t.Errorf("payload = %v, want hello=world", got)
	}
}

func TestGateway_RejectsDisallowedOrigin(t *testing.T) {
	gw, _, _, _ := startGatewayStack(t, []string{"https://allowed.example"})

	req, _ := http.NewRequest("GET", strings.Replace(gw, "ws://", "http://", 1), nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Connection", "upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestGateway_RejectsRequestWithoutSession(t *testing.T) {
	gw, _, _, _ := startGatewayStack(t, []string{"http://example.com"})

	_, resp, err := websocket.Dial(context.Background(), gw, nil)
	if err == nil {
		t.Fatal("expected dial error")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// newCookieJar returns an http.CookieJar pre-loaded with a sessionid cookie
// for the supplied URL.
func newCookieJar(t *testing.T, rawURL, sessionID string) (http.CookieJar, error) {
	t.Helper()
	jar, err := newJar()
	if err != nil {
		return nil, err
	}
	u := parseURL(t, rawURL)
	jar.SetCookies(u, []*http.Cookie{{Name: "sessionid", Value: sessionID}})
	return jar, nil
}
