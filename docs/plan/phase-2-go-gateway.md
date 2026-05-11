# Phase 2 — Go gateway

← [Plan index](README.md)

## Goal

Build the standalone Go WebSocket gateway end-to-end, test-first. The gateway accepts WebSocket upgrades, validates each via Django's `/internal/ws-auth/`, holds connections in a single-goroutine-owned hub, and fans out messages from Redis. At the end of this phase, `go test ./...` is green and a local binary can talk to a Django dev server + Redis.

## Prerequisites

- Phase 0 complete (`go.mod`, deps, `.golangci.yml`).
- Phase 1 useful but not strictly required — you can stub Django's `/internal/ws-auth/` with `httptest.Server` in unit tests.

## TDD discipline

Go tests live next to the file they test (`hub_test.go` beside `hub.go`). Use stdlib `testing` only. For HTTP boundary tests use `net/http/httptest`. For Redis use `miniredis` (`github.com/alicebob/miniredis/v2`) — add as a test-only dep.

Cycle every task: write `_test.go` → see it fail → write the implementation → green → refactor.

## Hard-rule reminders for this phase

- **Hub state mutates only inside `Run()`.** External callers send on channels; never touch a map directly.
- **Goroutines exit on `ctx.Done()`.** Every `go func()` must select on the context.
- **Secret never logged.** Hash session IDs (`sha256(...)[:8]`) when you need an identifier in logs.
- **`crypto/subtle.ConstantTimeCompare`** for secret compares. The pre-commit hook will block `==`.
- **Client subscriptions to channels starting with `_` are rejected** — but the hub *itself* auto-subscribes clients to their `_session:{key}` and `_user:{id}:revoke` control channels at register time.

## Tasks

### 2.1 `gateway/config.go` (Step 1) — `config_test.go` ✓ done
- [x] **Tests:**
  - Missing required env (`REDIS_URL`, `DJANGO_AUTH_URL`, `INTERNAL_AUTH_SECRET`, `ALLOWED_ORIGINS`) → error names the missing key.
  - `INTERNAL_AUTH_SECRET` < 32 chars → error message is exactly `"INTERNAL_AUTH_SECRET must be at least 32 characters"`; assert the actual value is NOT in the error string and the length is NOT mentioned numerically.
  - Bad duration string (`PING_INTERVAL=garbage`) → error names the field.
  - All-defaults case: only required env set → struct has documented defaults.
  - `ALLOWED_ORIGINS` parses comma-separated.
- [x] **Implementation:** per Step 1.

### 2.2 `gateway/auth.go` (Step 2) — `auth_test.go`
- [x] **Tests** (`httptest.Server` doubling as Django):
  - 200 + `{"authenticated":true, user_id, username, allowed_channels}` → returns populated `AuthResult` with `SessionKey` echoed back.
  - 401 → returns `ErrUnauthenticated`.
  - 200 + `{"authenticated":false}` → returns `ErrUnauthenticated`.
  - 500 → returns `ErrAuthFailed` wrapping cause.
  - Malformed JSON → `ErrAuthFailed`.
  - Server slower than 5s → request times out as `ErrAuthFailed`.
  - **Log records do not contain the secret value or the raw session ID.**
- [x] **Implementation:** per Step 2.

### 2.3 `gateway/redis.go` (Step 5) — `redis_test.go` (miniredis)
- [x] **Tests:**
  - `Subscribe("chan-a")` once → Redis sees one subscription; refcount = 1.
  - Two `Subscribe("chan-a")` → still one Redis SUBSCRIBE; refcount = 2.
  - `Unsubscribe` brings refcount to 0 → Redis sees UNSUBSCRIBE.
  - Publishing to a subscribed channel forwards `{channel, payload}` JSON to `hub.incoming`.
  - Disconnect → reconnect backoff: 1s, 2s, 4s, capped 30s. (Use a fake clock or shorter constants under a test flag.)
  - On reconnect, all `activeChans` are resubscribed.
  - `Ping(ctx)` returns nil when Redis is up, error when not.
- [x] **Implementation:** per Step 5. Keep the goroutine count bounded; one reader, one reconnect loop.

### 2.4 `gateway/hub.go` (Step 3) — `hub_test.go`
- [x] **Tests:**
  - `register` a client → `clients` map contains it; client is auto-subscribed to `_session:{sessionKey}` and `_user:{userID}:revoke` via the redis fake.
  - Registering more than `MaxConnectionsPerUser` clients for the same user → excess client receives close code 4429 and is not added.
  - Registering past `MaxConnectionsTotal` → 4429.
  - Client-initiated `subscribe` to `_anything` → error frame `{"type":"error","channel":"_anything","reason":"forbidden"}`, no Redis subscription.
  - Client-initiated `subscribe` to a channel not in `allowedChannels` → forbidden error frame.
  - Client-initiated `subscribe` to allowed channel → Redis sub created (refcount 0→1).
  - `incoming` on a normal channel → all subscribed clients receive the bytes.
  - `incoming` on `_session:{key}` with `{"type":"revoke"}` → client whose `sessionKey` matches is closed with code 4401 reason `"session_revoked"`.
  - `incoming` on `_user:{id}:revoke` with `{"type":"revoke"}` → all of user `id`'s clients closed with 4401.
  - Slow client (full `send` buffer) → that client is unregistered; other subscribers still receive.
  - On `unregister`, last subscriber leaving a channel → Redis UNSUBSCRIBE.
- [x] **Implementation:** per Step 3. Verify with `go test -race ./...` that there are no data races.

### 2.5 `gateway/client.go` (Step 4) — `client_test.go`
- [x] **Tests** (use `net/http/httptest` + websocket dial loopback):
  - `ReadPump` enforces `MaxMessageSize`: oversized frame closes the connection.
  - `ReadPump` rejects malformed JSON and unknown `action` (no panic; just ignored or logged).
  - `ReadPump` validates the channel regex; bad channel → no hub message.
  - `ReadPump` rejects `_`-prefix even with the regex passing.
  - `WritePump` sends ping at `PingInterval`; missing pong within `PongTimeout` triggers exit (the read pump detects it via the read deadline).
  - `WritePump` exits at `ConnectionMaxLifetime` and closes with code 1000.
  - Every log line includes `connection_id`; lines about the user include `user_id`.
- [x] **Implementation:** per Step 4.

### 2.6 `gateway/health.go` (Step 6) — `health_test.go`
- [x] **Tests:**
  - Healthy Redis → 200, body `"ok"`.
  - `shuttingDown.Store(true)` → 503 "shutting down".
  - Redis ping fails → 503 "redis unreachable".
  - Timeout: Redis hangs past 1s → 503.
- [x] **Implementation:** per Step 6.

### 2.7 `gateway/main.go` (Step 7) — integration `main_test.go`
- [x] **Tests:**
  - With miniredis + httptest Django stub: dial `/ws/` with a sessionid cookie, hub registers client, `publish` from Redis side delivers payload to the websocket client.
  - Origin check: request with origin not in `ALLOWED_ORIGINS` is rejected.
  - SIGTERM handler: send SIGTERM → server shuts down within 10s; `shuttingDown` becomes true; subsequent `/healthz` returns 503.
- [x] **Implementation:** per Step 7. Keep `wsHandler` in a separate function for testability.

### 2.8 `gateway/Dockerfile` (Step 8)
- [x] Per Step 8.
- [x] **Acceptance:** `docker build gateway/` succeeds; image runs and prints config errors clearly when env is missing.

### 2.9 Coverage & race audit
- [x] `go test -race ./...` clean.
- [x] `go test -cover ./...` ≥ 80% on `hub.go`, `auth.go`, `redis.go`.
- [x] `golangci-lint run` clean.

## Definition of done for Phase 2

- **Every exported Go identifier has a complete godoc comment** starting with the identifier name. Document parameters, returns, errors, and goroutine-safety. These render into the Go API reference page in Phase 5.
- `go test -race ./gateway/...` green.
- `gofmt -s -l gateway/` empty (no diffs).
- `go vet ./gateway/...` clean.
- `golangci-lint run` clean.
- Manual end-to-end: run `redis-server`, run a tiny Python script serving `/internal/ws-auth/` with a hardcoded response, run the gateway binary, connect with `websocat`, see auth + subscribe + publish work.

## Notes

- Don't implement the binary downloader here — Phase 3 owns it.
- The release pipeline that produces the binary lives in Phase 6.
