package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// dialWS connects a client to a test server that upgrades and hands the
// server-side conn to handler. handler runs synchronously; the test
// continues after handler returns.
func dialWS(t *testing.T, handler func(ctx context.Context, conn *websocket.Conn)) (*websocket.Conn, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			t.Error(err)
			return
		}
		handler(r.Context(), c)
	}))
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	clientConn, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = clientConn.CloseNow() })
	return clientConn, srv
}

func TestClient_WritePump_DeliversFrame(t *testing.T) {
	hub, mgr, _ := startHub(t, &Config{
		MaxConnectionsPerUser: 10,
		MaxConnectionsTotal:   100,
		PingInterval:          50 * time.Millisecond,
		PongTimeout:           500 * time.Millisecond,
		ConnectionMaxLifetime: 10 * time.Second,
		MaxMessageSize:        4096,
	})

	var serverClient *Client
	clientConn, _ := dialWS(t, func(ctx context.Context, conn *websocket.Conn) {
		serverClient = newPumpClient(hub, conn, 7, "sess-pump")
		hub.register <- serverClient
		waitRegistered(t, mgr, serverClient)
		go serverClient.WritePump(ctx)
		<-ctx.Done()
	})

	// Wait until the pump is running.
	waitFor(t, func() bool { return serverClient != nil }, 1*time.Second, "server client init")
	serverClient.send <- []byte(`{"channel":"hi","payload":{"a":1}}`)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_, data, err := clientConn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != `{"channel":"hi","payload":{"a":1}}` {
		t.Errorf("got %q", data)
	}
}

func TestClient_WritePump_HandlesDisconnect(t *testing.T) {
	hub, mgr, _ := startHub(t, &Config{
		MaxConnectionsPerUser: 10,
		MaxConnectionsTotal:   100,
		PingInterval:          50 * time.Millisecond,
		PongTimeout:           500 * time.Millisecond,
		ConnectionMaxLifetime: 10 * time.Second,
		MaxMessageSize:        4096,
	})

	var serverClient *Client
	clientConn, _ := dialWS(t, func(ctx context.Context, conn *websocket.Conn) {
		serverClient = newPumpClient(hub, conn, 7, "sess-disc")
		hub.register <- serverClient
		waitRegistered(t, mgr, serverClient)
		go serverClient.WritePump(ctx)
		<-ctx.Done()
	})

	waitFor(t, func() bool { return serverClient != nil }, 1*time.Second, "server client init")
	serverClient.disconnect <- disconnectReason{Code: CloseSessionRevoked, Reason: "session_revoked"}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_, _, err := clientConn.Read(ctx)
	if err == nil {
		t.Fatal("expected close error")
	}
	if code := websocket.CloseStatus(err); code != CloseSessionRevoked {
		t.Errorf("close code = %d, want %d (err=%v)", code, CloseSessionRevoked, err)
	}
}

func TestClient_ReadPump_ParsesSubscribeAction(t *testing.T) {
	hub, mgr, _ := startHub(t, &Config{
		MaxConnectionsPerUser: 10,
		MaxConnectionsTotal:   100,
		PingInterval:          50 * time.Millisecond,
		PongTimeout:           500 * time.Millisecond,
		ConnectionMaxLifetime: 10 * time.Second,
		MaxMessageSize:        4096,
	})

	var serverClient *Client
	clientConn, _ := dialWS(t, func(ctx context.Context, conn *websocket.Conn) {
		serverClient = newPumpClient(hub, conn, 7, "sess-read")
		serverClient.allowedChannels["user-7"] = true
		hub.register <- serverClient
		waitRegistered(t, mgr, serverClient)
		go serverClient.ReadPump(ctx)
		<-ctx.Done()
	})

	waitFor(t, func() bool { return serverClient != nil }, 1*time.Second, "server client init")
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"action": "subscribe", "channel": "user-7"})
	if err := clientConn.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatalf("write: %v", err)
	}

	waitFor(t, func() bool { return mgr.count("user-7") == 1 }, 1*time.Second, "subscribe processed")
}

func TestClient_ReadPump_RejectsOversizedFrame(t *testing.T) {
	hub, mgr, _ := startHub(t, &Config{
		MaxConnectionsPerUser: 10,
		MaxConnectionsTotal:   100,
		PingInterval:          50 * time.Millisecond,
		PongTimeout:           500 * time.Millisecond,
		ConnectionMaxLifetime: 10 * time.Second,
		MaxMessageSize:        64,
	})

	var serverClient *Client
	readDone := make(chan struct{})
	clientConn, _ := dialWS(t, func(ctx context.Context, conn *websocket.Conn) {
		serverClient = newPumpClient(hub, conn, 7, "sess-big")
		hub.register <- serverClient
		waitRegistered(t, mgr, serverClient)
		go func() {
			serverClient.ReadPump(ctx)
			close(readDone)
		}()
		<-ctx.Done()
	})

	waitFor(t, func() bool { return serverClient != nil }, 1*time.Second, "server client init")
	huge := strings.Repeat("x", 1024)
	body, _ := json.Marshal(map[string]string{"action": "subscribe", "channel": huge})
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_ = clientConn.Write(ctx, websocket.MessageText, body)

	select {
	case <-readDone:
		// expected: ReadPump exited due to oversized frame
	case <-time.After(1 * time.Second):
		t.Error("ReadPump did not exit on oversized frame")
	}
}
