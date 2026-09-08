#!/usr/bin/env bash
set -euo pipefail
DOMAIN="gui/$(id -u)"
launchctl bootout "$DOMAIN/com.duclucky.gpt-agent.tunnel" >/dev/null 2>&1 || true
launchctl bootout "$DOMAIN/com.duclucky.gpt-agent.runtime" >/dev/null 2>&1 || true
