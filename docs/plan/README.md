# Build Plan — `django-websocket-gateway`

This folder is the working plan for delivering v1 of `django-websocket-gateway` against the spec in [`/CLAUDE.md`](../../CLAUDE.md). Each phase below has a dedicated file with full context, requirements, and acceptance criteria — pick a phase, read its file, execute.

## How to use this plan

- Phases are ordered by dependency. Phase 0 must complete before anything else. Phases 1 and 2 are independent of each other and can be parallelized.
- Inside each phase file, tasks are listed as checkboxes. **When you finish a task, edit both the phase file and the matching line in this README to flip `- [ ]` → `- [x]`.**
- Every phase file links back here. Every task references the relevant Step number in `/CLAUDE.md` so you can pull the exact code shape and rules without re-reading the whole spec.
- Hard rules from `/CLAUDE.md` (secret handling, single-goroutine hub, etc.) apply to **every** task. Re-read the "Hard rules" section if you're touching auth, the hub, or the downloader.

## Tooling decisions (locked)

- **Methodology:** TDD (red-green-refactor). See the "Development methodology: TDD" section of `/CLAUDE.md`. **Every task below is `test → implement → refactor` — never write production code before the failing test exists.**
- **Python tests:** `pytest` + `pytest-django`, declared as a `dev` optional-dependency in `pyproject.toml`. Test settings module lives at `websocket_gateway/tests/settings.py`.
- **Go tests:** stdlib `testing`. No external test framework. `*_test.go` next to the file under test.
- **Python lint/format:** `ruff check` + `ruff format` (line length 100, rules `E,W,F,I,UP,B,SIM,RUF`).
- **Go lint:** `gofmt -s`, `go vet ./...`, `golangci-lint run`.
- **JS:** `prettier --check` on `client.js`.
- **Pre-commit:** `git config core.hooksPath .githooks` after clone — the hook enforces secret-handling rules and the linters.
- **Docs:** MkDocs + Material theme, built with `--strict` in CI.

## Status overview

### Phase 0 — Repo scaffolding → [phase-0-scaffolding.md](phase-0-scaffolding.md)
- [x] 0.1 Create directory tree
- [x] 0.2 `LICENSE` (MIT) + `.gitignore` review
- [x] 0.3 `pyproject.toml` (Step 9) with `dev` extras for pytest
- [x] 0.4 `websocket_gateway/_version.py` (Step 10)
- [x] 0.5 `gateway/go.mod` initialization (Go 1.22, websocket + go-redis deps)
- [x] 0.6 `.editorconfig` + `gateway/.golangci.yml` + pre-commit hook activated

### Phase 1 — Python package core → [phase-1-python-core.md](phase-1-python-core.md)
- [x] 1.1 `_config.py` (Step 11) — validation, no secret echo
- [x] 1.2 `apps.py` (Step 12) — startup validation + signal connect
- [x] 1.3 `auth_decorator.py` (Step 13) — `hmac.compare_digest`
- [x] 1.4 `views.py` + `urls.py` (Steps 14–15)
- [x] 1.5 `publish.py` (Step 16) — thread-safe lazy Redis client
- [x] 1.6 `revocation.py` (Step 17) — signal handler + `force_logout_user`
- [x] 1.7 `__init__.py` public surface (Step 18)
- [x] 1.8 Test settings module + `conftest.py`
- [x] 1.9 `test_config.py`
- [x] 1.10 `test_auth_decorator.py`
- [x] 1.11 `test_views.py`
- [x] 1.12 `test_publish.py`
- [x] 1.13 `test_revocation.py`

### Phase 2 — Go gateway → [phase-2-go-gateway.md](phase-2-go-gateway.md)
- [x] 2.1 `gateway/config.go` (Step 1)
- [x] 2.2 `gateway/auth.go` (Step 2)
- [x] 2.3 `gateway/redis.go` (Step 5)
- [x] 2.4 `gateway/hub.go` (Step 3)
- [x] 2.5 `gateway/client.go` (Step 4)
- [x] 2.6 `gateway/health.go` (Step 6)
- [x] 2.7 `gateway/main.go` (Step 7) — wiring + `wsHandler`
- [x] 2.8 `gateway/Dockerfile` (Step 8)
- [x] 2.9 Go tests (hub semantics, auth, redis reconnect)

### Phase 3 — Distribution glue → [phase-3-distribution.md](phase-3-distribution.md)
- [x] 3.1 `_downloader.py` (Step 19)
- [x] 3.2 `management/commands/runwsgateway.py` (Step 20)
- [x] 3.3 `test_downloader.py`

### Phase 4 — JS client + reference infra → [phase-4-client-infra.md](phase-4-client-infra.md)
- [x] 4.1 `static/websocket_gateway/client.js` (Step 21)
- [x] 4.2 `docker-compose.example.yml`
- [x] 4.3 `Caddyfile.example` (blocks `/internal/*`)
- [x] 4.4 `.env.example`

### Phase 5 — MkDocs site → [phase-5-docs-site.md](phase-5-docs-site.md)
- [x] 5.1 `docs/mkdocs.yml` + `docs/requirements.txt`
- [x] 5.2 `docs/docs/index.md`
- [x] 5.3 `docs/docs/quickstart.md`
- [x] 5.4 `docs/docs/architecture.md`
- [x] 5.5 `docs/docs/authentication.md`
- [x] 5.6 `docs/docs/channels.md`
- [x] 5.7 `docs/docs/publishing.md`
- [x] 5.8 `docs/docs/background-jobs.md`
- [x] 5.9 `docs/docs/logout.md`
- [x] 5.10 `docs/docs/javascript-client.md`
- [x] 5.11 `docs/docs/deployment.md`
- [x] 5.12 `docs/docs/configuration.md`
- [x] 5.13 `docs/docs/threat-model.md`

### Phase 6 — CI/CD → [phase-6-ci-cd.md](phase-6-ci-cd.md)
- [x] 6.1 `.github/workflows/test.yml`
- [x] 6.2 `.github/workflows/release.yml`
- [x] 6.3 `.github/workflows/docs.yml`

### Phase 7 — Acceptance → [phase-7-acceptance.md](phase-7-acceptance.md)
- [x] 7.1 Walk the Testing checklist (suite-level items verified locally; release-dependent items deferred for manual smoke after first GitHub release)
- [x] 7.2 Audit the Definition of done (8/9 verified locally; PyPI install awaits first release)

## Cross-cutting reminders

0. **Docstrings and godoc comments are the docs site.** Every public symbol carries a complete, user-facing docstring (Google-style for Python) or godoc comment. MkDocs renders them via `mkdocstrings`. See the "Documentation from code" section of `/CLAUDE.md`.
1. **The secret never appears in logs, exceptions, or responses.** Audit every `log.*`, `raise`, and `Json/HttpResponse` call.
2. **Always `hmac.compare_digest` / `crypto/subtle.ConstantTimeCompare`** for secret comparison — never `==`.
3. **Hub state mutates only in the hub goroutine.** All cross-goroutine communication via channels.
4. **Reject `_`-prefixed channels on client-initiated subscribes.** Auto-subscribe to control channels server-side at handshake.
5. **`os.execvpe`, not `subprocess.Popen`.** The management command replaces the process.
6. **Downloaded binaries always SHA-256 verified.** Mismatch is a hard error.
