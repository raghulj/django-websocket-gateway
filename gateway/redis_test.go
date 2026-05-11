package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// fakeHub captures incoming messages from the redis subscriber for assertions.
type fakeHub struct {
	mu       sync.Mutex
	messages []incomingMessage
	ch       chan incomingMessage
}

func newFakeHub() *fakeHub {
	return &fakeHub{ch: make(chan incomingMessage, 64)}
}

func (h *fakeHub) deliver(m incomingMessage) {
	h.mu.Lock()
	h.messages = append(h.messages, m)
	h.mu.Unlock()
	select {
	case h.ch <- m:
	default:
	}
}

func (h *fakeHub) wait(t *testing.T, timeout time.Duration) incomingMessage {
	t.Helper()
	select {
	case m := <-h.ch:
		return m
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for incoming message")
		return incomingMessage{}
	}
}

// waitForSubscribers polls miniredis until the channel reports want
// subscribers or the deadline expires. miniredis applies subscribe and
// unsubscribe asynchronously after the client call returns.
func waitForSubscribers(t *testing.T, mr *miniredis.Miniredis, channel string, want int) {
	t.Helper()
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if mr.PubSubNumSub(channel)[channel] == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("subscribers on %s = %d, want %d", channel, mr.PubSubNumSub(channel)[channel], want)
}

func newRedisSubscriber(t *testing.T) (*RedisSubscriber, *miniredis.Miniredis, *fakeHub) {
	t.Helper()
	mr := miniredis.RunT(t)
	hub := newFakeHub()
	sub, err := NewRedisSubscriber(&Config{RedisURL: "redis://" + mr.Addr()}, hub.deliver)
	if err != nil {
		t.Fatalf("NewRedisSubscriber: %v", err)
	}
	t.Cleanup(func() { sub.Close() })
	return sub, mr, hub
}

func TestRedisSubscriber_SubscribeOnceCreatesOneSubscription(t *testing.T) {
	sub, mr, _ := newRedisSubscriber(t)
	if err := sub.Subscribe("chan-a"); err != nil {
		t.Fatal(err)
	}
	waitForSubscribers(t, mr, "chan-a", 1)
}

func TestRedisSubscriber_RefcountsRepeatedSubscribes(t *testing.T) {
	sub, mr, _ := newRedisSubscriber(t)
	for i := 0; i < 3; i++ {
		if err := sub.Subscribe("chan-a"); err != nil {
			t.Fatal(err)
		}
	}
	waitForSubscribers(t, mr, "chan-a", 1)

	// Two unsubscribes should not yet remove the Redis subscription.
	if err := sub.Unsubscribe("chan-a"); err != nil {
		t.Fatal(err)
	}
	if err := sub.Unsubscribe("chan-a"); err != nil {
		t.Fatal(err)
	}
	// Brief wait — give miniredis a chance to apply any erroneous unsubscribe.
	time.Sleep(50 * time.Millisecond)
	if count := mr.PubSubNumSub("chan-a")["chan-a"]; count != 1 {
		t.Errorf("after 2 unsubs refcount still > 0, subscribers = %d, want 1", count)
	}

	// Third unsubscribe drops the Redis subscription.
	if err := sub.Unsubscribe("chan-a"); err != nil {
		t.Fatal(err)
	}
	waitForSubscribers(t, mr, "chan-a", 0)
}

func TestRedisSubscriber_PublishForwardsToHub(t *testing.T) {
	sub, mr, hub := newRedisSubscriber(t)
	if err := sub.Subscribe("watch"); err != nil {
		t.Fatal(err)
	}
	waitForSubscribers(t, mr, "watch", 1)

	payload := map[string]any{"channel": "watch", "payload": map[string]any{"x": 1}}
	body, _ := json.Marshal(payload)
	mr.Publish("watch", string(body))

	msg := hub.wait(t, 1*time.Second)
	if msg.Channel != "watch" {
		t.Errorf("Channel = %q, want watch", msg.Channel)
	}
	if string(msg.Payload) != string(body) {
		t.Errorf("Payload mismatch: got %q want %q", msg.Payload, body)
	}
}

func TestRedisSubscriber_PingHealthy(t *testing.T) {
	sub, _, _ := newRedisSubscriber(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := sub.Ping(ctx); err != nil {
		t.Errorf("Ping on healthy redis: %v", err)
	}
}

func TestRedisSubscriber_PingUnreachable(t *testing.T) {
	hub := newFakeHub()
	// Port 1 is reserved and refuses connections; this guarantees a Redis ping fails.
	sub, err := NewRedisSubscriber(&Config{RedisURL: "redis://127.0.0.1:1"}, hub.deliver)
	if err != nil {
		// Construction may succeed even if the server is unreachable (lazy
		// connect); the failure should surface on Ping.
		t.Fatal(err)
	}
	defer sub.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := sub.Ping(ctx); err == nil {
		t.Error("expected Ping error on unreachable redis")
	}
}
