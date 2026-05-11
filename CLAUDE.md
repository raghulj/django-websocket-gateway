# CLAUDE.md — Build Spec for Claude Code

Precise build instructions for `django-websocket-gateway`. Read `README.md` first for user-facing behavior; the `docs/` folder has the full design rationale. This file tells you exactly what to implement.

## Scope (v1)

**Build:**
- Python package `django-websocket-gateway` published to PyPI.
- Go binary `gateway` published as platform assets on GitHub Releases and as a Docker image on GHCR.
- Downloader that fetches the binary from GitHub on first use, with SHA-256 verification.
- `runwsgateway` Django management command (launches the binary via `os.execvpe`).
- Django app: auth view, internal-auth decorator, publish helper, logout signal handler, JS client.
- MkDocs documentation site (`docs/`) deployed to GitHub Pages.
- GitHub Actions release workflow.
- Reference `docker-compose.yml` and `Caddyfile`.

**Do NOT build (deliberately out of scope):**
- Prometheus metrics or `/metrics` endpoint. Logs are the only observability layer.
- `/readyz` endpoint. Just `/healthz`.
- `check_gateway` diagnostic command. Startup validation does the work.
- Secret rotation (`INTERNAL_SECRET_PREVIOUS`).
- cosign signing in CI. SHA-256 checksums only.
- HMAC-signed requests. Shared secret in header.
- Message persistence, replay, or presence.
- A separate JS SDK package — ship one static file.
- Django session decoding in Go.

## Hard rules

1. The shared secret is **dedicated** — separate from Django's `SECRET_KEY`. Enforced at startup.
2. Secret length: **≥32 characters**. Reject shorter at startup.
3. Secret **never** appears in logs, exception messages, or HTTP responses.
4. Secret comparison: `hmac.compare_digest` (Python) / `crypto/subtle.ConstantTimeCompare` (Go). **Never `==`**.
5. Gateway never decides what channels a user can subscribe to. Django returns `allowed_channels`; gateway enforces.
6. Gateway never reads Django's database directly.
7. Goroutines exit cleanly on context cancellation. No leaks.
8. Hub's maps are mutated only from the hub goroutine. Cross-goroutine communication via channels.
9. Downloaded binaries verified against SHA-256 from `SHA256SUMS`. Mismatch is a hard error.
10. Client-initiated subscriptions to channels starting with `_` are **rejected** by the gateway. The `_` prefix is reserved for internal control channels.

## Tech stack

**Python:** Python 3.10+, Django 4.2+/5.x, `redis-py`. Stdlib `urllib.request`, `hmac`, `logging`.
**Go:** Go 1.22+, `github.com/coder/websocket`, `github.com/redis/go-redis/v9`. Stdlib `log/slog`, `crypto/subtle`.
**Infra:** Caddy 2.x, Redis 7.x, GitHub Actions, MkDocs + Material theme.

## Repository layout

```
.
├── README.md
├── CLAUDE.md
├── LICENSE
├── pyproject.toml
├── docker-compose.example.yml
├── Caddyfile.example
├── .env.example
├── .github/workflows/
│   ├── release.yml
│   ├── test.yml
│   └── docs.yml
├── docs/                          # MkDocs site (see Step 24)
│   ├── mkdocs.yml
│   ├── requirements.txt
│   └── docs/
│       ├── index.md
│       ├── quickstart.md
│       ├── architecture.md
│       ├── authentication.md
│       ├── channels.md
│       ├── publishing.md
│       ├── background-jobs.md
│       ├── logout.md
│       ├── javascript-client.md
│       ├── deployment.md
│       ├── configuration.md
│       └── threat-model.md
├── gateway/                       # Go service
│   ├── main.go
│   ├── config.go
│   ├── hub.go
│   ├── client.go
│   ├── auth.go
│   ├── redis.go
│   ├── health.go
│   ├── go.mod
│   ├── go.sum
│   └── Dockerfile
└── websocket_gateway/             # Python package
    ├── __init__.py                # exposes publish, force_logout_user
    ├── apps.py
    ├── urls.py
    ├── views.py
    ├── auth_decorator.py
    ├── publish.py
    ├── revocation.py              # signal handler + force_logout_user
    ├── _downloader.py
    ├── _config.py
    ├── _version.py
    ├── bin/.gitkeep
    ├── static/websocket_gateway/client.js
    ├── management/commands/
    │   └── runwsgateway.py
    └── tests/
        ├── test_config.py
        ├── test_auth_decorator.py
        ├── test_views.py
        ├── test_publish.py
        ├── test_revocation.py
        └── test_downloader.py
```

---

## Build order

### Step 1: `gateway/config.go`

```go
type Config struct {
    ListenAddr            string
    RedisURL              string
    DjangoAuthURL         string
    InternalAuthSecret    string
    AllowedOrigins        []string
    MaxConnectionsPerUser int
    MaxConnectionsTotal   int
    MaxMessageSize        int64
    ConnectionMaxLifetime time.Duration
    PingInterval          time.Duration
    PongTimeout           time.Duration
    LogLevel              slog.Level
}

func Load() (*Config, error) { ... }
```

Validation:
- Required: `RedisURL`, `DjangoAuthURL`, `InternalAuthSecret`, `AllowedOrigins`.
- Secret must be ≥32 chars. Error: `"INTERNAL_AUTH_SECRET must be at least 32 characters"` — do not include the value or its length-as-clue.
- Parse durations with `time.ParseDuration`.
- Defaults: `MaxConnectionsPerUser=10`, `MaxConnectionsTotal=50000`, `MaxMessageSize=8192`, `ConnectionMaxLifetime=12h`, `PingInterval=30s`, `PongTimeout=60s`, `LogLevel=info`.

### Step 2: `gateway/auth.go`

```go
type AuthResult struct {
    UserID          int64    `json:"user_id"`
    Username        string   `json:"username"`
    AllowedChannels []string `json:"allowed_channels"`
    SessionKey      string   // captured from input, not returned by Django
}

type Authenticator struct { httpClient *http.Client; url, secret string }

func NewAuthenticator(cfg *Config) *Authenticator { ... }
func (a *Authenticator) Validate(ctx context.Context, sessionID string) (*AuthResult, error)
```

- POST with `X-Internal-Auth` and `X-Forwarded-Session`. 5s timeout.
- Return `ErrUnauthenticated` for HTTP 401 or `{"authenticated": false}`.
- Return `ErrAuthFailed` (wrap cause) for transport errors, 5xx, malformed JSON.
- Log failures with `auth_url`, `reason`, never the secret value, never the session value (log a short hash if needed: `fmt.Sprintf("%x", sha256.Sum256([]byte(sessionID)))[:8]`).
- Populate `AuthResult.SessionKey` from the input session ID after success — needed for control-channel auto-subscribe.

### Step 3: `gateway/hub.go`

The central broker. Single goroutine owns all shared state.

```go
type Hub struct {
    register    chan *Client
    unregister  chan *Client
    subscribe   chan subscription
    unsubscribe chan subscription
    incoming    chan incomingMessage

    // owned by run() goroutine — no external access:
    clients     map[*Client]bool
    channels    map[string]map[*Client]bool
    userClients map[int64]map[*Client]bool
    redis       *RedisSubscriber  // set after construction; channel refcounted via Subscribe/Unsubscribe
    cfg         *Config
    log         *slog.Logger
}
```

`Run(ctx)` loop:

- **register:** enforce `MaxConnectionsTotal` and `MaxConnectionsPerUser`. Excess → close with code 4429, do not add. On success: auto-subscribe the client to its **control channels** `_session:{sessionKey}` and `_user:{userID}:revoke` (see Step 5).
- **unregister:** remove from all maps; close `c.send`; if any channel becomes empty, unsubscribe from Redis.
- **subscribe (client-initiated):** validate channel name regex `^[a-zA-Z0-9_:-]{1,128}$` AND reject if it starts with `_`. Validate against `client.allowedChannels`. If not allowed, send `{"type":"error","channel":...,"reason":"forbidden"}` on the client's send channel. Otherwise add and refcount-subscribe via Redis.
- **subscribe (internal, for control channels):** bypass the `_` and allowedChannels checks. Internal use only.
- **incoming:** for each subscriber, non-blocking send. Buffer full → disconnect that client. Special handling: if channel starts with `_`, parse payload; if `type=="revoke"`, find the relevant client(s) and close their WebSocket with code 4401 reason `"session_revoked"`.

Non-blocking send pattern:
```go
select {
case c.send <- msg:
default:
    // slow client; disconnect
    h.unregister <- c
}
```

### Step 4: `gateway/client.go`

```go
type Client struct {
    conn            *websocket.Conn
    hub             *Hub
    send            chan []byte
    userID          int64
    sessionKey      string
    connectionID    string  // UUID, used in all logs
    allowedChannels map[string]bool
    subscribed      map[string]bool
    connectedAt     time.Time
    cfg             *Config
}

func (c *Client) ReadPump(ctx context.Context)
func (c *Client) WritePump(ctx context.Context)
```

`ReadPump`:
- `conn.SetReadLimit(cfg.MaxMessageSize)`.
- Read deadline = `now + PongTimeout`; reset on every read.
- Parse `{action, channel}`. Validate regex. Reject leading `_`. Send subscribe/unsubscribe to hub.
- Exit on context cancellation or read error.

`WritePump`:
- Ticker at `cfg.PingInterval`.
- `select` over `c.send`, ticker, ctx, and a lifetime timer (`time.After(cfg.ConnectionMaxLifetime - time.Since(c.connectedAt))`).
- On send: write deadline + text frame.
- On ping tick: write ping frame.
- On lifetime expiry: close cleanly with 1000.
- On any error: exit (unregister via deferred cleanup).

All log lines from this file include `connection_id`. Lines involving the user include `user_id`.

### Step 5: `gateway/redis.go`

```go
type RedisSubscriber struct {
    client      *redis.Client
    hub         *Hub
    pubsub      *redis.PubSub
    activeChans map[string]int
    mu          sync.Mutex
    log         *slog.Logger
}

func NewRedisSubscriber(cfg *Config, hub *Hub) (*RedisSubscriber, error)
func (r *RedisSubscriber) Run(ctx context.Context) error
func (r *RedisSubscriber) Subscribe(channel string) error
func (r *RedisSubscriber) Unsubscribe(channel string) error
func (r *RedisSubscriber) Ping(ctx context.Context) error
```

- One `PubSub` connection, `Subscribe`/`Unsubscribe` calls dynamically.
- Refcount in `activeChans`. Subscribe to Redis only on 0→1; unsubscribe on 1→0.
- Goroutine reads from `pubsub.Channel()`, forwards `{channel, payload}` JSON to `hub.incoming`.
- On Redis disconnect: reconnect loop with backoff 1s, 2s, 4s, capped at 30s. Resubscribe all `activeChans`. Log each reconnect attempt.
- `Ping` exists for `/healthz` use.

### Step 6: `gateway/health.go`

```go
var shuttingDown atomic.Bool

func HealthzHandler(redis *RedisSubscriber) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if shuttingDown.Load() {
            http.Error(w, "shutting down", http.StatusServiceUnavailable)
            return
        }
        ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
        defer cancel()
        if err := redis.Ping(ctx); err != nil {
            http.Error(w, "redis unreachable", http.StatusServiceUnavailable)
            return
        }
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("ok"))
    }
}
```

Single endpoint — used both for liveness and readiness. Returns 503 on shutdown or Redis failure, 200 otherwise.

### Step 7: `gateway/main.go`

```go
func main() {
    cfg, err := Load()
    if err != nil { log.Fatal(err) }
    setupLogger(cfg.LogLevel)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    hub := NewHub(cfg)
    redisSub, err := NewRedisSubscriber(cfg, hub)
    if err != nil { log.Fatal(err) }
    hub.redis = redisSub
    auth := NewAuthenticator(cfg)

    go hub.Run(ctx)
    go redisSub.Run(ctx)

    mux := http.NewServeMux()
    mux.HandleFunc("/healthz", HealthzHandler(redisSub))
    mux.HandleFunc("/ws/", wsHandler(cfg, hub, auth))

    server := &http.Server{
        Addr:              cfg.ListenAddr,
        Handler:           mux,
        ReadHeaderTimeout: 10 * time.Second,
    }

    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

    go func() {
        if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            slog.Error("server error", "err", err)
        }
    }()

    <-sigCh
    slog.Info("shutdown initiated")
    shuttingDown.Store(true)
    hub.BroadcastClose()

    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer shutdownCancel()
    server.Shutdown(shutdownCtx)
    cancel()
}
```

`wsHandler` accepts the upgrade, extracts the `sessionid` cookie, calls `auth.Validate`, builds the `Client` (with `sessionKey` and `allowedChannels`), registers with the hub, starts `ReadPump` and `WritePump`. Check origin against `cfg.AllowedOrigins`.

### Step 8: `gateway/Dockerfile`

```dockerfile
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /gateway .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /gateway /gateway
EXPOSE 8080
ENTRYPOINT ["/gateway"]
```

---

### Step 9: `pyproject.toml`

```toml
[build-system]
requires = ["setuptools>=68", "wheel"]
build-backend = "setuptools.build_meta"

[project]
name = "django-websocket-gateway"
version = "0.1.0"
description = "Drop-in real-time WebSocket gateway for Django"
readme = "README.md"
requires-python = ">=3.10"
license = {text = "MIT"}
dependencies = ["Django>=4.2", "redis>=4.5"]
classifiers = [
    "Framework :: Django",
    "License :: OSI Approved :: MIT License",
    "Programming Language :: Python :: 3.10",
    "Programming Language :: Python :: 3.11",
    "Programming Language :: Python :: 3.12",
]

[tool.setuptools.packages.find]
include = ["websocket_gateway*"]

[tool.setuptools.package-data]
websocket_gateway = ["static/websocket_gateway/*.js", "bin/.gitkeep"]
```

### Step 10: `websocket_gateway/_version.py`

```python
__version__ = "0.1.0"
```

Synced from git tag by the release workflow.

### Step 11: `websocket_gateway/_config.py`

```python
import hmac
from django.conf import settings
from django.core.exceptions import ImproperlyConfigured


MIN_SECRET_LENGTH = 32


def get_config() -> dict:
    cfg = getattr(settings, "WEBSOCKET_GATEWAY", None)
    if cfg is None:
        raise ImproperlyConfigured(
            "WEBSOCKET_GATEWAY settings dict is required when "
            "'websocket_gateway' is in INSTALLED_APPS."
        )
    _validate_secret(cfg)
    _validate_required(cfg)
    _validate_callback(cfg)
    return cfg


def _validate_secret(cfg: dict) -> None:
    secret = cfg.get("INTERNAL_SECRET")
    if not secret:
        raise ImproperlyConfigured(
            "WEBSOCKET_GATEWAY['INTERNAL_SECRET'] is required. "
            'Generate one with: python -c "import secrets; '
            'print(secrets.token_urlsafe(48))"'
        )
    if not isinstance(secret, str):
        raise ImproperlyConfigured(
            "WEBSOCKET_GATEWAY['INTERNAL_SECRET'] must be a string."
        )
    if len(secret) < MIN_SECRET_LENGTH:
        raise ImproperlyConfigured(
            f"WEBSOCKET_GATEWAY['INTERNAL_SECRET'] must be at least "
            f"{MIN_SECRET_LENGTH} characters long."
        )
    django_secret = settings.SECRET_KEY or ""
    if isinstance(django_secret, str) and django_secret:
        if hmac.compare_digest(secret, django_secret):
            raise ImproperlyConfigured(
                "WEBSOCKET_GATEWAY['INTERNAL_SECRET'] must NOT equal "
                "settings.SECRET_KEY. Use a distinct, dedicated secret."
            )


def _validate_required(cfg: dict) -> None:
    required = ["REDIS_URL", "AUTHORIZATION_CALLBACK", "ALLOWED_ORIGINS"]
    missing = [k for k in required if not cfg.get(k)]
    if missing:
        raise ImproperlyConfigured(
            f"WEBSOCKET_GATEWAY missing required keys: {', '.join(missing)}"
        )


def _validate_callback(cfg: dict) -> None:
    from django.utils.module_loading import import_string
    path = cfg["AUTHORIZATION_CALLBACK"]
    try:
        callback = import_string(path)
    except ImportError as e:
        raise ImproperlyConfigured(
            f"WEBSOCKET_GATEWAY['AUTHORIZATION_CALLBACK']='{path}' "
            f"could not be imported: {e}"
        )
    if not callable(callback):
        raise ImproperlyConfigured(
            f"WEBSOCKET_GATEWAY['AUTHORIZATION_CALLBACK']='{path}' is not callable."
        )
```

**Critical:** every error message describes the problem without ever mentioning the secret's value.

### Step 12: `websocket_gateway/apps.py`

```python
from django.apps import AppConfig


class WebsocketGatewayConfig(AppConfig):
    name = "websocket_gateway"
    verbose_name = "WebSocket Gateway"

    def ready(self) -> None:
        from . import _config, revocation  # noqa: F401
        _config.get_config()  # validate at startup
        revocation.connect_signals()
```

### Step 13: `websocket_gateway/auth_decorator.py`

```python
import functools
import hmac
import logging
from django.http import HttpResponseForbidden
from ._config import get_config

logger = logging.getLogger(__name__)


def require_internal_auth(view_func):
    @functools.wraps(view_func)
    def wrapper(request, *args, **kwargs):
        cfg = get_config()
        provided = request.headers.get("X-Internal-Auth", "")
        if not provided:
            logger.warning(
                "ws-auth rejected: missing X-Internal-Auth header",
                extra={"remote": request.META.get("REMOTE_ADDR")},
            )
            return HttpResponseForbidden()
        if not hmac.compare_digest(provided, cfg["INTERNAL_SECRET"]):
            logger.warning(
                "ws-auth rejected: invalid X-Internal-Auth header",
                extra={"remote": request.META.get("REMOTE_ADDR")},
            )
            return HttpResponseForbidden()
        return view_func(request, *args, **kwargs)
    return wrapper
```

Log messages name the failure mode but never the provided value.

### Step 14: `websocket_gateway/views.py`

```python
from django.contrib.auth import get_user_model
from django.contrib.sessions.backends.db import SessionStore
from django.http import JsonResponse
from django.utils.module_loading import import_string
from django.views.decorators.csrf import csrf_exempt
from django.views.decorators.http import require_POST
from ._config import get_config
from .auth_decorator import require_internal_auth


@csrf_exempt
@require_POST
@require_internal_auth
def ws_auth(request):
    session_key = request.headers.get("X-Forwarded-Session", "")
    if not session_key:
        return JsonResponse({"authenticated": False}, status=401)

    session = SessionStore(session_key=session_key)
    user_id = session.get("_auth_user_id")
    if not user_id:
        return JsonResponse({"authenticated": False}, status=401)

    User = get_user_model()
    try:
        user = User.objects.get(pk=user_id, is_active=True)
    except User.DoesNotExist:
        return JsonResponse({"authenticated": False}, status=401)

    cfg = get_config()
    callback = import_string(cfg["AUTHORIZATION_CALLBACK"])
    allowed_channels = callback(user)

    if not isinstance(allowed_channels, list) or not all(isinstance(c, str) for c in allowed_channels):
        raise TypeError(
            f"AUTHORIZATION_CALLBACK must return list[str], got "
            f"{type(allowed_channels).__name__}"
        )

    return JsonResponse({
        "authenticated": True,
        "user_id": user.id,
        "username": user.get_username(),
        "allowed_channels": allowed_channels,
    })
```

### Step 15: `websocket_gateway/urls.py`

```python
from django.urls import path
from .views import ws_auth

app_name = "websocket_gateway"
urlpatterns = [path("internal/ws-auth/", ws_auth, name="ws-auth")]
```

### Step 16: `websocket_gateway/publish.py`

```python
import json
import threading
from typing import Any
import redis
from ._config import get_config


_lock = threading.Lock()
_client: redis.Redis | None = None


def _get_client() -> redis.Redis:
    global _client
    with _lock:
        if _client is None:
            _client = redis.from_url(get_config()["REDIS_URL"])
        return _client


def publish(channel: str, payload: dict[str, Any]) -> int:
    """Publish to a WebSocket channel. Returns number of Redis subscribers."""
    message = json.dumps({"channel": channel, "payload": payload})
    return _get_client().publish(channel, message)
```

### Step 17: `websocket_gateway/revocation.py`

```python
from django.contrib.auth.signals import user_logged_out
from .publish import publish


def connect_signals() -> None:
    user_logged_out.connect(
        _revoke_on_logout,
        dispatch_uid="websocket_gateway.revoke_on_logout",
    )


def _revoke_on_logout(sender, request, user, **kwargs):
    if request is None or not getattr(request, "session", None):
        return
    session_key = request.session.session_key
    if session_key:
        publish(f"_session:{session_key}", {"type": "revoke", "reason": "logout"})


def force_logout_user(user) -> None:
    """Force-close all WebSocket connections for a user.
    
    Use after revoking access (banning, security event). This does NOT
    delete the user's session rows — call Session.objects.filter(...).delete()
    yourself if needed. This only kicks live WebSocket connections.
    """
    publish(f"_user:{user.pk}:revoke", {"type": "revoke", "reason": "force_logout"})
```

### Step 18: `websocket_gateway/__init__.py`

```python
from ._version import __version__
from .publish import publish
from .revocation import force_logout_user

default_app_config = "websocket_gateway.apps.WebsocketGatewayConfig"

__all__ = ["publish", "force_logout_user", "__version__"]
```

### Step 19: `websocket_gateway/_downloader.py`

```python
import hashlib
import os
import platform
import shutil
import stat
import sys
import tempfile
import urllib.error
import urllib.request
from pathlib import Path
from ._version import __version__


GITHUB_REPO = "raghulj/django-websocket-gateway"
BASE_URL_TEMPLATE = "https://github.com/{repo}/releases/download/v{version}"


class DownloadError(RuntimeError):
    pass


def ensure_binary() -> Path:
    if override := os.environ.get("WS_GATEWAY_BINARY_PATH"):
        p = Path(override)
        if not p.exists():
            raise DownloadError(f"WS_GATEWAY_BINARY_PATH={override} does not exist.")
        return p

    bin_dir = Path(__file__).resolve().parent / "bin"
    bin_dir.mkdir(parents=True, exist_ok=True)
    binary_name = _platform_binary_name()
    binary_path = bin_dir / binary_name

    if binary_path.exists() and os.access(binary_path, os.X_OK):
        return binary_path

    if os.environ.get("WS_GATEWAY_SKIP_DOWNLOAD"):
        raise DownloadError(
            f"Binary not found at {binary_path} and WS_GATEWAY_SKIP_DOWNLOAD is set. "
            f"Set WS_GATEWAY_BINARY_PATH, unset the skip flag, or use the Docker image "
            f"ghcr.io/{GITHUB_REPO}:{__version__}."
        )

    _download_and_verify(binary_name, binary_path)
    mode = binary_path.stat().st_mode
    binary_path.chmod(mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    return binary_path


def _platform_binary_name() -> str:
    system_map = {"linux": "linux", "darwin": "darwin"}
    machine_map = {"x86_64": "amd64", "amd64": "amd64",
                   "aarch64": "arm64", "arm64": "arm64"}
    sys_key = system_map.get(sys.platform)
    arch_key = machine_map.get(platform.machine().lower())
    if not sys_key or not arch_key:
        raise DownloadError(
            f"Unsupported platform: {sys.platform}/{platform.machine()}. "
            f"Use the Docker image ghcr.io/{GITHUB_REPO}:{__version__}."
        )
    return f"gateway-{sys_key}-{arch_key}"


def _base_url() -> str:
    if override := os.environ.get("WS_GATEWAY_DOWNLOAD_URL"):
        return override.rstrip("/")
    return BASE_URL_TEMPLATE.format(repo=GITHUB_REPO, version=__version__)


def _download_and_verify(binary_name: str, dest: Path) -> None:
    base = _base_url()
    expected = _fetch_checksum(f"{base}/SHA256SUMS", binary_name)
    with tempfile.NamedTemporaryFile(delete=False, dir=dest.parent, prefix=".dl-") as tmp:
        tmp_path = Path(tmp.name)
    try:
        _stream_download(f"{base}/{binary_name}", tmp_path)
        actual = _sha256(tmp_path)
        if actual != expected:
            raise DownloadError(
                f"Checksum mismatch for {binary_name}: expected {expected}, got {actual}."
            )
        shutil.move(str(tmp_path), str(dest))
    finally:
        if tmp_path.exists():
            tmp_path.unlink(missing_ok=True)


def _stream_download(url: str, dest: Path, timeout: int = 120) -> None:
    try:
        with urllib.request.urlopen(url, timeout=timeout) as resp, dest.open("wb") as out:
            shutil.copyfileobj(resp, out, length=65536)
    except urllib.error.URLError as e:
        raise DownloadError(f"Download failed: {url}: {e}") from e


def _fetch_checksum(url: str, binary_name: str) -> str:
    try:
        with urllib.request.urlopen(url, timeout=30) as resp:
            content = resp.read().decode()
    except urllib.error.URLError as e:
        raise DownloadError(f"Checksum fetch failed: {url}: {e}") from e
    for line in content.splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split()
        if len(parts) >= 2 and parts[1].lstrip("*") == binary_name:
            return parts[0]
    raise DownloadError(f"Checksum for {binary_name} not in SHA256SUMS at {url}")


def _sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()
```

### Step 20: `websocket_gateway/management/commands/runwsgateway.py`

```python
import os
from django.core.management.base import BaseCommand
from websocket_gateway._config import get_config
from websocket_gateway._downloader import ensure_binary
from websocket_gateway import __version__


class Command(BaseCommand):
    help = "Run the WebSocket gateway (Go binary, separate process)."

    def handle(self, *args, **options):
        cfg = get_config()
        binary = ensure_binary()
        env = os.environ.copy()
        env.update(_translate(cfg))
        self.stdout.write(self.style.SUCCESS(
            f"Launching gateway {__version__}: {binary}"
        ))
        os.execvpe(str(binary), [str(binary)], env)


def _translate(cfg: dict) -> dict:
    env = {
        "INTERNAL_AUTH_SECRET": cfg["INTERNAL_SECRET"],
        "REDIS_URL": cfg["REDIS_URL"],
        "DJANGO_AUTH_URL": cfg.get("DJANGO_AUTH_URL", "http://django:8000/internal/ws-auth/"),
        "ALLOWED_ORIGINS": ",".join(cfg["ALLOWED_ORIGINS"]),
        "LISTEN_ADDR": cfg.get("GATEWAY_BIND", ":8080"),
        "LOG_LEVEL": cfg.get("LOG_LEVEL", "info"),
    }
    for key in ["MAX_CONNECTIONS_PER_USER", "MAX_CONNECTIONS_TOTAL", "MAX_MESSAGE_SIZE",
                "PING_INTERVAL", "PONG_TIMEOUT", "CONNECTION_MAX_LIFETIME"]:
        if key in cfg:
            env[key] = str(cfg[key])
    return env
```

### Step 21: `websocket_gateway/static/websocket_gateway/client.js`

```javascript
export class WSClient {
  constructor(url, options = {}) {
    this.url = url;
    this.channels = new Set(options.channels || []);
    this.handlers = new Map();
    this.backoff = 1000;
    this.maxBackoff = 30000;
    this.shouldReconnect = true;
    this._connect();
  }

  on(channel, handler) { this.handlers.set(channel, handler); }

  subscribe(channel) {
    this.channels.add(channel);
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({action: "subscribe", channel}));
    }
  }

  unsubscribe(channel) {
    this.channels.delete(channel);
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({action: "unsubscribe", channel}));
    }
  }

  close() {
    this.shouldReconnect = false;
    this.ws?.close();
  }

  _connect() {
    this.ws = new WebSocket(this.url);

    this.ws.onopen = () => {
      this.backoff = 1000;
      for (const ch of this.channels) {
        this.ws.send(JSON.stringify({action: "subscribe", channel: ch}));
      }
    };

    this.ws.onmessage = (event) => {
      let msg;
      try { msg = JSON.parse(event.data); } catch { return; }
      if (msg.type === "error") { console.warn("WS error:", msg); return; }
      const handler = this.handlers.get(msg.channel);
      if (handler) handler(msg.payload);
    };

    this.ws.onclose = (event) => {
      if (!this.shouldReconnect) return;
      // 4401 = unauthorized (revoked or session invalid); stop trying.
      if (event.code === 4401) {
        console.warn("WS unauthorized; not reconnecting");
        return;
      }
      const delay = this.backoff + Math.random() * 1000;
      setTimeout(() => this._connect(), delay);
      this.backoff = Math.min(this.backoff * 2, this.maxBackoff);
    };

    this.ws.onerror = () => this.ws.close();
  }
}
```

### Step 22: Tests

`test_config.py`:
- Missing settings → `ImproperlyConfigured`.
- Secret < 32 chars → `ImproperlyConfigured`; assert value not in str(exc).
- Secret == SECRET_KEY → `ImproperlyConfigured`.
- Valid config → returns dict.

`test_auth_decorator.py`:
- No header → 403.
- Wrong secret → 403.
- Right secret → passes through.
- Log records do not contain secret value.

`test_views.py`:
- No session → 401. Invalid session → 401. Inactive user → 401.
- Valid session → 200 with `allowed_channels`.
- Callback returning non-list → 500.

`test_publish.py`:
- `publish()` calls `redis.publish` with correct JSON shape.

`test_revocation.py`:
- `user_logged_out` signal triggers a publish on `_session:{key}`.
- `force_logout_user(u)` publishes on `_user:{u.id}:revoke`.

`test_downloader.py`:
- Checksum mismatch → `DownloadError`; no file left at destination.
- `WS_GATEWAY_BINARY_PATH` override.
- `WS_GATEWAY_SKIP_DOWNLOAD` with no binary → `DownloadError`.

---

### Step 23: GitHub Actions

`.github/workflows/release.yml` — builds binaries for `linux-amd64/arm64`, `darwin-amd64/arm64`; generates `SHA256SUMS`; creates GitHub Release; publishes wheel to PyPI; pushes Docker image to GHCR. Version comes from the git tag.

`.github/workflows/test.yml` — runs Go tests and Python tests (matrix: Python 3.10/3.11/3.12 × Django 4.2/5.0) on every push and PR.

`.github/workflows/docs.yml` — builds MkDocs site on push to `main`, deploys to GitHub Pages.

Sketches:

```yaml
# docs.yml
name: Docs
on:
  push:
    branches: [main]
permissions:
  contents: read
  pages: write
  id-token: write
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with: { python-version: '3.12' }
      - run: pip install -r docs/requirements.txt
      - run: mkdocs build --strict --config-file docs/mkdocs.yml --site-dir _site
      - uses: actions/upload-pages-artifact@v3
        with: { path: _site }
  deploy:
    needs: build
    runs-on: ubuntu-latest
    environment:
      name: github-pages
    steps:
      - uses: actions/deploy-pages@v4
```

### Step 24: MkDocs docs site

The full content for each page is provided in the `docs/` folder of this repository. Build target structure described in the layout section. The pages cover:

- **index.md** — landing page, what it is, link to quickstart.
- **quickstart.md** — five-minute install-to-running guide.
- **architecture.md** — components, data flow, why this design.
- **authentication.md** — the dedicated secret, validation rules, threat coverage.
- **channels.md** — channel naming, the authorization callback, custom channel patterns.
- **publishing.md** — `publish()` from views, signals, anywhere.
- **background-jobs.md** — Celery and worker integration.
- **logout.md** — signal-driven revocation, `force_logout_user`, behavior matrix.
- **javascript-client.md** — `WSClient` API, reconnect behavior.
- **deployment.md** — Compose, Caddy, the Docker image option.
- **configuration.md** — full settings and env-var reference.
- **threat-model.md** — what's covered, what isn't, when to upgrade.

Refer to `docs/docs/*.md` for the actual content. The MkDocs `nav` in `mkdocs.yml` orders them logically.

---

## Testing checklist

**Secret validation:**
- [ ] `INTERNAL_SECRET` < 32 chars → Django refuses to start; error doesn't include the value.
- [ ] `INTERNAL_SECRET == SECRET_KEY` → Django refuses to start.
- [ ] Logs of failed auth attempts contain reason but not the provided/expected value.

**Auth & authz:**
- [ ] Anonymous WS request → close code 4401.
- [ ] Valid session → connected; allowed channels enforced.
- [ ] Subscribe to disallowed channel → error frame.
- [ ] Subscribe to channel starting with `_` → error frame (clients can't touch control channels).
- [ ] Public request to `/internal/ws-auth/` → 404 from Caddy.
- [ ] Direct request without `X-Internal-Auth` → 403.

**Logout & revocation:**
- [ ] `request.user.logout()` (or `LogoutView`) → existing WS closed within ~100ms with code 4401.
- [ ] `force_logout_user(u)` → all of u's WS closed.
- [ ] Page navigation after logout → WS closes naturally (browser).

**Publish/subscribe:**
- [ ] `publish("user-42", {...})` from view, signal, and Celery task all reach the client.
- [ ] Multiple clients on same channel all receive.
- [ ] No subscribers → no error.

**Resource limits:**
- [ ] Per-user cap enforced (excess → code 4429).
- [ ] Inbound message > `MAX_MESSAGE_SIZE` → close.
- [ ] `ulimit -n` in container is 65535.

**Reliability:**
- [ ] `docker kill redis && docker start redis` → reconnect; new messages flow.
- [ ] `SIGTERM` → graceful shutdown within 10s; clients receive close frames.
- [ ] Slow client doesn't stall others.

**Distribution:**
- [ ] First `runwsgateway` downloads binary, verifies checksum.
- [ ] Tampered `SHA256SUMS` → `DownloadError`; nothing installed.
- [ ] `WS_GATEWAY_BINARY_PATH` and `WS_GATEWAY_SKIP_DOWNLOAD` work.

## Common pitfalls

1. **Logging the secret.** The biggest hazard. Audit every log statement, exception, and response body. Length is OK; value is never OK.
2. **`==` for secret comparison.** Always `hmac.compare_digest` / `crypto/subtle.ConstantTimeCompare`.
3. **Not validating at startup.** `AppConfig.ready()` is what makes misconfiguration fail at deploy, not at first connection.
4. **Forgetting `csrf_exempt` on `ws_auth`.** It's a POST endpoint receiving server-to-server traffic.
5. **`subprocess.Popen` instead of `os.execvpe`.** Replace the process; don't supervise.
6. **Downloading without checksum verification.** Always verify; refuse on mismatch.
7. **Forgetting to reject `_` prefix on client subscribes.** Otherwise users can subscribe to other users' revocation channels.
8. **Not auto-subscribing to control channels at handshake.** Without it, logout revocation does nothing.
9. **Blocking the hub on a slow client.** Always non-blocking send; disconnect on full buffer.

## Definition of done

1. `pip install django-websocket-gateway` succeeds.
2. Five-line integration in a Django app (INSTALLED_APPS, urls, settings, callback, env var) yields working real-time.
3. `python manage.py runwsgateway` downloads binary on first run; subsequent runs use cache.
4. End-to-end demo: user logs in → browser opens WS → `publish()` from Django shell appears in browser within 50ms.
5. User logs out → WS closes within 100ms; client does not reconnect.
6. `docker compose stop redis && docker compose start redis` → messages still flow.
7. GitHub Actions: tag push triggers binary build, PyPI publish, Docker image push, docs deploy.
8. Tests in the checklist pass.
9. MkDocs site builds with `--strict`.
