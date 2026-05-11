# Deployment

The gateway is one extra process alongside your Django stack. Run it
however you run your other workers: systemd, supervisord, Docker
Compose, Kubernetes, fly.io, Railway, your platform of choice.

## With Docker Compose

The repo ships a reference `docker-compose.example.yml` and
`Caddyfile.example`. Copy them into your project, point the `django`
service at your image, and fill `.env`:

```bash
cp docker-compose.example.yml docker-compose.yml
cp Caddyfile.example Caddyfile
cp .env.example .env
# edit .env
docker compose up -d
```

What you get:

- TLS termination via Caddy (auto-issued via Let's Encrypt for the
  configured `DOMAIN`).
- `/internal/*` is **404'd publicly** at the reverse proxy. Only the
  gateway, on the internal Docker network, can reach it.
- The gateway runs from the GHCR image, with `nofile=65535` so it can
  hold tens of thousands of sockets.

## With the Python package only

If you prefer not to use the GHCR image, `python manage.py runwsgateway`
downloads the platform-appropriate binary from GitHub Releases on first
use:

```bash
python manage.py runwsgateway
```

The binary lands at `websocket_gateway/bin/gateway-{os}-{arch}`. SHA-256
is verified before the file is made executable. Subsequent invocations
reuse the cached binary.

For air-gapped builds, set `WS_GATEWAY_BINARY_PATH` to point at a
locally-built or pre-shipped binary, or `WS_GATEWAY_SKIP_DOWNLOAD=1` to
refuse the download entirely (combined with `WS_GATEWAY_BINARY_PATH`).

## Scaling

The gateway is **horizontally scalable**. Run as many replicas as you
need; each one subscribes to Redis and receives every published message
for channels it has local subscribers on.

- No sticky sessions are required (each connection's lifetime stays on
  the replica it landed on).
- Load-balancing decisions can be plain round-robin.
- Per-replica caps are enforced via `MAX_CONNECTIONS_TOTAL`. Set this
  generously; the binary itself is cheap (~15 MB resident set).

## TLS

Caddy in the reference stack auto-issues certificates. For a CDN or
load-balancer that terminates TLS upstream, point the gateway directly
behind it; the gateway speaks plain HTTP/WebSocket on `:8080`.

## Health checks

The gateway exposes `/healthz`:

| Condition | Status |
|---|---|
| Ready | 200 "ok" |
| SIGTERM received | 503 "shutting down" |
| Redis Ping fails within 1 s | 503 "redis unreachable" |

Wire this into your platform's liveness AND readiness probes — there is
no separate `/readyz` endpoint in v1.

## Graceful shutdown

On `SIGTERM` or `SIGINT`:

1. `/healthz` flips to 503 immediately so the load balancer drains.
2. The HTTP server stops accepting new connections.
3. In-flight WebSockets close cleanly within 10 s.
4. The Redis pub/sub connection closes.

If the platform sends `SIGKILL` after a short timeout, connections drop
abruptly and clients reconnect to another replica.

## What goes in the public TLS path

| Path | Target |
|---|---|
| `/` | Django |
| `/static/*` | Django staticfiles (or your CDN) |
| `/ws/*` | Gateway |
| `/internal/*` | **Blocked at the proxy.** The gateway calls Django on the internal network. |
