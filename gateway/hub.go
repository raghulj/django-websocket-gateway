package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// WebSocket application-defined close codes used by the gateway.
const (
	// CloseSessionRevoked closes a connection whose Django session has been
	// invalidated (logout, force-logout, ban).
	CloseSessionRevoked = 4401
	// CloseSlowClient closes a connection that fails to drain its send buffer.
	CloseSlowClient = 4408
	// CloseTooManyConnections closes a connection refused because a
	// per-user or per-process cap is exceeded.
	CloseTooManyConnections = 4429
)

// channelRegex restricts client-initiated channel names to a safe charset.
// The `_` prefix is reserved for internal control channels and is rejected
// even when it would otherwise pass the regex.
var channelRegex = regexp.MustCompile(`^[a-zA-Z0-9_:-]{1,128}$`)

// disconnectReason carries the WebSocket close code and human-readable
// reason the hub wants WritePump to apply to a connection.
type disconnectReason struct {
	Code   int
	Reason string
}

// Client is the hub-side view of a connected WebSocket. It is mutated only by
// the hub goroutine after the client is registered. WritePump owns the
// connection-side state (the actual *websocket.Conn) and is responsible for
// turning `send` payloads into frames and `disconnect` signals into close
// frames.
type Client struct {
	// conn is the underlying WebSocket connection. nil in unit tests that
	// exercise hub routing only.
	conn *websocket.Conn
	// hub references the owning Hub so the read pump can post subscribe
	// messages. nil for routing-only tests.
	hub *Hub

	send            chan []byte
	disconnect      chan disconnectReason
	userID          int64
	sessionKey      string
	connectionID    string
	allowedChannels map[string]bool
	subscribed      map[string]bool
	connectedAt     time.Time
	cfg             *Config
}

// counter is a tiny monotonic id source used for connectionID generation.
type counter struct{ v atomic.Int64 }

func newCounter() *counter { return &counter{} }
func (c *counter) next() int64 {
	return c.v.Add(1)
}

// channelManager is the subset of RedisSubscriber the hub depends on.
// Defining it as an interface here keeps hub tests free of a real Redis.
type channelManager interface {
	Subscribe(channel string) error
	Unsubscribe(channel string) error
}

// subscription is a single subscribe/unsubscribe request flowing between
// the WebSocket pumps and the hub goroutine.
type subscription struct {
	client  *Client
	channel string
}

// Hub is the broker that routes pub/sub messages between Redis and
// connected WebSocket clients.
//
// All shared state (clients, channels, userClients) is owned by the
// goroutine running Hub.Run. External callers communicate exclusively
// through the channel fields.
type Hub struct {
	register    chan *Client
	unregister  chan *Client
	subscribe   chan subscription
	unsubscribe chan subscription
	incoming    chan incomingMessage

	clients     map[*Client]bool
	channels    map[string]map[*Client]bool
	userClients map[int64]map[*Client]bool
	manager     channelManager
	cfg         *Config
	log         *slog.Logger

	closeOnce sync.Once
}

// NewHub constructs a Hub bound to cfg and the supplied channelManager.
//
// The hub's goroutine is not started until Run is called. manager may be
// nil; in that case subscribe/unsubscribe operations are local-only (useful
// for tests of the routing logic itself, but unwanted in production).
func NewHub(cfg *Config, manager channelManager) *Hub {
	return &Hub{
		register:    make(chan *Client, 8),
		unregister:  make(chan *Client, 8),
		subscribe:   make(chan subscription, 32),
		unsubscribe: make(chan subscription, 32),
		incoming:    make(chan incomingMessage, 256),
		clients:     make(map[*Client]bool),
		channels:    make(map[string]map[*Client]bool),
		userClients: make(map[int64]map[*Client]bool),
		manager:     manager,
		cfg:         cfg,
		log:         slog.With("component", "hub"),
	}
}

// Run executes the hub loop until ctx is cancelled.
//
// The loop owns every map on the Hub. No other goroutine reads or writes
// them; cross-goroutine communication is via the channel fields exclusively.
// On ctx.Done, the loop exits cleanly without draining (the caller is
// expected to have already initiated a graceful shutdown via WritePumps).
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-h.register:
			h.handleRegister(c)
		case c := <-h.unregister:
			h.handleUnregister(c)
		case s := <-h.subscribe:
			h.handleClientSubscribe(s)
		case s := <-h.unsubscribe:
			h.handleClientUnsubscribe(s)
		case msg := <-h.incoming:
			h.handleIncoming(msg)
		}
	}
}

func (h *Hub) handleRegister(c *Client) {
	if h.cfg.MaxConnectionsTotal > 0 && len(h.clients) >= h.cfg.MaxConnectionsTotal {
		h.refuse(c, CloseTooManyConnections, "max_connections_total")
		return
	}
	if h.cfg.MaxConnectionsPerUser > 0 {
		if existing, ok := h.userClients[c.userID]; ok && len(existing) >= h.cfg.MaxConnectionsPerUser {
			h.refuse(c, CloseTooManyConnections, "max_connections_per_user")
			return
		}
	}

	h.clients[c] = true
	if h.userClients[c.userID] == nil {
		h.userClients[c.userID] = make(map[*Client]bool)
	}
	h.userClients[c.userID][c] = true
	h.log.Info("client registered", "connection_id", c.connectionID, "user_id", c.userID)

	// Auto-subscribe to control channels (bypassing the `_` prefix block).
	h.addSubscription(c, "_session:"+c.sessionKey)
	h.addSubscription(c, "_user:"+formatUserID(c.userID)+":revoke")
}

func (h *Hub) handleUnregister(c *Client) {
	if !h.clients[c] {
		return
	}
	delete(h.clients, c)
	if users, ok := h.userClients[c.userID]; ok {
		delete(users, c)
		if len(users) == 0 {
			delete(h.userClients, c.userID)
		}
	}
	for ch := range c.subscribed {
		h.removeFromChannel(c, ch)
	}
	// Signal disconnect (if not already signalled) and close the send channel.
	select {
	case c.disconnect <- disconnectReason{}:
	default:
	}
	closeChan(c)
	h.log.Info("client unregistered", "connection_id", c.connectionID, "user_id", c.userID)
}

func (h *Hub) handleClientSubscribe(s subscription) {
	if !h.clients[s.client] {
		return
	}
	if !channelRegex.MatchString(s.channel) || s.channel[0] == '_' {
		h.sendError(s.client, s.channel, "invalid_channel")
		return
	}
	if !s.client.allowedChannels[s.channel] {
		h.sendError(s.client, s.channel, "forbidden")
		return
	}
	h.addSubscription(s.client, s.channel)
}

func (h *Hub) handleClientUnsubscribe(s subscription) {
	if !h.clients[s.client] {
		return
	}
	if _, ok := s.client.subscribed[s.channel]; !ok {
		return
	}
	delete(s.client.subscribed, s.channel)
	h.removeFromChannel(s.client, s.channel)
}

// addSubscription is the internal subscribe path used both for control
// channels (at register time) and for client-initiated subscribes (after
// validation).
//
// The hub keeps a per-channel set of subscribed clients; the manager is
// asked to Subscribe only when the first client joins a channel (0→1
// transition). This keeps Redis SUBSCRIBE calls proportional to the number
// of distinct channels, not the number of clients.
func (h *Hub) addSubscription(c *Client, channel string) {
	if c.subscribed[channel] {
		return
	}
	c.subscribed[channel] = true
	clients := h.channels[channel]
	firstSubscriber := clients == nil
	if firstSubscriber {
		clients = make(map[*Client]bool)
		h.channels[channel] = clients
	}
	clients[c] = true
	if firstSubscriber && h.manager != nil {
		if err := h.manager.Subscribe(channel); err != nil {
			h.log.Warn("redis subscribe failed", "channel", channel, "err", err)
		}
	}
}

func (h *Hub) removeFromChannel(c *Client, channel string) {
	clients, ok := h.channels[channel]
	if !ok {
		return
	}
	delete(clients, c)
	if len(clients) == 0 {
		delete(h.channels, channel)
		if h.manager != nil {
			if err := h.manager.Unsubscribe(channel); err != nil {
				h.log.Warn("redis unsubscribe failed", "channel", channel, "err", err)
			}
		}
	}
}

func (h *Hub) handleIncoming(msg incomingMessage) {
	if len(msg.Channel) > 0 && msg.Channel[0] == '_' {
		h.handleControlMessage(msg)
		return
	}
	for c := range h.channels[msg.Channel] {
		h.deliverFrame(c, msg.Payload)
	}
}

// controlPayload mirrors the {"channel": ..., "payload": {"type": ..., "reason": ...}}
// shape used by control messages.
type controlPayload struct {
	Payload struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
	} `json:"payload"`
}

func (h *Hub) handleControlMessage(msg incomingMessage) {
	var parsed controlPayload
	if err := json.Unmarshal(msg.Payload, &parsed); err != nil {
		h.log.Warn("control message: malformed", "channel", msg.Channel)
		return
	}
	if parsed.Payload.Type != "revoke" {
		return
	}
	// Find candidates by channel pattern.
	candidates := h.channels[msg.Channel]
	for c := range candidates {
		h.disconnectClient(c, CloseSessionRevoked, "session_revoked")
	}
}

func (h *Hub) deliverFrame(c *Client, payload []byte) {
	select {
	case c.send <- payload:
	default:
		// Slow client: full buffer. Disconnect and unregister.
		h.disconnectClient(c, CloseSlowClient, "slow_client")
		// Trigger unregister via the hub channel to keep all map mutations
		// on the hub goroutine path. Use non-blocking send: handleUnregister
		// is reached on the next loop iteration.
		go func(c *Client) { h.unregister <- c }(c)
	}
}

func (h *Hub) sendError(c *Client, channel, reason string) {
	frame, _ := json.Marshal(map[string]any{
		"type":    "error",
		"channel": channel,
		"reason":  reason,
	})
	h.deliverFrame(c, frame)
}

func (h *Hub) refuse(c *Client, code int, reason string) {
	h.log.Warn("refused client", "connection_id", c.connectionID, "user_id", c.userID, "reason", reason)
	h.disconnectClient(c, code, reason)
}

// disconnectClient signals WritePump to close the connection with the
// supplied code. It is safe to call multiple times — the disconnect channel
// is buffered (size 1) and a second send is dropped.
func (h *Hub) disconnectClient(c *Client, code int, reason string) {
	select {
	case c.disconnect <- disconnectReason{Code: code, Reason: reason}:
	default:
	}
}

func closeChan(c *Client) {
	defer func() { _ = recover() }() // tolerate double-close in tests
	close(c.send)
}

func formatUserID(id int64) string {
	return strconv.FormatInt(id, 10)
}
