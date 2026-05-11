# Phase 6 — CI/CD

← [Plan index](README.md)

## Goal

Three GitHub Actions workflows: tests on every push/PR, docs deploy on push to main, release on tag push. The release workflow builds four binaries with `SHA256SUMS`, publishes a wheel to PyPI, pushes a Docker image to GHCR, and creates a GitHub Release with all artifacts.

## Prerequisites

- Phases 1–5 complete (you have things to test, lint, and release).
- Repo secrets configured: `PYPI_API_TOKEN`. (GHCR uses `GITHUB_TOKEN`; Pages uses OIDC.)

## Tasks

### 6.1 `.github/workflows/test.yml`
- [x] **Triggers:** `push` to any branch, `pull_request` to `main`.
- [x] **Jobs:**
  - `python` — matrix `python-version: [3.10, 3.11, 3.12]` × `django-version: [4.2, 5.0]`. Steps: checkout, `setup-python`, `pip install -e .[dev]`, `pip install "django==${{ matrix.django-version }}.*"`, `ruff check .`, `ruff format --check .`, `pytest -q`.
  - `go` — runs once on `ubuntu-latest`. Steps: checkout, `setup-go` (1.22), `go test -race -cover ./gateway/...`, `golangci-lint run ./gateway/...`, `gofmt -s -l gateway/ | tee /dev/stderr | wc -l | grep -q '^0$'`.
  - `js` — runs once. Steps: checkout, `setup-node` 20, `npx prettier --check websocket_gateway/static/websocket_gateway/client.js`, run the `client-tests/` suite if present.
- [x] **Required check:** branch protection on `main` requires all three jobs.

### 6.2 `.github/workflows/release.yml`
- [x] **Trigger:** `push` of a tag matching `v*.*.*`.
- [x] **Jobs (sequential, fan-out where useful):**
  1. `build-binaries` — matrix `{os: linux, darwin} × {arch: amd64, arm64}`. Uses `setup-go`, runs `CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -ldflags="-s -w" -o gateway-$os-$arch ./gateway`. Uploads each as an artifact.
  2. `checksum` — depends on `build-binaries`. Downloads all artifacts, generates `SHA256SUMS` with `sha256sum gateway-*-*` (BSD-format also OK). Uploads.
  3. `release` — depends on `checksum`. Creates a GitHub Release for the tag, attaches all four binaries + `SHA256SUMS`.
  4. `python-publish` — bumps `websocket_gateway/_version.py` to the tag (strip leading `v`), builds wheel + sdist with `python -m build`, publishes via `pypa/gh-action-pypi-publish` using OIDC trusted publishing if configured (else `PYPI_API_TOKEN`).
  5. `docker-publish` — builds `gateway/Dockerfile` for `linux/amd64,linux/arm64` with `docker/build-push-action`; tags `ghcr.io/raghulj/django-websocket-gateway:<tag>` and `:latest`.
- [x] **Version sync:** the python-publish job edits `_version.py` in-tree before building. Do NOT commit that change back to `main`; it's tag-scoped.
- [x] **Provenance:** include `--provenance=true` on the docker build (free attestation).

### 6.3 `.github/workflows/docs.yml`
- [x] Use the sketch in `/CLAUDE.md` Step 23 verbatim. Pin actions to versions, not `@main`.
- [x] Add a `concurrency: { group: pages, cancel-in-progress: true }` to avoid races on rapid pushes.

## Definition of done for Phase 6

- `test.yml`: open a PR with a deliberately broken test → check fails. Fix → check passes.
- `release.yml`: push a `v0.1.0-rc1` tag → Release created with four binaries + SHA256SUMS; wheel on TestPyPI; image on GHCR. (Use TestPyPI / a draft release for the first dry run.)
- `docs.yml`: push to a docs branch and merge to main → Pages site updates.

## Notes

- Don't sign with cosign — explicitly out of scope per "Do NOT build". Just SHA-256.
- Don't add a `check_gateway` command — also out of scope.
- The release workflow runs **only on tag push**, not on commits to main. This keeps merges fast.
- TestPyPI is your friend for dry-running the publish before flipping to real PyPI.
