package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// fakeChannelManager records Subscribe/Unsubscribe calls so hub tests can
// assert on refcount transitions without bringing up a real Redis.
type fakeChannelManager struct {
	mu          sync.Mutex
	activeChans map[string]int
}

func newFakeChannelManager() *fakeChannelManager {
	return &fakeChannelManager{activeChans: make(map[string]int)}
}

func (f *fakeChannelManager) Subscribe(channel string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activeChans[channel]++
	return nil
}

func (f *fakeChannelManager) Unsubscribe(channel string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activeChans[channel]--
	if f.activeChans[channel] <= 0 {
		delete(f.activeChans, channel)
	}
	return nil
}

func (f *fakeChannelManager) count(channel string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.activeChans[channel]
}

func newTestClient(userID int64, sessionKey string, allowed ...string) *Client {
	allowedMap := make(map[string]bool, len(allowed))
	for _, c := range allowed {
		allowedMap[c] = true
	}
	return &Client{
		send:            make(chan []byte, 4),
		disconnect:      make(chan disconnectReason, 1),
		userID:          userID,
		sessionKey:      sessionKey,
		connectionID:    "conn-" + sessionKey,
		allowedChannels: allowedMap,
		subscribed:      make(map[string]bool),
		connectedAt:     time.Now(),
	}
}

func startHub(t *testing.T, cfg *Config) (*Hub, *fakeChannelManager, context.CancelFunc) {
	t.Helper()
	if cfg == nil {
		cfg = &Config{MaxConnectionsPerUser: 10, MaxConnectionsTotal: 100}
	}
	mgr := newFakeChannelManager()
	hub := NewHub(cfg, mgr)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		hub.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return hub, mgr, cancel
}

// waitRegistered blocks until c's control channels show up in the channel
// manager, signalling the hub has finished processing the register message.
// This is the only stable sync point because channel state mutation order
// across (register, subscribe) is not guaranteed by Go's select.
func waitRegistered(t *testing.T, mgr *fakeChannelManager, c *Client) {
	t.Helper()
	sessionCh := "_session:" + c.sessionKey
	userCh := "_user:" + formatUserID(c.userID) + ":revoke"
	waitFor(t, func() bool {
		return mgr.count(sessionCh) >= 1 && mgr.count(userCh) >= 1
	}, 500*time.Millisecond, "client registered")
}

func expectFrame(t *testing.T, c *Client, timeout time.Duration) []byte {
	t.Helper()
	select {
	case b := <-c.send:
		return b
	case <-time.After(timeout):
		t.Fatalf("no frame received within %v", timeout)
		return nil
	}
}

func expectDisconnect(t *testing.T, c *Client, code int, timeout time.Duration) disconnectReason {
	t.Helper()
	select {
	case d := <-c.disconnect:
		if d.Code != code {
			t.Errorf("disconnect code = %d, want %d (reason=%q)", d.Code, code, d.Reason)
		}
		return d
	case <-time.After(timeout):
		t.Fatalf("no disconnect within %v", timeout)
		return disconnectReason{}
	}
}

func TestHub_Register_AutoSubscribesControlChannels(t *testing.T) {
	hub, mgr, _ := startHub(t, nil)
	c := newTestClient(42, "sess-abc")

	hub.register <- c
	// Give the hub a moment to process.
	waitFor(t, func() bool {
		return mgr.count("_session:sess-abc") == 1 && mgr.count("_user:42:revoke") == 1
	}, 500*time.Millisecond, "control channels auto-subscribed")
}

func TestHub_Register_EnforcesPerUserCap(t *testing.T) {
	cfg := &Config{MaxConnectionsPerUser: 2, MaxConnectionsTotal: 100}
	hub, _, _ := startHub(t, cfg)

	for i := 0; i < 2; i++ {
		hub.register <- newTestClient(7, "sess-"+string(rune('a'+i)))
	}
	excess := newTestClient(7, "sess-too-many")
	hub.register <- excess
	expectDisconnect(t, excess, 4429, 500*time.Millisecond)
}

func TestHub_Register_EnforcesTotalCap(t *testing.T) {
	cfg := &Config{MaxConnectionsPerUser: 100, MaxConnectionsTotal: 2}
	hub, _, _ := startHub(t, cfg)

	for i := 0; i < 2; i++ {
		hub.register <- newTestClient(int64(i), "sess-"+string(rune('a'+i)))
	}
	excess := newTestClient(99, "sess-overflow")
	hub.register <- excess
	expectDisconnect(t, excess, 4429, 500*time.Millisecond)
}

func TestHub_Subscribe_RejectsUnderscorePrefix(t *testing.T) {
	hub, mgr, _ := startHub(t, nil)
	c := newTestClient(1, "sess-1", "user-1")
	hub.register <- c
	waitRegistered(t, mgr, c)

	hub.subscribe <- subscription{client: c, channel: "_secret-leak"}

	frame := expectFrame(t, c, 500*time.Millisecond)
	var got map[string]any
	if err := json.Unmarshal(frame, &got); err != nil {
		t.Fatalf("frame not json: %v", err)
	}
	if got["type"] != "error" || got["channel"] != "_secret-leak" {
		t.Errorf("unexpected frame: %v", got)
	}
}

func TestHub_Subscribe_RejectsForbidden(t *testing.T) {
	hub, mgr, _ := startHub(t, nil)
	c := newTestClient(1, "sess-1", "user-1")
	hub.register <- c
	waitRegistered(t, mgr, c)
	hub.subscribe <- subscription{client: c, channel: "other-user-channel"}

	frame := expectFrame(t, c, 500*time.Millisecond)
	var got map[string]any
	_ = json.Unmarshal(frame, &got)
	if got["type"] != "error" || got["reason"] != "forbidden" {
		t.Errorf("expected forbidden error, got %v", got)
	}
}

func TestHub_Subscribe_AllowedChannelTriggersRedisSubscribe(t *testing.T) {
	hub, mgr, _ := startHub(t, nil)
	c := newTestClient(1, "sess-1", "user-1")
	hub.register <- c
	waitRegistered(t, mgr, c)
	hub.subscribe <- subscription{client: c, channel: "user-1"}

	waitFor(t, func() bool { return mgr.count("user-1") == 1 }, 500*time.Millisecond, "redis subscribe")
}

func TestHub_Subscribe_InvalidRegexRejected(t *testing.T) {
	hub, mgr, _ := startHub(t, nil)
	c := newTestClient(1, "sess-1", "user-1")
	hub.register <- c
	waitRegistered(t, mgr, c)
	hub.subscribe <- subscription{client: c, channel: "has spaces"}

	frame := expectFrame(t, c, 500*time.Millisecond)
	var got map[string]any
	_ = json.Unmarshal(frame, &got)
	if got["type"] != "error" {
		t.Errorf("expected error frame, got %v", got)
	}
}

func TestHub_Incoming_DeliversToAllSubscribers(t *testing.T) {
	hub, mgr, _ := startHub(t, nil)
	c1 := newTestClient(1, "s1", "room-x")
	c2 := newTestClient(2, "s2", "room-x")
	hub.register <- c1
	hub.register <- c2
	waitRegistered(t, mgr, c1)
	waitRegistered(t, mgr, c2)
	hub.subscribe <- subscription{client: c1, channel: "room-x"}
	hub.subscribe <- subscription{client: c2, channel: "room-x"}
	waitFor(t, func() bool { return mgr.count("room-x") == 1 }, 500*time.Millisecond, "sub done")

	hub.incoming <- incomingMessage{Channel: "room-x", Payload: []byte(`{"channel":"room-x","payload":{"hi":1}}`)}

	for _, c := range []*Client{c1, c2} {
		_ = expectFrame(t, c, 500*time.Millisecond)
	}
}

func TestHub_Incoming_SessionRevokeDisconnects(t *testing.T) {
	hub, mgr, _ := startHub(t, nil)
	c := newTestClient(42, "sess-abc")
	hub.register <- c
	waitRegistered(t, mgr, c)

	hub.incoming <- incomingMessage{
		Channel: "_session:sess-abc",
		Payload: []byte(`{"channel":"_session:sess-abc","payload":{"type":"revoke","reason":"logout"}}`),
	}
	expectDisconnect(t, c, 4401, 500*time.Millisecond)
}

func TestHub_Incoming_UserRevokeDisconnectsAllUserClients(t *testing.T) {
	hub, mgr, _ := startHub(t, nil)
	c1 := newTestClient(42, "s1")
	c2 := newTestClient(42, "s2")
	other := newTestClient(99, "s99")
	hub.register <- c1
	hub.register <- c2
	hub.register <- other
	waitRegistered(t, mgr, c1)
	waitRegistered(t, mgr, c2)
	waitRegistered(t, mgr, other)

	hub.incoming <- incomingMessage{
		Channel: "_user:42:revoke",
		Payload: []byte(`{"channel":"_user:42:revoke","payload":{"type":"revoke","reason":"force_logout"}}`),
	}

	expectDisconnect(t, c1, 4401, 500*time.Millisecond)
	expectDisconnect(t, c2, 4401, 500*time.Millisecond)
	// other should NOT receive a disconnect
	select {
	case d := <-other.disconnect:
		t.Errorf("unaffected client disconnected: %+v", d)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHub_SlowClient_DisconnectedOnFullBuffer(t *testing.T) {
	hub, mgr, _ := startHub(t, nil)
	// send buffer = 1; we'll publish 5 messages quickly without draining.
	c := newTestClient(1, "s1", "room")
	c.send = make(chan []byte, 1)
	hub.register <- c
	waitRegistered(t, mgr, c)
	hub.subscribe <- subscription{client: c, channel: "room"}
	waitFor(t, func() bool { return mgr.count("room") == 1 }, 500*time.Millisecond, "sub")

	for i := 0; i < 5; i++ {
		hub.incoming <- incomingMessage{
			Channel: "room",
			Payload: []byte(`{"channel":"room","payload":{}}`),
		}
	}
	// One frame in c.send; the rest cause the hub to disconnect the slow client.
	expectDisconnect(t, c, 4408, 1*time.Second)
}

func TestHub_LastUnsub_RedisUnsubscribe(t *testing.T) {
	hub, mgr, _ := startHub(t, nil)
	c := newTestClient(1, "s1", "room")
	hub.register <- c
	waitRegistered(t, mgr, c)
	hub.subscribe <- subscription{client: c, channel: "room"}
	waitFor(t, func() bool { return mgr.count("room") == 1 }, 500*time.Millisecond, "sub")

	hub.unsubscribe <- subscription{client: c, channel: "room"}
	waitFor(t, func() bool { return mgr.count("room") == 0 }, 500*time.Millisecond, "unsub")
}

func waitFor(t *testing.T, predicate func() bool, timeout time.Duration, label string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting: %s", label)
}
