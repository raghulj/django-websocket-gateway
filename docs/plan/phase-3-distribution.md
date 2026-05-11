# Phase 3 — Distribution glue

← [Plan index](README.md)

## Goal

Make `python manage.py runwsgateway` work: the management command downloads the right Go binary from GitHub Releases, verifies its SHA-256, caches it, and `execvpe`s into it. Built test-first.

## Prerequisites

- Phase 1 complete (`_config.get_config()` works, package importable).
- Phase 2 useful but not required — the downloader test suite uses a local HTTP fixture, not a real GitHub release.

## TDD discipline

The downloader has the highest security stakes outside the secret path: a tampered binary on the user's box is game over. Tests come first and cover **failure paths first** (mismatch, missing checksum, unsupported platform, env overrides).

## Tasks

### 3.1 `websocket_gateway/_downloader.py` (Step 19) — `test_downloader.py`
- [ ] **Tests** (use `pytest`'s `tmp_path` and a tiny `http.server.ThreadingHTTPServer` fixture):
  - **Happy path:** server hosts `SHA256SUMS` + `gateway-linux-amd64`; checksum matches → file lands at `bin/gateway-linux-amd64`, is executable.
  - **Checksum mismatch:** corrupt the binary → `DownloadError`; **no file at destination**; no `.dl-*` temp left behind.
  - **Checksum missing for binary:** `SHA256SUMS` doesn't list our binary name → `DownloadError`.
  - **`WS_GATEWAY_BINARY_PATH` set to existing file** → returns that path; no network call (assert the HTTP server logs zero requests).
  - **`WS_GATEWAY_BINARY_PATH` set to non-existent file** → `DownloadError`.
  - **`WS_GATEWAY_SKIP_DOWNLOAD=1` + no cached binary** → `DownloadError` with the documented hint.
  - **`WS_GATEWAY_DOWNLOAD_URL` override** → fetches from the override URL instead of GitHub.
  - **Unsupported platform** (mock `sys.platform="freebsd"`) → `DownloadError` naming Docker fallback.
  - **Cached binary, executable** → returns it without re-downloading.
  - **Cached binary, not executable** → re-downloads.
  - **Atomic move:** simulate a write failure between download and verify → no partial file at destination.
- [ ] **Implementation:** per Step 19. Use `tempfile.NamedTemporaryFile(dir=dest.parent)` so `shutil.move` is rename-only.

### 3.2 `management/commands/runwsgateway.py` (Step 20) — `test_runwsgateway.py`
- [ ] **Tests** (monkeypatch `os.execvpe` to capture args):
  - Invocation reads config, calls `ensure_binary`, then calls `os.execvpe` with the binary path and a translated env.
  - Env contains `INTERNAL_AUTH_SECRET`, `REDIS_URL`, `DJANGO_AUTH_URL` (default if not in settings), `ALLOWED_ORIGINS` joined by commas, `LISTEN_ADDR` (default `:8080`), `LOG_LEVEL`.
  - Optional keys (`MAX_CONNECTIONS_PER_USER` etc.) are passed through when set.
  - **Secret value not in stdout** — the success message names the binary path and version but not the secret.
- [ ] **Implementation:** per Step 20. Use `os.execvpe`, not `subprocess.Popen` — common pitfall.

### 3.3 Final hard-rule audit
- [ ] Grep `_downloader.py` and `runwsgateway.py`: no `print(...)` of the secret, no exception message containing it.
- [ ] `ruff check` + `ruff format --check` clean.

## Docstring requirement

- Every public symbol added in this phase (`ensure_binary`, `DownloadError`, `Command`, env-translation helper) carries a Google-style docstring. The downloader page in the docs site renders these directly.

## Definition of done for Phase 3

- `pytest websocket_gateway/tests/test_downloader.py websocket_gateway/tests/test_runwsgateway.py -q` green.
- Manual smoke test (Linux or macOS): point `WS_GATEWAY_DOWNLOAD_URL` at a local HTTP server hosting a hand-built `gateway-{os}-{arch}` and matching `SHA256SUMS`; run `python manage.py runwsgateway`; the Go process starts.
- `WS_GATEWAY_BINARY_PATH=/path/to/local-build/gateway python manage.py runwsgateway` works for development without any download.

## Notes

- The downloader fetching `SHA256SUMS` and the binary as separate requests is intentional: it lets us verify before writing executable bits.
- Never `chmod +x` until after checksum verification.
- `execvpe` replaces the Python process; the management command must be the last thing Python does. No code after the `os.execvpe` line.
