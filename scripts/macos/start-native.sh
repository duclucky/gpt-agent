#!/usr/bin/env bash
set -euo pipefail

DOMAIN="gui/$(id -u)"
LAUNCH_AGENTS="$HOME/Library/LaunchAgents"
RUNTIME_LABEL="com.duclucky.gpt-agent.runtime"
TUNNEL_LABEL="com.duclucky.gpt-agent.tunnel"

ensure_loaded() {
  local label="$1" plist="$2"
  if launchctl print "$DOMAIN/$label" >/dev/null 2>&1; then
    return 0
  fi
  [[ -f "$plist" ]] || {
    echo "LaunchAgent plist is missing: $plist" >&2
    return 1
  }
  launchctl bootstrap "$DOMAIN" "$plist"
}

ensure_loaded "$RUNTIME_LABEL" "$LAUNCH_AGENTS/$RUNTIME_LABEL.plist"
launchctl kickstart -k "$DOMAIN/$RUNTIME_LABEL"

if [[ -f "$LAUNCH_AGENTS/$TUNNEL_LABEL.plist" ]]; then
  ensure_loaded "$TUNNEL_LABEL" "$LAUNCH_AGENTS/$TUNNEL_LABEL.plist"
  launchctl kickstart -k "$DOMAIN/$TUNNEL_LABEL"
fi
