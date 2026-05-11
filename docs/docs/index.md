# django-websocket-gateway

A drop-in real-time messaging layer for Django, similar in spirit to Pusher
but self-hosted. Installable as a Python package; the actual WebSocket
gateway is a small Go service that runs as a separate process or container
alongside Django.

```bash
pip install django-websocket-gateway
```

## What you get

- A Go WebSocket gateway, downloaded automatically from GitHub Releases on first run.
- A Django app providing the auth endpoint the gateway calls during the WebSocket handshake.
- A [`publish(channel, payload)`](publishing.md) helper for sending from views, signals, or background workers.
- A `runwsgateway` management command that launches the gateway as a separate process.
- Automatic session-revocation on logout.
- A small JavaScript client with reconnect-with-backoff.

The mental model is the same as Celery or RQ: you run a worker process
alongside Django. Here the worker happens to be written in Go and holds
WebSocket connections instead of consuming tasks.

## Architecture at a glance

```
                 ┌──────────────┐
                 │   Browser    │
                 └──────┬───────┘
                        │ WebSocket (wss://app.example.com/ws/)
                        │ + Django sessionid cookie
                        ▼
                 ┌──────────────┐         ┌──────────────────┐
                 │    Caddy     │────────▶│   Django (web)   │
                 │ (TLS, proxy) │  HTTPS  │  + workers       │
                 └──────┬───────┘         └────────┬─────────┘
                        │ ws://                    │
                        ▼                          │ PUBLISH
                 ┌──────────────┐                  │
                 │  Go Gateway  │◀─── SUBSCRIBE ───┤
                 │ (runwsgateway)                  ▼
                 └──────┬───────┘         ┌──────────────────┐
                        │ POST            │      Redis       │
                        └────────────────▶│  (pub/sub)       │
                          /internal/      └──────────────────┘
                          ws-auth/
```

See [Architecture](architecture.md) for the full reasoning.

## Next steps

1. [Quickstart](quickstart.md) — five-minute install-to-running guide.
2. [Authentication](authentication.md) — the dedicated shared secret.
3. [Threat model](threat-model.md) — what's covered and what isn't.
