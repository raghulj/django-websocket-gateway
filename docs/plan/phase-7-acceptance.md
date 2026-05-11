# Phase 7 — Acceptance

← [Plan index](README.md)

## Goal

Walk the full **Testing checklist** and **Definition of done** in `/CLAUDE.md` against a fresh local stack and a real release pipeline run. Anything that fails goes back to its owning phase for a fix. Nothing in this phase invents new behaviour.

## Prerequisites

- Phases 0–6 complete and committed.

## Tasks

### 7.1 Testing checklist in `/CLAUDE.md`

Tick each off after exercising it. Reproduce the scenario, capture evidence (log line, screenshot, or test name), and move on. Reference the section in `/CLAUDE.md` under "Testing checklist".

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
- [ ] `LogoutView` triggers WS close within ~100ms with code 4401.
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

### 7.2 Definition of done

- [ ] `pip install django-websocket-gateway` from TestPyPI succeeds.
- [ ] Five-line integration in a fresh Django app yields working real-time.
- [ ] `python manage.py runwsgateway` downloads binary on first run; subsequent runs use cache.
- [ ] End-to-end demo: user logs in → browser opens WS → `publish()` from Django shell appears in browser within 50ms.
- [ ] User logs out → WS closes within 100ms; client does not reconnect.
- [ ] `docker compose stop redis && docker compose start redis` → messages still flow.
- [ ] Tag push triggers binary build, PyPI publish, Docker image push, docs deploy.
- [ ] Tests in the checklist pass.
- [ ] `mkdocs build --strict` clean.

### 7.3 Post-acceptance house-keeping

- [ ] Bump `_version.py` to `0.1.0`, push the `v0.1.0` tag (real PyPI this time).
- [ ] Update `README.md` if the install snippet has drifted.
- [ ] File any follow-ups (presence channels, message replay, secret rotation) into a `v1.1` issue list.

## Definition of done for Phase 7

Every checkbox above ticked. If anything failed: write the failing test in the owning phase, fix, re-run.
