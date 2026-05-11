# Authentication

The package uses a **dedicated shared secret** for the Django↔gateway
internal API, separate from Django's `SECRET_KEY`. This page explains the
validation rules, how to generate the secret, and what the threat model
covers.

## Generate a secret

```bash
python -c "import secrets; print(secrets.token_urlsafe(48))"
```

Set the result as `WEBSOCKET_GATEWAY["INTERNAL_SECRET"]` in Django and as
`INTERNAL_AUTH_SECRET` in the gateway environment. `runwsgateway`
translates the Django setting into the gateway env automatically.

## Validation rules (enforced at Django startup)

The settings validator refuses to start the app if any rule is violated.
Errors describe the problem without echoing any secret value:

- `INTERNAL_SECRET` must be present and a string.
- `INTERNAL_SECRET` must be **at least 32 characters**.
- `INTERNAL_SECRET` must **not equal** `settings.SECRET_KEY` (compared
  with [`hmac.compare_digest`][hmac]).
- `AUTHORIZATION_CALLBACK` must resolve to a callable.
- `REDIS_URL`, `AUTHORIZATION_CALLBACK`, `ALLOWED_ORIGINS` must be set.

[hmac]: https://docs.python.org/3/library/hmac.html#hmac.compare_digest

The gateway enforces the length rule too: `INTERNAL_AUTH_SECRET < 32`
chars produces a startup error whose message is exactly
`"INTERNAL_AUTH_SECRET must be at least 32 characters"` — no value
echoed.

## What the secret protects

The shared secret authenticates one HTTP path: Django's
`/internal/ws-auth/` endpoint, which the gateway hits during every
WebSocket handshake. The flow is:

1. Browser opens a WebSocket; Caddy forwards to the gateway.
2. Gateway POSTs to `/internal/ws-auth/` with:
    - `X-Internal-Auth: <secret>`
    - `X-Forwarded-Session: <user's sessionid>`
3. Django decodes the session, looks up the user, returns
   `{authenticated, user_id, username, allowed_channels}`.

Without the right secret, step 3 returns HTTP 403 and the handshake is
refused. Two layers of defence — the network policy on `/internal/*`
(see [Deployment](deployment.md)) and the secret check — keep the
endpoint safe even if one fails.

## Comparison is timing-safe

Both `_config.py` and the `require_internal_auth` decorator compare the
header value with [`hmac.compare_digest`][hmac]. The Go side uses
`crypto/subtle.ConstantTimeCompare`. `==` is never used.

## Rotation

v1 does **not** support overlapping secrets (`INTERNAL_SECRET_PREVIOUS`
is not a feature). To rotate:

1. Generate a new value.
2. Update both `WEBSOCKET_GATEWAY["INTERNAL_SECRET"]` in Django and
   `INTERNAL_AUTH_SECRET` in the gateway environment.
3. Restart both processes in any order. Connections in flight during the
   restart receive a clean close; clients reconnect with the new
   credentials.

This brief window is acceptable for most deployments. If you need
zero-downtime rotation, that's on the roadmap for a future release.

## What appears in logs

Every failure path logs a short, structured warning naming the failure
mode (`missing_header`, `bad_header`, `5xx`, `transport`, `json`) but
never the provided or expected secret. Session IDs are logged only as
the first 8 hex characters of their SHA-256 — enough to correlate across
log lines, not enough to replay.

## Implementation reference

::: websocket_gateway._config
    options:
      members: [get_config, MIN_SECRET_LENGTH]
