package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// incomingMessage is the in-process representation of a Redis pub/sub message
// after it has been received by RedisSubscriber and forwarded to the hub.
//
// Payload is the raw JSON body Django (or any other publisher) wrote into
// Redis; the hub parses it as needed. Channel is the Redis channel name.
type incomingMessage struct {
	Channel string
	Payload []byte
}

// incomingHandler is the callback shape RedisSubscriber invokes per message.
// The hub registers its own delivery function under this signature.
type incomingHandler func(incomingMessage)

// RedisSubscriber owns the single Redis pub/sub connection.
//
// The subscriber refcounts logical subscribers per channel: callers may
// invoke Subscribe and Unsubscribe many times, but the underlying Redis
// SUBSCRIBE happens only on the 0→1 transition and UNSUBSCRIBE only on the
// 1→0 transition. This keeps the pub/sub connection small and predictable.
//
// All public methods are safe for concurrent use.
type RedisSubscriber struct {
	client *redis.Client
	pubsub *redis.PubSub

	mu          sync.Mutex
	deliver     incomingHandler
	activeChans map[string]int
	closed      bool
	started     bool
}

// NewRedisSubscriber dials Redis using cfg.RedisURL and prepares a pub/sub
// session. Dialing is lazy in go-redis; the first command (Ping, Subscribe)
// surfaces connection errors.
//
// The optional deliver callback may be supplied here or attached later via
// SetDeliver. The subscriber does not start reading until Start is called
// — letting the caller resolve construction-order cycles (e.g., the hub
// references the subscriber and vice-versa).
func NewRedisSubscriber(cfg *Config, deliver incomingHandler) (*RedisSubscriber, error) {
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	client := redis.NewClient(opts)
	pubsub := client.Subscribe(context.Background())
	r := &RedisSubscriber{
		client:      client,
		pubsub:      pubsub,
		deliver:     deliver,
		activeChans: make(map[string]int),
	}
	if deliver != nil {
		r.Start()
	}
	return r, nil
}

// SetDeliver attaches (or replaces) the deliver callback. Safe to call
// before Start; if called after, the new callback applies to subsequent
// messages.
func (r *RedisSubscriber) SetDeliver(deliver incomingHandler) {
	r.mu.Lock()
	r.deliver = deliver
	r.mu.Unlock()
}

// Start spawns the read-loop goroutine. Subsequent calls are no-ops.
func (r *RedisSubscriber) Start() {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	r.mu.Unlock()
	go r.readLoop()
}

// Subscribe increments the refcount for channel. The first subscriber on a
// channel triggers a Redis SUBSCRIBE; subsequent ones are local-only.
func (r *RedisSubscriber) Subscribe(channel string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("redis subscriber closed")
	}
	r.activeChans[channel]++
	if r.activeChans[channel] == 1 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := r.pubsub.Subscribe(ctx, channel); err != nil {
			r.activeChans[channel]--
			return fmt.Errorf("redis subscribe %q: %w", channel, err)
		}
	}
	return nil
}

// Unsubscribe decrements the refcount for channel. The last unsubscribe
// triggers a Redis UNSUBSCRIBE. Unknown channels are a no-op.
func (r *RedisSubscriber) Unsubscribe(channel string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	if _, ok := r.activeChans[channel]; !ok {
		return nil
	}
	r.activeChans[channel]--
	if r.activeChans[channel] > 0 {
		return nil
	}
	delete(r.activeChans, channel)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.pubsub.Unsubscribe(ctx, channel); err != nil {
		return fmt.Errorf("redis unsubscribe %q: %w", channel, err)
	}
	return nil
}

// Ping verifies the Redis connection is alive. Used by /healthz.
func (r *RedisSubscriber) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

// Close terminates the pub/sub session and closes the underlying connection.
// Idempotent.
func (r *RedisSubscriber) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.mu.Unlock()
	_ = r.pubsub.Close()
	return r.client.Close()
}

// readLoop forwards incoming messages until the pub/sub is closed.
//
// Each Redis message is delivered as an incomingMessage on the deliver
// callback. If deliver has not yet been set, the message is dropped. The
// loop exits when Close() drops the pub/sub channel.
func (r *RedisSubscriber) readLoop() {
	ch := r.pubsub.Channel()
	for msg := range ch {
		r.mu.Lock()
		deliver := r.deliver
		r.mu.Unlock()
		if deliver != nil {
			deliver(incomingMessage{Channel: msg.Channel, Payload: []byte(msg.Payload)})
		}
	}
	slog.Debug("redis read loop exited")
}
