#!/usr/bin/env bash
set -euo pipefail

INSTALL_ROOT="${GPT_AGENT_INSTALL_ROOT:-$HOME/.local/share/gpt-agent}"
WORKSPACE_ROOT="${GPT_AGENT_WORKSPACE_ROOT:-$HOME/Projects}"
SKIP_OPTIONAL_TOOLING=0
SKIP_TUNNEL_DOWNLOAD=0
SKIP_SETUP_WIZARD=0
PREFLIGHT_ONLY=0
RESET_RUNTIME_CONFIG=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --install-root) INSTALL_ROOT="$2"; shift 2 ;;
    --workspace-root) WORKSPACE_ROOT="$2"; shift 2 ;;
    --skip-optional-tooling) SKIP_OPTIONAL_TOOLING=1; shift ;;
    --skip-tunnel-download) SKIP_TUNNEL_DOWNLOAD=1; shift ;;
    --skip-setup-wizard) SKIP_SETUP_WIZARD=1; shift ;;
    --preflight-only) PREFLIGHT_ONLY=1; shift ;;
    --reset-runtime-config) RESET_RUNTIME_CONFIG=1; shift ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SOURCE_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
RUNTIME_ROOT="$INSTALL_ROOT/runtime"
DATA_ROOT="$INSTALL_ROOT/data"
CONFIG_ROOT="$INSTALL_ROOT/config"
BIN_ROOT="$INSTALL_ROOT/bin"
LOG_ROOT="$INSTALL_ROOT/logs"
NODE_TOOLING_ROOT="$INSTALL_ROOT/tooling-node"
PY_TOOLING_ROOT="$INSTALL_ROOT/tooling-python"
TMP_ROOT="$INSTALL_ROOT/tmp"
CONFIG_PATH="$DATA_ROOT/config.json"
RUNTIME_LABEL="com.duclucky.gpt-agent.runtime"
RUNTIME_PLIST="$HOME/Library/LaunchAgents/$RUNTIME_LABEL.plist"
DOMAIN="gui/$(id -u)"

say() { printf '==> %s\n' "$*"; }
warn() { printf 'WARN: %s\n' "$*" >&2; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }
xml_escape() {
  local value="$1"
  value="${value//&/&amp;}"
  value="${value//</&lt;}"
  value="${value//>/&gt;}"
  value="${value//\"/&quot;}"
  value="${value//\'/&apos;}"
  printf '%s' "$value"
}

if [[ "$(uname -s)" != "Darwin" ]]; then
  fail "This installer is for macOS only."
fi
if [[ "$(uname -m)" != "arm64" ]]; then
  fail "macOS MVP currently supports Apple Silicon (arm64) only."
fi
if [[ "$INSTALL_ROOT" == "/" || -z "$INSTALL_ROOT" ]]; then
  fail "Install root cannot be the filesystem root."
fi
if [[ "$SOURCE_ROOT" == "$RUNTIME_ROOT" ]]; then
  fail "Run the installer from an extracted release, not from the installed runtime directory."
fi
for required in config.example.json scripts/macos/run-native-runtime.sh scripts/macos/run-native-tunnel.sh; do
  [[ -f "$SOURCE_ROOT/$required" ]] || fail "Release source is incomplete; missing $required"
done

port_busy=0
if lsof -nP -iTCP:8765 -sTCP:LISTEN >/dev/null 2>&1; then port_busy=1; fi
runtime_loaded=0
if launchctl print "$DOMAIN/$RUNTIME_LABEL" >/dev/null 2>&1; then runtime_loaded=1; fi

if (( PREFLIGHT_ONLY == 1 )); then
  echo "GPT Agent macOS install preflight (no changes will be made)"
  echo "[OK] InstallRoot: $INSTALL_ROOT"
  if [[ -d "$WORKSPACE_ROOT" ]]; then echo "[OK] WorkspaceRoot: $WORKSPACE_ROOT"; else echo "[WARN] WorkspaceRoot: $WORKSPACE_ROOT (will be created)"; fi
  echo "[OK] Architecture: $(uname -m)"
  for command in curl unzip rsync launchctl lsof plutil; do
    if have "$command"; then echo "[OK] $command: available"; else echo "[WARN] $command: missing"; fi
  done
  for command in node npm git; do
    if have "$command"; then echo "[OK] $command: available"; elif have brew; then echo "[WARN] $command: missing; installer can use Homebrew"; else echo "[WARN] $command: missing and Homebrew is unavailable"; fi
  done
  if (( runtime_loaded == 1 )); then echo "[WARN] launchd runtime: already loaded; installer will replace it"; else echo "[OK] launchd runtime: not loaded"; fi
  if (( port_busy == 1 )); then
    if (( runtime_loaded == 1 )); then echo "[WARN] 127.0.0.1:8765: in use by existing runtime; installer will stop it before replacement"; else echo "[WARN] 127.0.0.1:8765: occupied without GPT Agent launchd service; install will fail until released"; fi
  else
    echo "[OK] 127.0.0.1:8765: available"
  fi
  if [[ -f "$CONFIG_PATH" ]]; then
    if (( RESET_RUNTIME_CONFIG == 1 )); then echo "[OK] Runtime config: $CONFIG_PATH will be regenerated"; else echo "[OK] Runtime config: $CONFIG_PATH will be preserved"; fi
  else
    echo "[OK] Runtime config: $CONFIG_PATH will be created"
  fi
  echo "Preflight complete. No files, packages, launchd agents, or services were changed."
  exit 0
fi

for command in curl unzip rsync launchctl lsof plutil; do
  have "$command" || fail "Required macOS command not found: $command"
done

ensure_brew_package() {
  local command="$1" package="$2"
  if have "$command"; then return 0; fi
  have brew || fail "$command is required. Install Homebrew or install $package manually, then retry."
  say "Installing prerequisite with Homebrew: $package"
  brew install "$package"
  have "$command" || fail "$command is still unavailable after installing $package"
}

ensure_brew_package node node
ensure_brew_package npm node
ensure_brew_package git git

say "Installing GPT Agent for macOS arm64"
printf 'Source:    %s\nRuntime:   %s\nData:      %s\nWorkspace: %s\n' "$SOURCE_ROOT" "$RUNTIME_ROOT" "$DATA_ROOT" "$WORKSPACE_ROOT"
mkdir -p "$INSTALL_ROOT" "$RUNTIME_ROOT" "$DATA_ROOT" "$CONFIG_ROOT" "$BIN_ROOT" "$LOG_ROOT" "$NODE_TOOLING_ROOT" "$PY_TOOLING_ROOT" "$TMP_ROOT" "$WORKSPACE_ROOT" "$HOME/Library/LaunchAgents"

export TMPDIR="$TMP_ROOT"
export npm_config_cache="$TMP_ROOT/npm-cache"
export PIP_CACHE_DIR="$TMP_ROOT/pip-cache"
mkdir -p "$npm_config_cache" "$PIP_CACHE_DIR"

say "Copying sanitized runtime distribution"
rsync -a --delete \
  --exclude '.git' --exclude 'node_modules' --exclude '.venv-tools' --exclude 'data' --exclude '.gpt-agent' --exclude 'dist' --exclude 'release' \
  "$SOURCE_ROOT/" "$RUNTIME_ROOT/"
rm -rf "$RUNTIME_ROOT/data"
mkdir -p "$RUNTIME_ROOT/data"
[[ -f "$SOURCE_ROOT/data/.gitkeep" ]] && cp "$SOURCE_ROOT/data/.gitkeep" "$RUNTIME_ROOT/data/.gitkeep"

for binary in gpt-agent gpt-agent-setup; do
  [[ -f "$SOURCE_ROOT/$binary" ]] || fail "Prebuilt release binary is missing: $binary"
  cp "$SOURCE_ROOT/$binary" "$BIN_ROOT/$binary"
  chmod 755 "$BIN_ROOT/$binary"
done
chmod 755 "$RUNTIME_ROOT/scripts/macos/"*.sh

say "Installing isolated Node language tooling"
cat >"$NODE_TOOLING_ROOT/package.json" <<'JSON'
{
  "name": "gpt-agent-macos-tooling",
  "private": true,
  "version": "1.0.0",
  "dependencies": {
    "typescript": "6.0.3",
    "typescript-language-server": "5.3.0",
    "pyright": "1.1.411",
    "bash-language-server": "5.6.0",
    "vscode-langservers-extracted": "4.10.0",
    "yaml-language-server": "1.24.0"
  },
  "overrides": { "editorconfig": { "minimatch": "10.2.6" } }
}
JSON
(
  cd "$NODE_TOOLING_ROOT"
  npm install --package-lock-only --ignore-scripts
  npm ci --ignore-scripts
  npm audit --audit-level=high
)

if have python3; then
  say "Creating Python tooling environment"
  rm -rf "$PY_TOOLING_ROOT"
  python3 -m venv "$PY_TOOLING_ROOT"
  "$PY_TOOLING_ROOT/bin/python" -m pip install --disable-pip-version-check --upgrade pip wheel
  "$PY_TOOLING_ROOT/bin/python" -m pip install --disable-pip-version-check debugpy semgrep
else
  warn "python3 not found; Python DAP/Semgrep will be unavailable."
  rm -rf "$PY_TOOLING_ROOT"
fi

if (( SKIP_OPTIONAL_TOOLING == 0 )) && have brew; then
  declare -a optional=("rg:ripgrep" "fd:fd" "jq:jq" "sqlite3:sqlite" "shellcheck:shellcheck")
  for entry in "${optional[@]}"; do
    command="${entry%%:*}"; package="${entry##*:}"
    if ! have "$command"; then
      say "Ensuring optional tool: $package"
      brew install "$package" || warn "Optional Homebrew package failed: $package"
    fi
  done
  if ! have go; then brew install go || warn "Optional Go install failed"; fi
fi

export PATH="$BIN_ROOT:$NODE_TOOLING_ROOT/node_modules/.bin:$PY_TOOLING_ROOT/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"
if have go; then
  say "Ensuring gopls v0.23.0"
  GOBIN="$BIN_ROOT" go install golang.org/x/tools/gopls@v0.23.0 || warn "gopls install failed; Go LSP will be disabled if unavailable."
fi

say "Preparing macOS runtime config"
if [[ -f "$CONFIG_PATH" && $RESET_RUNTIME_CONFIG -eq 0 ]]; then
  node -e 'JSON.parse(require("fs").readFileSync(process.argv[1],"utf8"))' "$CONFIG_PATH" >/dev/null || fail "Existing runtime config is invalid JSON: $CONFIG_PATH. Fix it or rerun with --reset-runtime-config."
  warn "Preserving existing runtime config: $CONFIG_PATH"
else
  CLANGD_PATH="$(command -v clangd || true)" RUST_ANALYZER_PATH="$(command -v rust-analyzer || true)" \
  SOURCE_CONFIG="$RUNTIME_ROOT/config.example.json" CONFIG_PATH="$CONFIG_PATH" WORKSPACE_ROOT="$WORKSPACE_ROOT" PY_TOOLING_ROOT="$PY_TOOLING_ROOT" BIN_ROOT="$BIN_ROOT" node <<'NODE'
const fs=require('fs');
const cfg=JSON.parse(fs.readFileSync(process.env.SOURCE_CONFIG,'utf8'));
cfg.workspaces=[{id:'main',root:process.env.WORKSPACE_ROOT,enabled:true}];
if (cfg.security) {
  cfg.security.requireLinuxFilesystem=false;
  cfg.security.processSandbox=false;
  cfg.security.requireProcessSandbox=false;
  cfg.security.safeNetworkDefault=false;
  cfg.security.requireProjectCwdForDriveWorkspace=false;
}
const py=process.env.PY_TOOLING_ROOT+'/bin/python';
if (fs.existsSync(py) && cfg.debugAdapters?.python) cfg.debugAdapters.python.command=py;
else if (cfg.debugAdapters) delete cfg.debugAdapters.python;
const gopls=process.env.BIN_ROOT+'/gopls';
if (cfg.lspServers?.go) {
  if (fs.existsSync(gopls)) cfg.lspServers.go.command=gopls;
  else delete cfg.lspServers.go;
}
const clangd=(process.env.CLANGD_PATH||'').trim();
for (const language of ['c','cpp']) {
  if (!cfg.lspServers?.[language]) continue;
  if (clangd) cfg.lspServers[language].command=clangd;
  else delete cfg.lspServers[language];
}
const rustAnalyzer=(process.env.RUST_ANALYZER_PATH||'').trim();
if (cfg.lspServers?.rust) {
  if (rustAnalyzer) cfg.lspServers.rust.command=rustAnalyzer;
  else delete cfg.lspServers.rust;
}
fs.writeFileSync(process.env.CONFIG_PATH,JSON.stringify(cfg,null,2)+'\n',{mode:0o600});
NODE
  chmod 600 "$CONFIG_PATH"
fi
mkdir -p "$DATA_ROOT/processes" "$DATA_ROOT/artifacts"

if (( SKIP_TUNNEL_DOWNLOAD == 0 )); then
  say "Installing latest official OpenAI tunnel-client for macOS arm64"
  release_json="$TMP_ROOT/tunnel-release.json"
  sums="$TMP_ROOT/SHA256SUMS.txt"
  zip="$TMP_ROOT/tunnel-client.zip"
  curl --fail --silent --show-error --location -H 'User-Agent: GPT-Agent-macOS-Installer' https://api.github.com/repos/openai/tunnel-client/releases/latest -o "$release_json"
  asset_info="$(node - "$release_json" <<'NODE'
const fs=require('fs'); const r=JSON.parse(fs.readFileSync(process.argv[2],'utf8'));
const zip=r.assets.find(a=>/^tunnel-client-v.*-darwin-arm64\.zip$/.test(a.name));
const sums=r.assets.find(a=>a.name==='SHA256SUMS.txt');
if(!zip||!sums) process.exit(2);
process.stdout.write([zip.name,zip.browser_download_url,sums.browser_download_url].join('\t'));
NODE
)" || fail "Could not find official darwin-arm64 tunnel-client release assets."
  IFS=$'\t' read -r asset_name asset_url sums_url <<<"$asset_info"
  curl --fail --silent --show-error --location "$asset_url" -o "$zip"
  curl --fail --silent --show-error --location "$sums_url" -o "$sums"
  expected="$(awk -v name="$asset_name" '$2==name || $2=="*"name {print $1; exit}' "$sums")"
  [[ -n "$expected" ]] || fail "Checksum entry missing for $asset_name"
  actual="$(shasum -a 256 "$zip" | awk '{print $1}')"
  [[ "$actual" == "$expected" ]] || fail "tunnel-client SHA256 mismatch"
  tunnel_tmp="$TMP_ROOT/tunnel-client-extract"
  rm -rf "$tunnel_tmp"; mkdir -p "$tunnel_tmp"
  unzip -q -o "$zip" -d "$tunnel_tmp"
  tunnel_bin="$(find "$tunnel_tmp" -type f -name tunnel-client -perm -u+x -print -quit)"
  if [[ -z "$tunnel_bin" ]]; then tunnel_bin="$(find "$tunnel_tmp" -type f -name tunnel-client -print -quit)"; fi
  [[ -n "$tunnel_bin" ]] || fail "tunnel-client executable missing from verified archive"
  cp "$tunnel_bin" "$BIN_ROOT/tunnel-client"; chmod 755 "$BIN_ROOT/tunnel-client"
  rm -rf "$tunnel_tmp" "$zip" "$sums" "$release_json"
fi

if (( runtime_loaded == 1 )); then
  say "Stopping existing GPT Agent runtime launchd agent"
  launchctl bootout "$DOMAIN/$RUNTIME_LABEL" >/dev/null 2>&1 || true
  deadline=$((SECONDS + 20))
  while lsof -nP -iTCP:8765 -sTCP:LISTEN >/dev/null 2>&1; do
    (( SECONDS < deadline )) || break
    sleep 1
  done
fi
if lsof -nP -iTCP:8765 -sTCP:LISTEN >/dev/null 2>&1; then
  fail "127.0.0.1:8765 is still occupied after stopping the prior GPT Agent service. Release the port and rerun the installer."
fi

say "Registering GPT Agent runtime launchd agent"
plist_label="$(xml_escape "$RUNTIME_LABEL")"
plist_runner="$(xml_escape "$RUNTIME_ROOT/scripts/macos/run-native-runtime.sh")"
plist_root="$(xml_escape "$INSTALL_ROOT")"
plist_stdout="$(xml_escape "$LOG_ROOT/runtime-launchd.log")"
plist_stderr="$(xml_escape "$LOG_ROOT/runtime-launchd.err.log")"
cat >"$RUNTIME_PLIST" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>$plist_label</string>
<key>ProgramArguments</key><array><string>/bin/bash</string><string>$plist_runner</string><string>$plist_root</string></array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>StandardOutPath</key><string>$plist_stdout</string>
<key>StandardErrorPath</key><string>$plist_stderr</string>
</dict></plist>
PLIST
chmod 644 "$RUNTIME_PLIST"
plutil -lint "$RUNTIME_PLIST" >/dev/null
launchctl bootout "$DOMAIN/$RUNTIME_LABEL" >/dev/null 2>&1 || true
launchctl bootstrap "$DOMAIN" "$RUNTIME_PLIST"
launchctl kickstart -k "$DOMAIN/$RUNTIME_LABEL"

deadline=$((SECONDS + 60))
until curl --fail --silent --show-error --max-time 2 http://127.0.0.1:8765/healthz >/dev/null 2>&1; do
  if (( SECONDS >= deadline )); then fail "GPT Agent did not become healthy after installation. Check $LOG_ROOT/runtime.log"; fi
  sleep 1
done

say "GPT Agent installation completed successfully"
echo "launchd: $RUNTIME_LABEL"
echo "MCP: http://127.0.0.1:8765/mcp"

if (( SKIP_TUNNEL_DOWNLOAD == 0 && SKIP_SETUP_WIZARD == 0 )); then
  "$BIN_ROOT/gpt-agent-setup" --install-root "$INSTALL_ROOT"
elif (( SKIP_TUNNEL_DOWNLOAD == 1 )); then
  warn "Tunnel setup skipped because tunnel-client download was disabled."
else
  echo "Tunnel setup: run $RUNTIME_ROOT/scripts/macos/configure-openai-tunnel.sh when ready."
fi
