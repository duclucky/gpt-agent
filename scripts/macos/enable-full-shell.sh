#!/usr/bin/env bash
set -euo pipefail

MINUTES=30
INSTALL_ROOT="${GPT_AGENT_INSTALL_ROOT:-$HOME/.local/share/gpt-agent}"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --minutes) MINUTES="$2"; shift 2 ;;
    --install-root) INSTALL_ROOT="$2"; shift 2 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done

if ! [[ "$MINUTES" =~ ^[0-9]+$ ]]; then
  echo "--minutes must be an integer" >&2
  exit 2
fi
if (( MINUTES < 1 )); then MINUTES=1; fi
if (( MINUTES > 60 )); then MINUTES=60; fi

echo "FULL SHELL permits arbitrary shell commands inside the selected workspace." >&2
echo "Repository build scripts can be malicious. This grant is temporary." >&2
read -r -p "Type exactly FULL ACCESS to unlock for $MINUTES minutes: " confirm
if [[ "$confirm" != "FULL ACCESS" ]]; then
  echo "Confirmation mismatch." >&2
  exit 1
fi

GRANT_PATH="$INSTALL_ROOT/data/full-shell.grant.json"
mkdir -p "$(dirname "$GRANT_PATH")"
NOW="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
EXPIRES="$(date -u -v+"${MINUTES}"M +"%Y-%m-%dT%H:%M:%SZ")"
USER_NAME="$(id -un)"
cat >"$GRANT_PATH" <<JSON
{
  "confirmedByUser": true,
  "grantedAt": "$NOW",
  "expiresAt": "$EXPIRES",
  "user": "$USER_NAME"
}
JSON
chmod 600 "$GRANT_PATH"
echo "FULL SHELL unlocked for $MINUTES minutes."
