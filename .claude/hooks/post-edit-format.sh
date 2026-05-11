#!/usr/bin/env bash
# PostToolUse hook: auto-format files Claude just edited.
# Reads JSON from stdin, extracts tool_input.file_path, runs language-appropriate
# formatter. Silent on success; stderr (with exit 2) surfaces issues to Claude.
set -u

input=$(cat)
file_path=$(printf '%s' "$input" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("tool_input",{}).get("file_path","") or "")' 2>/dev/null)
[[ -z "$file_path" || ! -f "$file_path" ]] && exit 0

case "$file_path" in
  *.py)
    if command -v ruff >/dev/null 2>&1; then
      ruff format --quiet "$file_path" >/dev/null 2>&1 || true
      if ! ruff check --quiet "$file_path" 2>&1; then
        echo "ruff check found issues in $file_path" >&2
        exit 2
      fi
    fi
    ;;
  *.go)
    if command -v gofmt >/dev/null 2>&1; then
      gofmt -w "$file_path"
    fi
    if command -v go >/dev/null 2>&1; then
      pkg_dir=$(dirname "$file_path")
      if ! (cd "$pkg_dir" && go vet ./... 2>&1); then
        echo "go vet failed in $pkg_dir" >&2
        exit 2
      fi
    fi
    ;;
  *.js)
    if command -v prettier >/dev/null 2>&1; then
      prettier --write --log-level warn "$file_path" >/dev/null 2>&1 || true
    fi
    ;;
esac

exit 0
