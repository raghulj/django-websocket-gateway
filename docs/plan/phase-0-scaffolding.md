# Phase 0 — Repo scaffolding

← [Plan index](README.md)

## Goal

Create the directory tree, packaging files, and tooling configuration so that subsequent phases can write tests and implementations without re-doing project setup. Nothing in this phase has runtime behaviour; the bar is "the project is importable, `pytest` collects zero tests, `go build ./...` succeeds with an empty `main`."

## Prerequisites

- Repo is git-initialized (it is).
- Local `python3.10+`, `go1.22+`, `ruff`, `golangci-lint` installed.

## Tasks

### 0.1 Create directory tree
- [x] Lay down every directory listed in the "Repository layout" section of `/CLAUDE.md`.
- [x] `websocket_gateway/bin/.gitkeep` and `websocket_gateway/tests/__init__.py` must exist (empty).
- [x] Every Python package directory gets an `__init__.py` (empty).

Acceptance: `find . -type d` matches the layout in the spec.

### 0.2 `LICENSE` + `.gitignore` review
- [x] Add MIT `LICENSE` (year 2026, copyright holder placeholder — confirm with project owner before publishing).
- [x] Review `.gitignore` to ensure `websocket_gateway/bin/gateway-*` (downloaded binaries), `*.egg-info/`, `dist/`, `.pytest_cache/`, `.ruff_cache/`, `_site/` are all ignored.

### 0.3 `pyproject.toml` (CLAUDE.md Step 9)
- [x] Use the exact `[project]` block from Step 9.
- [x] **Add** a `[project.optional-dependencies]` `dev` extra: `pytest`, `pytest-django`, `fakeredis`, `ruff`.
- [x] **Add** a `[tool.ruff]` block: `line-length = 100`, `target-version = "py310"`, `lint.select = ["E","W","F","I","UP","B","SIM","RUF"]`.
- [x] **Add** a `[tool.pytest.ini_options]` block: `DJANGO_SETTINGS_MODULE = "websocket_gateway.tests.settings"`, `python_files = "test_*.py"`.

Acceptance: `pip install -e .[dev]` succeeds in a fresh venv. `ruff check .` passes (over an empty tree).

### 0.4 `websocket_gateway/_version.py` (CLAUDE.md Step 10)
- [x] Single line: `__version__ = "0.1.0"`.
- [x] Comment above: noted that the release workflow rewrites this from the git tag.

### 0.5 `gateway/go.mod` initialization
- [x] `cd gateway && go mod init github.com/raghulj/django-websocket-gateway/gateway`.
- [x] `go get github.com/coder/websocket@latest github.com/redis/go-redis/v9@latest`.
- [x] Commit `go.mod` and `go.sum`.
- [x] Add `gateway/main.go` with a stub `package main; func main() {}` so `go build ./...` succeeds.

Acceptance: `go build ./...` inside `gateway/` produces a binary; `go vet ./...` clean.

### 0.6 `golangci-lint` config + `.editorconfig`
- [x] `gateway/.golangci.yml` enabling: `errcheck, staticcheck, gosimple, govet, ineffassign, unused`.
- [x] Root `.editorconfig`: 4-space indent for Python, tab indent for Go, 2-space for JS/YAML/MD.

### 0.7 Activate pre-commit hook
- [x] Run `git config core.hooksPath .githooks` and document it in `docs/docs/quickstart.md` (or note it for Phase 5).

## Definition of done for Phase 0

- `pip install -e .[dev]` succeeds.
- `pytest` runs (collects zero tests, exits 5 — that's fine).
- `go build ./...` and `go vet ./...` in `gateway/` succeed.
- `ruff check .` and `ruff format --check .` are clean.
- `git status` is clean after committing the scaffold.

## Notes

- This phase has no tests to write because there is no behaviour yet. The TDD rule resumes in Phase 1.
- Don't add CI workflows here — Phase 6 owns CI. The pre-commit hook is enough for now.
