#!/usr/bin/env bash
set -euo pipefail

INSTALL_ROOT="${1:-${GPT_AGENT_INSTALL_ROOT:-$HOME/.local/share/gpt-agent}}"
BIN="$INSTALL_ROOT/bin/tunnel-client"
PROFILE_DIR="$INSTALL_ROOT/config/tunnel-client"
DATA_ROOT="$INSTALL_ROOT/data"
LOG_ROOT="$INSTALL_ROOT/logs"
TMP_ROOT="$INSTALL_ROOT/tmp"
HEALTH_URL_FILE="$DATA_ROOT/tunnel-health.url"

mkdir -p "$DATA_ROOT" "$LOG_ROOT" "$TMP_ROOT"
rm -f "$HEALTH_URL_FILE"
export TMPDIR="$TMP_ROOT"
export PATH="$INSTALL_ROOT/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"

if [[ ! -x "$BIN" ]]; then
  echo "tunnel-client is missing or not executable: $BIN" >&2
  exit 1
fi
if [[ ! -f "$PROFILE_DIR/gpt-agent.yaml" ]]; then
  echo "GPT Agent tunnel is not configured. Run scripts/macos/configure-openai-tunnel.sh first." >&2
  exit 1
fi

deadline=$((SECONDS + 90))
until curl --fail --silent --show-error --max-time 2 http://127.0.0.1:8765/healthz >/dev/null 2>&1; do
  if (( SECONDS >= deadline )); then
    echo "GPT Agent runtime did not become healthy before tunnel startup." >&2
    exit 1
  fi
  sleep 1
done

exec "$BIN" run --profile gpt-agent --profile-dir "$PROFILE_DIR" \
  --health.url-file "$HEALTH_URL_FILE" \
  --log.file "$LOG_ROOT/tunnel.log"
