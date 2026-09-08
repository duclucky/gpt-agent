#!/usr/bin/env bash
set -euo pipefail

INSTALL_ROOT="${1:-${GPT_AGENT_INSTALL_ROOT:-$HOME/.local/share/gpt-agent}}"
SETUP_BIN="$INSTALL_ROOT/bin/gpt-agent-setup"
RUNTIME_LABEL="com.duclucky.gpt-agent.runtime"
DOMAIN="gui/$(id -u)"

if [[ ! -x "$SETUP_BIN" ]]; then
  echo "gpt-agent-setup is missing or not executable: $SETUP_BIN" >&2
  exit 1
fi

if ! curl --fail --silent --show-error --max-time 2 http://127.0.0.1:8765/healthz >/dev/null 2>&1; then
  launchctl kickstart -k "$DOMAIN/$RUNTIME_LABEL" >/dev/null 2>&1 || true
  deadline=$((SECONDS + 60))
  until curl --fail --silent --show-error --max-time 2 http://127.0.0.1:8765/healthz >/dev/null 2>&1; do
    if (( SECONDS >= deadline )); then
      echo "GPT Agent runtime is not healthy on 127.0.0.1:8765." >&2
      exit 1
    fi
    sleep 1
  done
fi

exec "$SETUP_BIN" --install-root "$INSTALL_ROOT"
