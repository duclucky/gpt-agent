#!/usr/bin/env bash
set -euo pipefail

INSTALL_ROOT="${GPT_AGENT_INSTALL_ROOT:-$HOME/.local/share/gpt-agent}"
PURGE_LOCAL_DATA=0
PURGE_WORKSPACE=0
SKIP_LAUNCHD=0
CONFIRM=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --install-root) INSTALL_ROOT="$2"; shift 2 ;;
    --purge-local-data) PURGE_LOCAL_DATA=1; shift ;;
    --purge-workspace) PURGE_WORKSPACE=1; shift ;;
    --skip-launchd) SKIP_LAUNCHD=1; shift ;;
    --confirm) CONFIRM="$2"; shift 2 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done

if [[ "$CONFIRM" != "UNINSTALL" ]]; then
  echo "Refusing to uninstall without --confirm UNINSTALL." >&2
  exit 1
fi

INSTALL_ROOT="$(cd "$(dirname "$INSTALL_ROOT")" 2>/dev/null && pwd)/$(basename "$INSTALL_ROOT")"
if [[ "$INSTALL_ROOT" == "/" || -z "$INSTALL_ROOT" ]]; then
  echo "Refusing unsafe install root: $INSTALL_ROOT" >&2
  exit 1
fi

DOMAIN="gui/$(id -u)"
RUNTIME_LABEL="com.duclucky.gpt-agent.runtime"
TUNNEL_LABEL="com.duclucky.gpt-agent.tunnel"
LAUNCH_AGENTS="$HOME/Library/LaunchAgents"
if (( SKIP_LAUNCHD == 0 )); then
  launchctl bootout "$DOMAIN/$TUNNEL_LABEL" >/dev/null 2>&1 || true
  launchctl bootout "$DOMAIN/$RUNTIME_LABEL" >/dev/null 2>&1 || true
  rm -f "$LAUNCH_AGENTS/$TUNNEL_LABEL.plist" "$LAUNCH_AGENTS/$RUNTIME_LABEL.plist"
fi

for relative in bin runtime logs tooling-node tooling-python tmp; do
  rm -rf "$INSTALL_ROOT/$relative"
done

if (( PURGE_LOCAL_DATA == 1 )); then
  rm -rf "$INSTALL_ROOT/data" "$INSTALL_ROOT/config"
else
  [[ -e "$INSTALL_ROOT/data" ]] && echo "Preserved: $INSTALL_ROOT/data"
  [[ -e "$INSTALL_ROOT/config" ]] && echo "Preserved: $INSTALL_ROOT/config"
fi

if (( PURGE_WORKSPACE == 1 )); then
  rm -rf "$INSTALL_ROOT/workspace"
else
  [[ -e "$INSTALL_ROOT/workspace" ]] && echo "Preserved: $INSTALL_ROOT/workspace"
fi

if [[ -d "$INSTALL_ROOT" ]] && [[ -z "$(find "$INSTALL_ROOT" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
  rmdir "$INSTALL_ROOT"
fi

echo "GPT Agent uninstall completed."
