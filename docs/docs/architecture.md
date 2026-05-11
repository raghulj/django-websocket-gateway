# Architecture

## Components

| Component | Language | Responsibility |
|---|---|---|
| Django (web) | Python | Owns users, sessions, business logic. Holds the authoritative answer to "is this session valid? what channels may it see?" |
| Go gateway | Go | Holds WebSocket connections. Calls Django to authenticate each connection. Subscribes to Redis pub/sub and fans messages out to clients. |
| Redis | — | Pub/sub backplane between Django (publisher) and one or more gateway processes (subscribers). |
| Caddy (or any reverse proxy) | — | Terminates TLS, routes `/ws/*` to the gateway and everything else to Django, blocks public traffic to `/internal/*`. |

## Data flow

### Connection

1. Browser opens a WebSocket to `wss://app.example.com/ws/`, sending its
   Django `sessionid` cookie.
2. Caddy forwards the upgrade to the Go gateway.
3. The gateway POSTs to Django's `/internal/ws-auth/` with the cookie
   value in `X-Forwarded-Session` and the configured shared secret in
   `X-Internal-Auth`.
4. Django (via `websocket_gateway.views.ws_auth`) decodes the session,
   looks up the user, invokes the `AUTHORIZATION_CALLBACK`, and returns
   `{user_id, username, allowed_channels}`.
5. The gateway upgrades the connection, registers the client with its
   hub, and auto-subscribes the client to two internal control channels:
   `_session:{key}` and `_user:{id}:revoke`.

### Publish

1. Django (a view, signal, or Celery task) calls
   `websocket_gateway.publish("user-42", {"x": 1})`.
2. The helper sends one Redis `PUBLISH` with the envelope
   `{"channel": "user-42", "payload": {"x": 1}}`.
3. Every gateway process subscribed to `user-42` receives the message,
   looks up its locally-connected clients on that channel, and writes a
   frame to each.

### Logout / revocation

1. Django's `user_logged_out` signal fires (e.g., from `LogoutView`).
2. `websocket_gateway.revocation._revoke_on_logout` publishes on
   `_session:{key}` with `{"type": "revoke", "reason": "logout"}`.
3. The gateway sees the message on the control channel, finds the
   matching connection, and closes the WebSocket with code 4401.
4. The browser JS client sees the 4401 and stops trying to reconnect.

## Design decisions

### Why a separate Go process?

WebSockets are long-lived connections. Each one occupies a thread/process
slot in WSGI servers; holding thousands at once is painful in Python and
trivial in Go. The gateway is a single goroutine per connection plus one
shared broker goroutine — comfortable on a 1 vCPU container.

### Why Redis pub/sub?

The gateway is **horizontally scalable**: run as many replicas as you
want, each subscribed to Redis. A publish reaches every replica that has
local subscribers for the channel. No sticky sessions needed.

Pub/sub is fire-and-forget — clients that are disconnected at the time
of publish miss the message. This is the explicit v1 trade-off in
exchange for simplicity (no per-user mailboxes, no replay log). See
[Threat model](threat-model.md) and [Publishing](publishing.md).

### Why a dedicated secret instead of Django's SECRET_KEY?

`SECRET_KEY` rotates with risks (signed cookies, password reset tokens,
CSRF tokens all rely on it). The Django↔gateway link is a separate trust
boundary with its own rotation cadence. The validator refuses to start
when the two values are equal.

### Why no message persistence / presence / replay in v1?

Each of those is a substantial feature with its own state, storage
trade-offs, and consistency story. v1 ships the smallest useful slice:
fan-out for live clients. Future versions can layer persistence on top
without breaking the protocol.

### Why single-goroutine hub state?

The hub owns three maps: clients, channels, userClients. Concurrent
mutation would require fine-grained locking and is famously easy to get
wrong. Instead, every mutation happens inside one goroutine; cross-
goroutine communication is via channels. The `-race` test suite verifies
the discipline.

## What the gateway *never* does

- **Reads Django's database directly.** Authentication is RPC over HTTP.
- **Decides what channels a user can subscribe to.** Django returns the
  allow-list; the gateway enforces it.
- **Logs the shared secret or raw session IDs.** Failures log a short
  SHA-256 prefix of the session ID at most.
- **Survives a malformed `SHA256SUMS`.** The downloader refuses to run
  any binary whose checksum it cannot verify.
