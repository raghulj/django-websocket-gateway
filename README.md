# django-websocket-gateway

A drop-in real-time messaging layer for Django, similar in spirit to Pusher but self-hosted. Installable as a Python package; the actual WebSocket gateway is a small Go service that runs as a separate process or container alongside Django.

```bash
pip install django-websocket-gateway
```

📚 **[Full documentation →](https://raghulj.github.io/django-websocket-gateway/)**

## What it gives you

- A Go WebSocket gateway, downloaded automatically from GitHub Releases on first run.
- A Django app providing the auth endpoint the gateway calls during the WebSocket handshake.
- A `publish(channel, payload)` helper for sending from views, signals, or background workers.
- A `runwsgateway` management command that launches the gateway as a separate process.
- Automatic session-revocation on logout.
- A small JavaScript client with reconnect-with-backoff.

The mental model is the same as Celery or RQ: you run a worker process alongside Django. Here the worker happens to be written in Go and holds WebSocket connections instead of consuming tasks.

## Architecture

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

- **Caddy** terminates TLS, serves Django on `/`, reverse-proxies `/ws/*` to the gateway, blocks public traffic to `/internal/*`.
- **Django** owns users, sessions, authorization. The package provides one private endpoint protected by a dedicated shared secret.
- **Go gateway** holds WebSocket connections; validates each via Django at handshake; fans out messages from Redis.
- **Redis** is the pub/sub backplane. Django publishes; every gateway instance subscribes.

## Quick start

### 1. Install

```bash
pip install django-websocket-gateway
```

### 2. Configure

```python
# settings.py
INSTALLED_APPS = [
    # ...
    "websocket_gateway",
]

WEBSOCKET_GATEWAY = {
    "INTERNAL_SECRET": env("WS_INTERNAL_SECRET"),
    "REDIS_URL": env("REDIS_URL", default="redis://localhost:6379/0"),
    "AUTHORIZATION_CALLBACK": "myapp.permissions.channels_for_user",
    "ALLOWED_ORIGINS": ["https://app.example.com"],
}
```

```python
# urls.py
urlpatterns = [
    # ...
    path("", include("websocket_gateway.urls")),
]
```

```python
# myapp/permissions.py
def channels_for_user(user):
    channels = [f"user-{user.id}"]
    channels += [f"org-{o.id}" for o in user.organizations.all()]
    return channels
```

### 3. Generate the secret

```bash
python -c "import secrets; print(secrets.token_urlsafe(48))"
# Set as WS_INTERNAL_SECRET in your environment.
```

### 4. Run

```
# Procfile
web:     gunicorn myapp.wsgi
worker:  celery -A myapp worker
gateway: python manage.py runwsgateway
```

The first invocation of `runwsgateway` downloads the Go binary from GitHub Releases (~15 MB).

### 5. Publish and subscribe

```python
# From anywhere in Django: views, signals, Celery tasks
from websocket_gateway import publish
publish("user-42", {"type": "notification", "text": "Your order shipped"})
```

```javascript
import { WSClient } from "/static/websocket_gateway/client.js";
const ws = new WSClient(`wss://${location.host}/ws/`);
ws.on("user-42", (payload) => console.log(payload));
ws.subscribe("user-42");
```

That's the integration. See the [full documentation](https://raghulj.github.io/django-websocket-gateway/) for deployment, custom channels, background jobs, logout handling, security model, and configuration reference.

## Security: the shared secret

The package uses a **dedicated secret** for Django↔gateway communication, separate from Django's `SECRET_KEY`. This is enforced at startup:

- Must be at least 32 characters.
- Must not equal `SECRET_KEY` (the package checks this with a timing-safe comparison).
- Never appears in logs, error messages, or HTTP responses.

The rationale, threat model, and rotation guidance are in [the security docs](https://raghulj.github.io/django-websocket-gateway/authentication/).

## Limitations (v1)

- No message persistence — disconnected clients miss messages published in their absence.
- No presence channels.
- No message replay on reconnect.
- Channel allowlist is fixed at connection time (reconnect picks up new permissions).

## License

MIT.
