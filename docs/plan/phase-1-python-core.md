# Phase 1 — Python package core

← [Plan index](README.md)

## Goal

Deliver the Django app's user-facing behaviour: configuration validation, the `/internal/ws-auth/` view, the `publish()` helper, and the logout signal — all built **test-first**. At the end of this phase, `pytest` is green and the package is usable in a Django project (the Go gateway can come later; Django alone is testable).

## Prerequisites

- Phase 0 complete.
- `fakeredis` available (dev extra) — we'll use it to avoid a real Redis in tests.

## TDD discipline

For every task: write the failing test in `websocket_gateway/tests/test_*.py` first, run `pytest -k <test>` and **see it fail with the expected assertion**, then write the minimum code in the production file to flip it green, then refactor. Commit at each green. Do not skip ahead.

## Tasks

### 1.8 Test settings + `conftest.py` (do this first — it unblocks everything)
- [x] `websocket_gateway/tests/settings.py`: minimum Django config — `SECRET_KEY="x"*50` (different from any `INTERNAL_SECRET` we'll use in tests), `INSTALLED_APPS=["django.contrib.auth","django.contrib.contenttypes","django.contrib.sessions","websocket_gateway"]`, `DATABASES={"default":{"ENGINE":"django.db.backends.sqlite3","NAME":":memory:"}}`, `USE_TZ=True`, `ROOT_URLCONF="websocket_gateway.tests.urls"`.
- [x] `websocket_gateway/tests/urls.py`: `urlpatterns = [path("", include("websocket_gateway.urls"))]`.
- [x] `websocket_gateway/tests/conftest.py`: `pytest` fixtures — a `valid_config` fixture returning a working `WEBSOCKET_GATEWAY` dict, a `fakeredis_client` fixture, a `monkeypatch_publish_redis` fixture that swaps the cached redis client with `fakeredis`.

Acceptance: `pytest` runs, collects zero tests, exits 5.

### 1.1 `_config.py` (Step 11) — `test_config.py` first
- [x] **Tests** (`test_config.py`):
  - `WEBSOCKET_GATEWAY` absent → `ImproperlyConfigured`.
  - `INTERNAL_SECRET` missing → `ImproperlyConfigured`.
  - `INTERNAL_SECRET` < 32 chars → `ImproperlyConfigured`; **assert `secret_value not in str(exc)`**.
  - `INTERNAL_SECRET == SECRET_KEY` → `ImproperlyConfigured`.
  - `AUTHORIZATION_CALLBACK` import path invalid → `ImproperlyConfigured` naming the path.
  - `AUTHORIZATION_CALLBACK` resolves to a non-callable → `ImproperlyConfigured`.
  - Required keys missing → error names which keys.
  - Valid config → returns the dict.
- [x] **Implementation**: exactly the code in Step 11 of `/CLAUDE.md`. Don't add extra validation that isn't tested.

### 1.2 `apps.py` (Step 12) — test startup validation
- [x] **Test**: invoke `WebsocketGatewayConfig.ready()` via Django's setup with an invalid config and assert it raises. Confirm `revocation.connect_signals` is called exactly once even across multiple `ready()` invocations (use `dispatch_uid` to dedupe).
- [x] **Implementation**: per Step 12.

### 1.3 `auth_decorator.py` (Step 13) — `test_auth_decorator.py`
- [x] **Tests**:
  - Request without `X-Internal-Auth` → 403.
  - Request with wrong secret → 403.
  - Request with right secret → wrapped view executes (response = 200).
  - **Assert secret value does not appear in any captured log record** (use `caplog`).
  - Assert `hmac.compare_digest` is the comparator (regression guard: substitute a string that is a prefix of the secret and confirm it still 403s, which `==` would also handle — better test: import the module source and `assert "hmac.compare_digest" in inspect.getsource(require_internal_auth)`).
- [x] **Implementation**: per Step 13.

### 1.4 `views.py` + `urls.py` (Steps 14–15) — `test_views.py`
- [x] **Tests** (using Django test client + the test_auth_decorator fixture for the header):
  - No `X-Forwarded-Session` header → 401, body `{"authenticated": false}`.
  - Invalid session key → 401.
  - Valid session but user inactive → 401.
  - Valid session, active user → 200; body contains `user_id`, `username`, `allowed_channels` from the callback.
  - Callback returns non-list → `TypeError` (Django will translate to 500).
  - GET request → 405 (require_POST).
  - Missing CSRF → still accepted (csrf_exempt).
- [x] **Implementation**: per Steps 14–15. Use `SessionStore` directly; do not implement session decoding in the gateway.

### 1.5 `publish.py` (Step 16) — `test_publish.py`
- [x] **Tests** (use fakeredis fixture):
  - `publish("user-42", {"x": 1})` calls `redis.publish` once with channel `"user-42"` and a JSON body `{"channel":"user-42","payload":{"x":1}}`.
  - The Redis client is cached (calling `publish` twice does not call `redis.from_url` twice).
  - Concurrent first calls from two threads share one client (lock test — spawn two threads, both call `publish` once; assert `redis.from_url` called exactly once).
- [x] **Implementation**: per Step 16.

### 1.6 `revocation.py` (Step 17) — `test_revocation.py`
- [x] **Tests**:
  - Firing `user_logged_out` with a request having a `session.session_key` → `publish` called on `_session:{key}` with payload `{"type":"revoke","reason":"logout"}`.
  - Firing `user_logged_out` with `request=None` → no publish, no exception.
  - Firing `user_logged_out` with a session having no `session_key` → no publish.
  - `force_logout_user(user)` → `publish` called on `_user:{user.pk}:revoke` with `{"type":"revoke","reason":"force_logout"}`.
- [x] **Implementation**: per Step 17.

### 1.7 `__init__.py` public surface (Step 18)
- [x] **Test**: `from websocket_gateway import publish, force_logout_user, __version__` succeeds; `__version__` matches `_version.__version__`.
- [x] **Implementation**: per Step 18.

## Definition of done for Phase 1

- `pytest -q` is green.
- `ruff check websocket_gateway/` clean.
- `ruff format --check websocket_gateway/` clean.
- **Every public module, class, and function in `websocket_gateway/` carries a Google-style docstring** (`Args:`, `Returns:`, `Raises:`, `Example:` where they apply). `mkdocstrings` will render these as the API reference in Phase 5 — they must read as user-facing documentation, not internal notes.
- Manual smoke: in a scratch Django project, add `"websocket_gateway"` to `INSTALLED_APPS`, set `WEBSOCKET_GATEWAY = {...}`, run `python manage.py check` — it passes.
- `python -c "from websocket_gateway import publish, force_logout_user, __version__; print(__version__)"` prints `0.1.0`.

## Hard-rule audit before commit

- [x] Grep the package for the test secret value — it must not appear in any production code path.
- [x] No `==` used to compare secrets anywhere (the pre-commit hook will block, but verify).
- [x] No `pdb`, `breakpoint()`, `print()` left behind.

## Notes

- Don't write the downloader or management command in this phase. Those depend on the Go binary existing — Phase 3.
- The Django session decode happens *in Django*, never in Go. That's Hard Rule #5/#6.
