#!/usr/bin/env bash
set -euo pipefail

VERSION="0.1.0"
ARCH="arm64"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --arch) ARCH="$2"; shift 2 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SOURCE_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
TEMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/gpt-agent-macos-contract.XXXXXX")"
LIFECYCLE_LABEL=""
cleanup() {
  if [[ -n "$LIFECYCLE_LABEL" ]] && command -v launchctl >/dev/null 2>&1; then
    launchctl bootout "gui/$(id -u)/$LIFECYCLE_LABEL" >/dev/null 2>&1 || true
  fi
  rm -rf "$TEMP_ROOT"
}
trap cleanup EXIT
OUTPUT_ROOT="$TEMP_ROOT/release"
EXTRACT_ROOT="$TEMP_ROOT/extracted"
FAKE_INSTALL="$TEMP_ROOT/fake-install"
PREFLIGHT_INSTALL="$TEMP_ROOT/preflight-install"
PREFLIGHT_WORKSPACE="$TEMP_ROOT/preflight-workspace"
mkdir -p "$OUTPUT_ROOT" "$EXTRACT_ROOT" "$PREFLIGHT_WORKSPACE"

bash "$SCRIPT_DIR/build-release.sh" --version "$VERSION" --arch "$ARCH" --output-dir "$OUTPUT_ROOT"
name="gpt-agent-v${VERSION}-macos-${ARCH}"
archive="$OUTPUT_ROOT/$name.tar.gz"
sha_file="$archive.sha256"
[[ -f "$archive" && -f "$sha_file" ]] || { echo "release archive/checksum missing" >&2; exit 1; }
expected="$(awk '{print $1}' "$sha_file")"
actual="$(shasum -a 256 "$archive" | awk '{print $1}')"
[[ "$expected" == "$actual" ]] || { echo "release checksum mismatch" >&2; exit 1; }
tar -xzf "$archive" -C "$EXTRACT_ROOT"

for required in gpt-agent gpt-agent-setup MANIFEST.sha256.json scripts/macos/install-gpt-agent.sh scripts/macos/uninstall-gpt-agent.sh scripts/macos/run-native-runtime.sh scripts/macos/run-native-tunnel.sh docs/MACOS.md go-core/tool_catalog.json; do
  [[ -f "$EXTRACT_ROOT/$required" ]] || { echo "required release file missing: $required" >&2; exit 1; }
done

python3 - "$EXTRACT_ROOT" "$VERSION" <<'PY'
from pathlib import Path
import hashlib,json,sys
root=Path(sys.argv[1]); version=sys.argv[2]
m=json.loads((root/'MANIFEST.sha256.json').read_text())
assert m['version']==version, (m['version'],version)
for name,meta in m['files'].items():
    p=(root/name).resolve()
    assert root.resolve() in p.parents or p==root.resolve()
    b=p.read_bytes(); assert len(b)==meta['bytes']; assert hashlib.sha256(b).hexdigest()==meta['sha256']
cat=json.loads((root/'go-core/tool_catalog.json').read_text())
assert len(cat)==83, len(cat)
learn=next(x for x in cat if x['name']=='gpt_agent_learn')
props=learn['inputSchema']['properties']
assert 'candidateIds' in props and 'dismissCandidateIds' in props
PY

for script in "$EXTRACT_ROOT"/scripts/macos/*.sh; do bash -n "$script"; done

if [[ "$(uname -s)" == "Darwin" && "$(uname -m)" == "arm64" ]]; then
  "$EXTRACT_ROOT/scripts/macos/install-gpt-agent.sh" --install-root "$PREFLIGHT_INSTALL" --workspace-root "$PREFLIGHT_WORKSPACE" --preflight-only --skip-optional-tooling --skip-tunnel-download --skip-setup-wizard
  [[ ! -e "$PREFLIGHT_INSTALL" ]] || { echo "preflight mutated install root" >&2; exit 1; }

  lifecycle_root="$TEMP_ROOT/lifecycle-install"
  lifecycle_runtime="$lifecycle_root/runtime"
  lifecycle_data="$lifecycle_root/data"
  lifecycle_workspace="$lifecycle_root/workspace"
  lifecycle_logs="$lifecycle_root/logs"
  mkdir -p "$lifecycle_root/bin" "$lifecycle_runtime" "$lifecycle_data" "$lifecycle_workspace" "$lifecycle_logs" "$lifecycle_root/tmp"
  rsync -a "$EXTRACT_ROOT/" "$lifecycle_runtime/"
  cp "$EXTRACT_ROOT/gpt-agent" "$lifecycle_root/bin/gpt-agent"
  chmod 755 "$lifecycle_root/bin/gpt-agent" "$lifecycle_runtime/scripts/macos/run-native-runtime.sh"
  python3 - "$lifecycle_data/config.json" "$lifecycle_workspace" <<'PY'
import json,sys
path,workspace=sys.argv[1:]
cfg={
  'server':{'host':'127.0.0.1','port':18769,'maxToolOutputChars':160000},
  'security':{'allowSecretFiles':False,'secretFilePatterns':['.env','*.key'],'httpAllowedHosts':['127.0.0.1','localhost','::1'],'safeNetworkDefault':False},
  'workspaces':[{'id':'main','root':workspace,'enabled':True}],
  'safeCommands':{},'lspServers':{},'debugAdapters':{}
}
open(path,'w',encoding='utf-8').write(json.dumps(cfg)+'\n')
PY
  LIFECYCLE_LABEL="com.duclucky.gpt-agent.contract.$PPID"
  lifecycle_plist="$TEMP_ROOT/$LIFECYCLE_LABEL.plist"
  cat >"$lifecycle_plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>$LIFECYCLE_LABEL</string>
<key>ProgramArguments</key><array><string>/bin/bash</string><string>$lifecycle_runtime/scripts/macos/run-native-runtime.sh</string><string>$lifecycle_root</string></array>
<key>RunAtLoad</key><true/>
<key>StandardOutPath</key><string>$lifecycle_logs/launchd.log</string>
<key>StandardErrorPath</key><string>$lifecycle_logs/launchd.err.log</string>
</dict></plist>
PLIST
  plutil -lint "$lifecycle_plist" >/dev/null
  domain="gui/$(id -u)"
  launchctl bootstrap "$domain" "$lifecycle_plist"
  launchctl kickstart -k "$domain/$LIFECYCLE_LABEL"
  deadline=$((SECONDS+20)); healthy=0
  while (( SECONDS < deadline )); do
    if curl --fail --silent --max-time 2 http://127.0.0.1:18769/healthz | grep -q '"ok":true'; then healthy=1; break; fi
    sleep 0.25
  done
  if (( healthy != 1 )); then
    cat "$lifecycle_logs/launchd.log" "$lifecycle_logs/launchd.err.log" >&2 2>/dev/null || true
    echo "launchd runtime lifecycle smoke failed" >&2
    exit 1
  fi
  launchctl bootout "$domain/$LIFECYCLE_LABEL"
  LIFECYCLE_LABEL=""
fi

for d in bin runtime logs tooling-node tooling-python tmp data config workspace; do
  mkdir -p "$FAKE_INSTALL/$d"; printf '%s\n' "$d" >"$FAKE_INSTALL/$d/sentinel.txt"
done
uninstaller="$EXTRACT_ROOT/scripts/macos/uninstall-gpt-agent.sh"
if "$uninstaller" --install-root "$FAKE_INSTALL" --skip-launchd >/dev/null 2>&1; then
  echo "uninstaller accepted missing confirmation" >&2; exit 1
fi
"$uninstaller" --install-root "$FAKE_INSTALL" --skip-launchd --confirm UNINSTALL
for d in bin runtime logs tooling-node tooling-python tmp; do [[ ! -e "$FAKE_INSTALL/$d" ]] || { echo "$d survived default uninstall" >&2; exit 1; }; done
for d in data config workspace; do [[ -f "$FAKE_INSTALL/$d/sentinel.txt" ]] || { echo "$d was not preserved" >&2; exit 1; }; done
"$uninstaller" --install-root "$FAKE_INSTALL" --skip-launchd --purge-local-data --purge-workspace --confirm UNINSTALL
[[ ! -e "$FAKE_INSTALL" ]] || { echo "purge uninstall left fake install root" >&2; exit 1; }

echo "macOS release contract passed: $name"
