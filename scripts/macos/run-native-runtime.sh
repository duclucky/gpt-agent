#!/usr/bin/env bash
set -euo pipefail

INSTALL_ROOT="${1:-${GPT_AGENT_INSTALL_ROOT:-$HOME/.local/share/gpt-agent}}"
RUNTIME_ROOT="$INSTALL_ROOT/runtime"
DATA_ROOT="$INSTALL_ROOT/data"
LOG_ROOT="$INSTALL_ROOT/logs"
TMP_ROOT="$INSTALL_ROOT/tmp"
BIN_ROOT="$INSTALL_ROOT/bin"
NODE_BIN="$INSTALL_ROOT/tooling-node/node_modules/.bin"
PY_BIN="$INSTALL_ROOT/tooling-python/bin"

mkdir -p "$DATA_ROOT" "$LOG_ROOT" "$TMP_ROOT"
export TMPDIR="$TMP_ROOT"
export GPT_AGENT_HOME="$DATA_ROOT"
export GPT_AGENT_APP_ROOT="$RUNTIME_ROOT"
export GPT_AGENT_LOG_PATH="$LOG_ROOT/runtime.log"
export PIP_CACHE_DIR="$TMP_ROOT/pip-cache"
export GOPATH="$TMP_ROOT/go"
export GOMODCACHE="$GOPATH/pkg/mod"
export GOCACHE="$TMP_ROOT/go-cache"
export PATH="$BIN_ROOT:$NODE_BIN:$PY_BIN:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"

cd "$RUNTIME_ROOT"
CORE_BIN="$BIN_ROOT/gpt-agent"
if [[ ! -x "$CORE_BIN" ]]; then
  echo "GPT Agent runtime binary is missing or not executable: $CORE_BIN" >&2
  exit 1
fi
exec "$CORE_BIN"
