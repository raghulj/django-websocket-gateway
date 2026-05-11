package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/coder/websocket"
)

// clientFrame is the small {action, channel} envelope clients send.
//
// Anything else is ignored. We deliberately do not echo unknown fields back
// to the client; doing so would risk reflecting attacker-controlled input.
type clientFrame struct {
	Action  string `json:"action"`
	Channel string `json:"channel"`
}

// pumpClient wires a freshly accepted *websocket.Conn into a Client struct
// usable by the hub. The send and disconnect channels are buffered so the
// hub never blocks on a slow consumer.
//
// The connection lifecycle (read + write pumps, lifetime cap, ping/pong) is
// owned by ReadPump and WritePump, which run as two goroutines until the
// connection or the supplied context terminates.
func newPumpClient(hub *Hub, conn *websocket.Conn, userID int64, sessionKey string) *Client {
	return &Client{
		conn:            conn,
		hub:             hub,
		send:            make(chan []byte, 64),
		disconnect:      make(chan disconnectReason, 1),
		userID:          userID,
		sessionKey:      sessionKey,
		connectionID:    generateConnectionID(),
		allowedChannels: make(map[string]bool),
		subscribed:      make(map[string]bool),
		connectedAt:     time.Now(),
		cfg:             hub.cfg,
	}
}

// generateConnectionID returns a short, opaque identifier that appears in
// every log line tied to one connection. We use a fingerprint of the
// connectedAt timestamp + a counter; collisions across a single process are
// vanishingly unlikely.
var connectionCounter = newCounter()

func generateConnectionID() string {
	return formatUserID(connectionCounter.next()) + "-" + formatUserID(time.Now().UnixNano()%1_000_000)
}

// ReadPump consumes inbound frames from the WebSocket. Each frame is parsed
// as a clientFrame; valid subscribe/unsubscribe actions are forwarded to
// the hub. Anything else is silently dropped (we never echo input).
//
// ReadPump owns the read side of conn. It exits — and closes the connection
// — when:
//
//   - The supplied context is cancelled.
//   - conn returns any read error (network error, oversized frame, peer close).
//   - A peer sends a frame larger than cfg.MaxMessageSize (enforced via
//     conn.SetReadLimit).
//
// The deferred close triggers WritePump to exit too (both pumps share conn).
func (c *Client) ReadPump(ctx context.Context) {
	defer func() {
		_ = c.conn.CloseNow()
		// Signal hub-side cleanup.
		c.hub.unregister <- c
	}()

	c.conn.SetReadLimit(c.cfg.MaxMessageSize)
	for {
		if ctx.Err() != nil {
			return
		}
		readCtx, cancel := context.WithTimeout(ctx, c.cfg.PongTimeout)
		_, data, err := c.conn.Read(readCtx)
		cancel()
		if err != nil {
			slog.Debug("read pump exit", "connection_id", c.connectionID, "err", err)
			return
		}
		var frame clientFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			slog.Debug("malformed client frame", "connection_id", c.connectionID)
			continue
		}
		switch frame.Action {
		case "subscribe":
			c.hub.subscribe <- subscription{client: c, channel: frame.Channel}
		case "unsubscribe":
			c.hub.unsubscribe <- subscription{client: c, channel: frame.Channel}
		default:
			slog.Debug("unknown client action", "connection_id", c.connectionID, "action", frame.Action)
		}
	}
}

// WritePump fans hub-side payloads and disconnect signals out to the
// WebSocket. It runs until any of:
//
//   - The supplied context is cancelled.
//   - cfg.ConnectionMaxLifetime elapses (a clean close with code 1000).
//   - A disconnect signal arrives (close with the supplied code/reason).
//   - The send channel is closed by the hub.
//   - A write error occurs.
//
// The write side of conn is owned by this goroutine; ReadPump must not call
// conn.Write itself.
func (c *Client) WritePump(ctx context.Context) {
	pingTicker := time.NewTicker(c.cfg.PingInterval)
	defer pingTicker.Stop()
	lifetime := time.After(c.cfg.ConnectionMaxLifetime - time.Since(c.connectedAt))

	closeWithCode := func(code websocket.StatusCode, reason string) {
		_ = c.conn.Close(code, reason)
	}

	for {
		select {
		case <-ctx.Done():
			closeWithCode(websocket.StatusGoingAway, "shutdown")
			return
		case <-lifetime:
			closeWithCode(websocket.StatusNormalClosure, "lifetime")
			return
		case d := <-c.disconnect:
			if d.Code == 0 {
				return
			}
			closeWithCode(websocket.StatusCode(d.Code), d.Reason)
			return
		case payload, ok := <-c.send:
			if !ok {
				closeWithCode(websocket.StatusNormalClosure, "hub_closed")
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := c.conn.Write(writeCtx, websocket.MessageText, payload)
			cancel()
			if err != nil {
				slog.Debug("write error", "connection_id", c.connectionID, "err", err)
				return
			}
		case <-pingTicker.C:
			pingCtx, cancel := context.WithTimeout(ctx, c.cfg.PongTimeout)
			err := c.conn.Ping(pingCtx)
			cancel()
			if err != nil {
				slog.Debug("ping failed", "connection_id", c.connectionID, "err", err)
				return
			}
		}
	}
}
