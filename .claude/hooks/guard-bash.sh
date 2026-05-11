#!/usr/bin/env bash
# PreToolUse hook for Bash: block patterns that violate project rules even
# if Claude tries to run them (belt-and-suspenders to settings.json deny list).
set -u

cmd=$(cat | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("tool_input",{}).get("command","") or "")' 2>/dev/null)

block() {
  echo "blocked by .claude/hooks/guard-bash.sh: $1" >&2
  exit 2
}

# Never bypass commit hooks or signing (CLAUDE.md harness rule).
[[ "$cmd" == *"--no-verify"* ]]    && block "use of --no-verify"
[[ "$cmd" == *"--no-gpg-sign"* ]]  && block "use of --no-gpg-sign"

# Never echo/cat the secret env var.
if echo "$cmd" | grep -qE '(echo|printf|cat).*INTERNAL_SECRET|INTERNAL_AUTH_SECRET'; then
  block "command appears to print INTERNAL_SECRET / INTERNAL_AUTH_SECRET"
fi

# Discourage destructive git ops without explicit user request (mirrors deny list).
[[ "$cmd" =~ git[[:space:]]+push.*--force ]]   && block "git push --force"
[[ "$cmd" =~ git[[:space:]]+reset[[:space:]]+--hard ]] && block "git reset --hard"

exit 0
