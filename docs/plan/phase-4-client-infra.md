# Phase 4 — JS client + reference infra

← [Plan index](README.md)

## Goal

Ship the small browser client and the reference deployment files (`docker-compose.example.yml`, `Caddyfile.example`, `.env.example`) so a user can stand up a working stack in five minutes. Built test-first for the JS portion; the infra files are reviewed via manual `docker compose up`.

## Prerequisites

- Phase 2 complete (the gateway binary exists, even if locally built).
- Phase 3 complete (so `runwsgateway` can launch it).

## Tasks

### 4.1 `static/websocket_gateway/client.js` (Step 21) — `test_client.js`
- [x] **Test setup:** use `vitest` (or stdlib `node:test` with `jsdom`) to run `client.js` in a fake browser. Add `vitest`, `jsdom`, `mock-socket` as a `package.json` dev dep under `client-tests/` (a tiny subdir; we do NOT publish an npm package).
- [x] **Tests:**
  - `new WSClient(url).subscribe("ch")` after `onopen` → sends one `{action:"subscribe",channel:"ch"}` frame.
  - Subscribing while not yet open → frame is sent on next `onopen`.
  - `unsubscribe("ch")` after open → frame sent; channel removed from set.
  - `on("ch", h)` then incoming `{channel:"ch", payload:{a:1}}` → handler called with `{a:1}`.
  - Incoming `{type:"error", channel, reason}` → handler NOT called; warning logged.
  - Malformed JSON → silently ignored.
  - `onclose` with code 4401 → no reconnect attempt.
  - `onclose` with code 1006 (network drop) → reconnect with exponential backoff capped at 30s, jittered.
  - `.close()` → no reconnect.
- [x] **Implementation:** per Step 21. Single file, ES2022, no build step.
- [x] **Lint:** `npx prettier --check client.js`.

### 4.2 `docker-compose.example.yml`
- [x] Services: `django` (placeholder image, build context note in comments), `gateway` (uses GHCR image), `redis:7`, `caddy:2`.
- [x] Env vars sourced from `.env`.
- [x] Volumes: a named volume for Caddy data.
- [x] Container `ulimits` for the gateway: `nofile: 65535`.
- [x] Healthchecks: gateway uses `/healthz`; redis uses `redis-cli ping`.
- [x] **Comment** at the top: "This is a reference. Replace the `django` image with your app."

### 4.3 `Caddyfile.example`
- [x] Reverse-proxy `/` to `django:8000`, `/ws/*` to `gateway:8080`.
- [x] **Explicitly `respond /internal/* 404` before the django proxy.** This is the public-blocking rule from the Testing checklist.
- [x] `tls` block parameterized via env / `{$DOMAIN}`.
- [x] WebSocket headers handled (Caddy 2 does this by default for `reverse_proxy`, but comment confirms it).

### 4.4 `.env.example`
- [x] Document every variable used by the compose stack, with a comment per line.
- [x] **Never** a real-looking secret value. Use `CHANGEME_GENERATE_WITH_python_-c_secrets_token_urlsafe_48` so it's obviously a placeholder.
- [x] List: `WS_INTERNAL_SECRET`, `REDIS_URL`, `DJANGO_AUTH_URL`, `ALLOWED_ORIGINS`, `DOMAIN`, `DJANGO_SECRET_KEY`.

## Definition of done for Phase 4

- `node --test client-tests/` (or `vitest run`) green.
- `prettier --check websocket_gateway/static/websocket_gateway/client.js` clean.
- `docker compose -f docker-compose.example.yml config` parses without errors.
- `caddy validate --config Caddyfile.example --adapter caddyfile` clean.
- Manual smoke: `docker compose up`; `curl https://localhost/healthz` returns 200; `curl https://localhost/internal/ws-auth/` returns 404 from Caddy.

## Notes

- The JS client is **one file** delivered as a Django static asset. No bundler, no npm package on the public registry. The `client-tests/` dir exists only for test infra and is `.gitignore`-d at the npm-artifact level (`node_modules/`).
- The Caddy block on `/internal/*` is the second line of defence; the first is the `X-Internal-Auth` header check on the Django view. Don't remove either.
