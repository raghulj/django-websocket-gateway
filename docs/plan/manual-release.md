# Manual release runbook

The auto-publishing workflows (`release.yml`, `docs.yml`) were intentionally **removed** from `.github/workflows/` to keep external side effects (PyPI, GHCR, Pages) under explicit manual control. Re-add them whenever you are ready to automate.

This page is the runbook for doing the same work by hand on your laptop.

## Prerequisites

- A clone of the repo at the commit you want to release.
- The Go toolchain (1.22+), Python 3.10+, `uv` (or `pip`), Docker with buildx, and `gh` CLI authenticated against the repo.
- For PyPI: an API token from <https://pypi.org/manage/account/token/> (or a TestPyPI token at <https://test.pypi.org/manage/account/token/>) — store as `TWINE_PASSWORD`.
- For GHCR: `gh auth token` works as the registry password.

## Step 1 — Pre-flight

```bash
# Run the same checks CI runs.
.venv/bin/pytest -q
.venv/bin/ruff check .
.venv/bin/ruff format --check .
(cd gateway && go test -race ./... && go vet ./... && gofmt -s -l . | (! grep .))
node --test websocket_gateway/static/websocket_gateway/client.test.js
.venv/bin/mkdocs build --strict --config-file docs/mkdocs.yml --site-dir /tmp/site
```

All five suites must be green. If anything is red, fix and start over.

## Step 2 — Bump the version

Edit `websocket_gateway/_version.py`:

```python
__version__ = "0.1.0"
```

Commit the bump on its own:

```bash
git commit -am "Release v0.1.0"
git tag -a v0.1.0 -m "v0.1.0"
git push origin main --tags
```

## Step 3 — Build the gateway binaries

```bash
cd gateway
mkdir -p ../dist
for combo in "linux amd64" "linux arm64" "darwin amd64" "darwin arm64"; do
  os=${combo% *}; arch=${combo#* }
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch \
    go build -ldflags="-s -w" -o ../dist/gateway-$os-$arch .
done
cd ..
(cd dist && sha256sum gateway-* > SHA256SUMS)
cat dist/SHA256SUMS
```

## Step 4 — Create the GitHub Release

```bash
gh release create v0.1.0 \
  dist/gateway-* dist/SHA256SUMS \
  --title "v0.1.0" \
  --notes "First release."
```

## Step 5 — Build + publish the wheel to PyPI

Try TestPyPI first:

```bash
rm -rf dist-py && python -m build --outdir dist-py
python -m twine upload --repository testpypi dist-py/*
```

Verify with `pip install -i https://test.pypi.org/simple/ django-websocket-gateway==0.1.0` in a scratch venv.

When happy, upload to the real PyPI:

```bash
python -m twine upload dist-py/*
```

## Step 6 — Build + push the Docker image to GHCR

```bash
docker buildx create --use --name dwg-builder 2>/dev/null || docker buildx use dwg-builder
echo $(gh auth token) | docker login ghcr.io -u $(gh api user --jq .login) --password-stdin

docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --provenance=true \
  -t ghcr.io/raghulj/django-websocket-gateway:0.1.0 \
  -t ghcr.io/raghulj/django-websocket-gateway:latest \
  --push \
  gateway/
```

## Step 7 — Deploy the docs

If GitHub Pages is enabled (Settings → Pages → Source: `gh-pages` branch):

```bash
bash docs/scripts/render-godoc.sh
.venv/bin/mkdocs gh-deploy --config-file docs/mkdocs.yml --force --message "docs: v0.1.0"
```

This builds the site and pushes it to the `gh-pages` branch.

## Step 8 — Verify

- `pip install django-websocket-gateway==0.1.0` in a clean venv succeeds.
- `docker pull ghcr.io/raghulj/django-websocket-gateway:0.1.0` works.
- The GitHub Release page lists all four binaries + `SHA256SUMS`.
- The docs site at `https://raghulj.github.io/django-websocket-gateway/` renders.
- A fresh integration in a scratch Django project — five lines per Quickstart — yields working real-time.

## When you're ready to automate

Re-add `.github/workflows/release.yml` and `.github/workflows/docs.yml` from any commit before `4b1821a` (or from the spec in `/CLAUDE.md` Step 23). Configure repository secrets / Trusted Publisher first.
