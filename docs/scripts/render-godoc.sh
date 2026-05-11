#!/usr/bin/env bash
# Generate docs/docs/reference/go.md from the gateway package's godoc.
#
# Usage: bash docs/scripts/render-godoc.sh
#
# Re-run after any change to public Go API or to godoc comments. CI runs
# this and fails if the working copy is dirty afterwards — the committed
# go.md must always match the source.

set -euo pipefail

cd "$(dirname "$0")/../.."

target="docs/docs/reference/go.md"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

{
  cat <<'HEADER'
# Go gateway API reference

Generated from godoc comments by `docs/scripts/render-godoc.sh`. **Do
not edit this file by hand** — change the comments in `gateway/*.go` and
re-run the script.

```
HEADER

  (cd gateway && go doc -all .)

  printf '```\n'
} > "$tmp"

mv "$tmp" "$target"
trap - EXIT
echo "wrote $target"
