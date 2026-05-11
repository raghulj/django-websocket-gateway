#!/usr/bin/env bash
# PostToolUse hook: scan the file Claude just touched for the project's
# hard-rule violations from CLAUDE.md:
#   - secrets being logged, returned, or stringified
#   - == comparison on secret values (must be hmac.compare_digest /
#     crypto/subtle.ConstantTimeCompare)
#   - leftover debug statements (pdb, breakpoint, fmt.Println in non-test Go)
#
# Exits 2 with stderr to surface findings to Claude without blocking the edit.
set -u

input=$(cat)
file_path=$(printf '%s' "$input" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("tool_input",{}).get("file_path","") or "")' 2>/dev/null)
[[ -z "$file_path" || ! -f "$file_path" ]] && exit 0

# Skip docs/tests/configuration where these patterns are legitimate examples.
case "$file_path" in
  */docs/*|*/CLAUDE.md|*/README.md|*/.claude/hooks/*|*/.githooks/*) exit 0 ;;
esac

violations=()

scan() { # pattern, message
  local pattern="$1" message="$2"
  if grep -nE "$pattern" "$file_path" >/dev/null 2>&1; then
    violations+=("$message")
    grep -nE "$pattern" "$file_path" | head -3 | sed 's/^/    /' >&2
  fi
}

case "$file_path" in
  *.py)
    # Secret-leak patterns (Python).
    scan '(logger|logging|log)\.[a-z]+\([^)]*INTERNAL_SECRET' \
         "secret may be logged (CLAUDE.md hard rule #3)"
    scan '(print|raise [A-Za-z]+)\([^)]*INTERNAL_SECRET' \
         "secret may appear in print/exception (CLAUDE.md hard rule #3)"
    scan 'INTERNAL_SECRET[[:space:]]*==|==[[:space:]]*[^=]*INTERNAL_SECRET' \
         "use hmac.compare_digest, not == on secret (CLAUDE.md hard rule #4)"
    # Debug leftovers.
    scan '(^|[[:space:]])(pdb\.set_trace|breakpoint)\(' \
         "debug statement (pdb/breakpoint) committed"
    ;;
  *.go)
    scan '(slog|log)\.[A-Za-z]+\([^)]*InternalAuthSecret' \
         "secret may be logged (CLAUDE.md hard rule #3)"
    scan 'InternalAuthSecret[[:space:]]*==|==[[:space:]]*[^=]*InternalAuthSecret' \
         "use crypto/subtle.ConstantTimeCompare, not == (CLAUDE.md hard rule #4)"
    case "$file_path" in
      *_test.go) ;;
      *) scan 'fmt\.Print(ln|f)?\(' "fmt.Print* in non-test Go (use slog)" ;;
    esac
    ;;
esac

if (( ${#violations[@]} > 0 )); then
  printf '\nsecret-leak/style check failed in %s:\n' "$file_path" >&2
  for v in "${violations[@]}"; do printf '  - %s\n' "$v" >&2; done
  exit 2
fi

exit 0
