# Configuration reference

Two configuration surfaces: the `WEBSOCKET_GATEWAY` dict in Django
settings, and the environment variables read by the Go gateway.
`runwsgateway` translates the Django settings into the gateway env
automatically, so most deployments only configure Django.

## Django settings

```python
WEBSOCKET_GATEWAY = {
    "INTERNAL_SECRET": env("WS_INTERNAL_SECRET"),
    "REDIS_URL": env("REDIS_URL"),
    "AUTHORIZATION_CALLBACK": "myapp.permissions.channels_for_user",
    "ALLOWED_ORIGINS": ["https://app.example.com"],
}
```

### Required keys

| Key | Type | Description |
|---|---|---|
| `INTERNAL_SECRET` | `str` | ≥ 32 chars. Distinct from `SECRET_KEY`. |
| `REDIS_URL` | `str` | `redis://[:password@]host:port[/db]` |
| `AUTHORIZATION_CALLBACK` | `str` | Dotted path to a `callable(user) -> list[str]` |
| `ALLOWED_ORIGINS` | `list[str]` | Allow-list of WebSocket Origin values |

### Optional keys (passed through to the gateway env)

These are not required by Django itself; if present they are forwarded
to the gateway binary by `runwsgateway`.

| Key | Type | Default | Effect |
|---|---|---|---|
| `DJANGO_AUTH_URL` | `str` | `http://django:8000/internal/ws-auth/` | URL the gateway calls during the handshake |
| `GATEWAY_BIND` | `str` | `:8080` | TCP address the gateway binds to |
| `LOG_LEVEL` | `str` | `info` | `debug` / `info` / `warn` / `error` |
| `MAX_CONNECTIONS_PER_USER` | `int` | `10` | Per-user cap; excess connections close with 4429 |
| `MAX_CONNECTIONS_TOTAL` | `int` | `50000` | Process-wide cap; excess close with 4429 |
| `MAX_MESSAGE_SIZE` | `int` | `8192` | Max inbound frame bytes |
| `PING_INTERVAL` | `str` | `30s` | WebSocket ping cadence |
| `PONG_TIMEOUT` | `str` | `60s` | Read deadline; missing pong → close |
| `CONNECTION_MAX_LIFETIME` | `str` | `12h` | Hard cap on a single connection's life |
| `AUTH_TIMEOUT` | `str` | `5s` | Per-call timeout for the Django auth call |

Durations use Go's `time.ParseDuration` format: `30s`, `5m`, `12h`, etc.

## Gateway environment variables

Used by the Go binary directly. These mirror the optional keys above,
plus a few that have no Django-side configuration:

| Var | Default | Description |
|---|---|---|
| `INTERNAL_AUTH_SECRET` | (required) | The shared secret. |
| `REDIS_URL` | (required) | Pub/sub URL. |
| `DJANGO_AUTH_URL` | (required) | `/internal/ws-auth/` URL. |
| `ALLOWED_ORIGINS` | (required) | Comma-separated. |
| `LISTEN_ADDR` | `:8080` | Bind address. |
| `LOG_LEVEL` | `info` | Log level. |
| `MAX_CONNECTIONS_PER_USER` | `10` | See above. |
| `MAX_CONNECTIONS_TOTAL` | `50000` | See above. |
| `MAX_MESSAGE_SIZE` | `8192` | See above. |
| `PING_INTERVAL` | `30s` | See above. |
| `PONG_TIMEOUT` | `60s` | See above. |
| `CONNECTION_MAX_LIFETIME` | `12h` | See above. |
| `AUTH_TIMEOUT` | `5s` | See above. |

## Downloader environment variables

Only used by `runwsgateway`:

| Var | Effect |
|---|---|
| `WS_GATEWAY_BINARY_PATH` | Use this binary verbatim; skip the download. |
| `WS_GATEWAY_SKIP_DOWNLOAD` | Refuse to download even when nothing is cached. |
| `WS_GATEWAY_DOWNLOAD_URL` | Override the default GitHub Releases prefix. Used by the test suite. |

## Implementation reference

::: websocket_gateway._config
