# Phase 5 — MkDocs site

← [Plan index](README.md)

## Goal

Build the MkDocs Material site under `docs/`. **The API reference pages are generated from code comments via `mkdocstrings` (Python) and `go doc -all` (Go) — docstrings and godoc comments are the source of truth, not hand-maintained reference pages.** The narrative pages (quickstart, architecture, threat model, etc.) are still hand-written. Everything deploys to GitHub Pages from CI (Phase 6).

## Prerequisites

- Phases 1–4 complete (you're documenting actual behaviour and pulling from real docstrings).
- Every public symbol in `websocket_gateway/` and `gateway/` has a complete docstring / godoc comment per the "Documentation from code" section of `/CLAUDE.md`. If any are missing, fix them in the owning phase before continuing here.

## Tasks

### 5.1 `docs/mkdocs.yml` + `docs/requirements.txt`
- [x] `docs/requirements.txt`:
  - `mkdocs`
  - `mkdocs-material`
  - `mkdocstrings[python]`
  - `pymdown-extensions`
- [x] `docs/mkdocs.yml`:
  - `site_name: django-websocket-gateway`
  - `docs_dir: docs` (so `docs/docs/*.md` are the source files).
  - `theme: name: material`, with `features: [navigation.sections, navigation.expand, content.code.copy, content.code.annotate]`.
  - `markdown_extensions: [admonition, pymdownx.superfences, pymdownx.tabbed, toc]`.
  - `plugins:`
    - `search`
    - `mkdocstrings`:
      - `default_handler: python`
      - Python handler options: `show_source: false`, `show_root_heading: true`, `merge_init_into_class: true`, `docstring_style: google`, `members_order: source`, `show_signature_annotations: true`.
      - `paths: [..]` so the handler can import `websocket_gateway` from the repo root.
  - `nav:` ordered as below.
  - `strict: true`.

Nav order:
1. Home → `index.md`
2. Quickstart → `quickstart.md`
3. Architecture → `architecture.md`
4. Authentication → `authentication.md`
5. Channels → `channels.md`
6. Publishing → `publishing.md`
7. Background jobs → `background-jobs.md`
8. Logout & revocation → `logout.md`
9. JavaScript client → `javascript-client.md`
10. Deployment → `deployment.md`
11. Configuration → `configuration.md`
12. Threat model → `threat-model.md`
13. **API reference**
    - Python → `reference/python.md`
    - Go gateway → `reference/go.md`

### 5.2 Hand-written narrative pages

Each lives at `docs/docs/<name>.md`. Write from real behaviour — read the source before writing the page. Keep examples copy-pasteable; CI does **not** execute them but reviewers will.

- [x] **`index.md`** — one-paragraph elevator pitch, the architecture diagram from `README.md`, link to Quickstart.
- [x] **`quickstart.md`** — five-minute install→running guide. Expand the `README.md` quickstart with screenshots/notes. Include `git config core.hooksPath .githooks` for contributors.
- [x] **`architecture.md`** — components, data flow, decisions (why Go for the gateway, why Redis pub/sub, why no presence). 200–300 lines.
- [x] **`authentication.md`** — dedicated secret, validation rules at startup, threat coverage (link to threat-model.md), rotation guidance (v1: change settings + restart both processes).
- [x] **`channels.md`** — naming rules, `_` reserved for control channels, the authorization callback contract, common patterns (`user-{id}`, `org-{id}`, `room-{slug}`). End with `:::: mkdocstrings` for `channels_for_user` example signature.
- [x] **`publishing.md`** — `publish()` from views, signals, Celery; payload JSON contract; "no subscribers → no error" semantics. Embed the `publish` docstring with `::: websocket_gateway.publish.publish`.
- [x] **`background-jobs.md`** — Celery example, broker-vs-pubsub DB separation.
- [x] **`logout.md`** — the `user_logged_out` signal pipeline, `force_logout_user`, behaviour matrix (logout vs ban vs session expiry). Embed `::: websocket_gateway.revocation.force_logout_user`.
- [x] **`javascript-client.md`** — `WSClient` API reference. Hand-written because mkdocstrings doesn't render JS; copy the documented surface from `client.js` JSDoc.
- [x] **`deployment.md`** — Docker Compose walkthrough, Caddy config, GHCR image option, scaling notes.
- [x] **`configuration.md`** — exhaustive reference of every `WEBSOCKET_GATEWAY` settings key and every env var the gateway reads. Generated partly from the `_config` docstring (`::: websocket_gateway._config`) but the env-var table is hand-maintained.
- [x] **`threat-model.md`** — STRIDE-style table.

### 5.3 Auto-generated reference pages

- [x] **`reference/python.md`** — thin per-module stubs that delegate to mkdocstrings:
  ```
  # Python API reference

  The package's public surface, generated from docstrings.

  ## `websocket_gateway`
  ::: websocket_gateway
      options:
        members: [publish, force_logout_user, __version__]

  ## `websocket_gateway.publish`
  ::: websocket_gateway.publish

  ## `websocket_gateway.revocation`
  ::: websocket_gateway.revocation

  ## `websocket_gateway.views`
  ::: websocket_gateway.views

  ## `websocket_gateway._config`
  ::: websocket_gateway._config
  ```
  Internal modules (`_downloader`, `_config`) are intentionally rendered because they describe configuration; private helpers within them are filtered by mkdocstrings defaults (members starting with `_` are hidden unless explicitly listed).

- [x] **`reference/go.md`** — generated at build time. Add a `docs/scripts/render-godoc.sh` that runs `go doc -all ./gateway/... | pandoc -f plain -t gfm > docs/docs/reference/go.md.tmp` then prepends a header. Wire it into a `Makefile` target `make docs-godoc` and call it from CI before `mkdocs build`. **The committed `reference/go.md` is the generated artefact** so contributors can preview without running the script; CI regenerates and fails if the diff is non-empty (regression guard against stale docs).

  Acceptance: changing a godoc comment, running `make docs-godoc`, committing the regenerated page is the standard workflow. CI catches drift.

### 5.4 Doc-style audit
- [x] `python -c "import pydocstyle; pydocstyle('websocket_gateway/')"` — or `ruff check --select D websocket_gateway/` once we add the `D` ruleset for the docs pass. (Optional: add ruff `D` rules to `pyproject.toml` after Phase 1 lands.)
- [x] Spot-check rendered API pages with `mkdocs serve`: open `/reference/python/` and confirm every public symbol has a usable description.
- [x] No "TODO", "FIXME", or "see source" in any docstring. If you couldn't describe it, the API isn't ready.

## Definition of done for Phase 5

- `make docs-godoc` regenerates `reference/go.md` cleanly.
- `mkdocs build --strict --config-file docs/mkdocs.yml --site-dir _site` succeeds.
- No broken internal links (`--strict` enforces this).
- `mkdocs serve` locally: every nav entry loads, the API reference pages render docstrings (not raw markdown placeholders).
- Every code example in the docs is copy-pasteable and works against the actual code in the repo.

## Notes

- **`docs/plan/` is internal**. Exclude it from the published site by giving it no nav entry; with `strict: true`, MkDocs Material's `not_in_nav` setting can suppress the warning. Alternative: move plan to `.plan/` at repo root and drop the `docs/plan/` link from the README. Decide during this phase.
- mkdocstrings reads docstrings at build time; CI must `pip install -e .[dev]` before `mkdocs build` so the handler can import `websocket_gateway`.
- The Go reference page is regenerated, not hand-written. If you find yourself editing `reference/go.md` directly, you're editing the wrong file — fix the godoc comment in `gateway/` and re-run the generator.
