#!/usr/bin/env bash
set -euo pipefail

VERSION="0.1.0"
ARCH="arm64"
OUTPUT_DIR=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --arch) ARCH="$2"; shift 2 ;;
    --output-dir) OUTPUT_DIR="$2"; shift 2 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done
[[ "$ARCH" == "arm64" || "$ARCH" == "amd64" ]] || { echo "Unsupported arch: $ARCH" >&2; exit 2; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SOURCE_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
OUTPUT_DIR="${OUTPUT_DIR:-$SOURCE_ROOT/release}"
DIST_ROOT="$SOURCE_ROOT/dist"
STAGE_NAME="gpt-agent-v${VERSION}-macos-${ARCH}"
STAGE_ROOT="$DIST_ROOT/$STAGE_NAME"
ARCHIVE="$OUTPUT_DIR/$STAGE_NAME.tar.gz"
SHA_FILE="$ARCHIVE.sha256"

say(){ printf '==> %s\n' "$*"; }
fail(){ printf 'ERROR: %s\n' "$*" >&2; exit 1; }
command -v go >/dev/null || fail "go is required"
command -v rsync >/dev/null || fail "rsync is required"
command -v python3 >/dev/null || fail "python3 is required"

say "Cleaning macOS release staging"
rm -rf "$STAGE_ROOT"
mkdir -p "$STAGE_ROOT" "$OUTPUT_DIR"

say "Checking Go formatting"
format_diff="$(find "$SOURCE_ROOT/go-core" -type f -name '*.go' -print0 | xargs -0 gofmt -d)"
[[ -z "$format_diff" ]] || { printf '%s\n' "$format_diff"; fail "Go formatting check failed"; }

say "Running Go tests and vet"
(cd "$SOURCE_ROOT/go-core" && go test ./... && go vet ./...)

say "Checking macOS shell syntax"
for script in "$SOURCE_ROOT"/scripts/macos/*.sh; do bash -n "$script"; done

say "Running public hygiene gate"
python3 - "$SOURCE_ROOT" <<'PY'
from pathlib import Path
import os,re,sys
root=Path(sys.argv[1])
skip_dirs={'.git','dist','release','node_modules','.venv-tools'}
skip_suffixes={'.exe','.zip','.gz','.tar'}
rules=[
    ('windows-user-path', re.compile(r'(?i)[A-Z]:\\Users\\[^\\\r\n]+\\')),
    ('macos-user-path', re.compile('/' + 'Users/' + r'[^/\r\n]+/')),
]
deny=os.environ.get('GPT_AGENT_PUBLIC_DENYLIST_FILE','').strip()
if deny:
    path=Path(deny)
    if not path.is_file():
        raise SystemExit('GPT_AGENT_PUBLIC_DENYLIST_FILE does not point to a readable file')
    for line in path.read_text(encoding='utf-8').splitlines():
        line=line.strip()
        if line and not line.startswith('#'):
            rules.append(('external',re.compile(line)))
secret=re.compile(r'(?i)\b(?:(?:sk|rk|sess)-[A-Za-z0-9._-]{20,}|(?:ghp|gho|github_pat)_[A-Za-z0-9._-]{20,})\b')
violations=[]
for file in root.rglob('*'):
    if not file.is_file() or any(part in skip_dirs for part in file.parts) or file.suffix.lower() in skip_suffixes:
        continue
    try:
        text=file.read_text(encoding='utf-8')
    except (UnicodeDecodeError,OSError):
        continue
    for source,pattern in rules:
        if pattern.search(text):
            violations.append(f'{file.relative_to(root)}: public hygiene denylist match ({source})')
    if not file.name.endswith('_test.go') and secret.search(text):
        violations.append(f'{file.relative_to(root)}: secret-like token pattern')
if violations:
    print('\n'.join(sorted(set(violations))),file=sys.stderr)
    raise SystemExit('Public hygiene gate failed')
PY

say "Copying source distribution"
rsync -a --delete \
  --exclude '.git' --exclude 'dist' --exclude 'release' --exclude 'node_modules' --exclude '.venv-tools' \
  --exclude '*.exe' --exclude '*.zip' --exclude '*.tar.gz' --exclude '*.sha256' \
  "$SOURCE_ROOT/" "$STAGE_ROOT/"

say "Building macOS binaries"
(
  cd "$SOURCE_ROOT/go-core"
  CGO_ENABLED=0 GOOS=darwin GOARCH="$ARCH" go build -trimpath -ldflags '-s -w' -o "$STAGE_ROOT/gpt-agent" .
  CGO_ENABLED=0 GOOS=darwin GOARCH="$ARCH" go build -trimpath -ldflags '-s -w' -o "$STAGE_ROOT/gpt-agent-setup" ./cmd/setup
)
chmod 755 "$STAGE_ROOT/gpt-agent" "$STAGE_ROOT/gpt-agent-setup" "$STAGE_ROOT"/scripts/macos/*.sh

if [[ "$(uname -s)" == "Darwin" ]] && { [[ "$(uname -m)" == "$ARCH" ]] || { [[ "$(uname -m)" == "x86_64" && "$ARCH" == "amd64" ]]; }; }; then
  say "Smoke testing macOS runtime binary"
  SMOKE_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/gpt-agent-macos-smoke.XXXXXX")"
  trap 'rm -rf "$SMOKE_ROOT"' EXIT
  cat >"$SMOKE_ROOT/config.json" <<JSON
{
  "server":{"host":"127.0.0.1","port":18768,"maxToolOutputChars":160000},
  "security":{"allowSecretFiles":false,"secretFilePatterns":[".env","*.key"],"httpAllowedHosts":["127.0.0.1","localhost","::1"],"safeNetworkDefault":false},
  "workspaces":[{"id":"main","root":"$STAGE_ROOT","enabled":true}],
  "safeCommands":{},"lspServers":{},"debugAdapters":{}
}
JSON
  GPT_AGENT_HOME="$SMOKE_ROOT" GPT_AGENT_APP_ROOT="$STAGE_ROOT" "$STAGE_ROOT/gpt-agent" >"$SMOKE_ROOT/stdout.log" 2>"$SMOKE_ROOT/stderr.log" &
  pid=$!
  trap 'kill "$pid" >/dev/null 2>&1 || true; rm -rf "$SMOKE_ROOT"' EXIT
  deadline=$((SECONDS+20)); healthy=0
  while (( SECONDS < deadline )); do
    if curl --fail --silent --max-time 2 http://127.0.0.1:18768/healthz | grep -q '"ok":true'; then healthy=1; break; fi
    sleep 0.25
  done
  kill "$pid" >/dev/null 2>&1 || true; wait "$pid" 2>/dev/null || true
  (( healthy == 1 )) || { cat "$SMOKE_ROOT/stdout.log" "$SMOKE_ROOT/stderr.log" >&2 || true; fail "macOS runtime smoke failed"; }
  rm -rf "$SMOKE_ROOT"; trap - EXIT
else
  say "Skipping runtime execution smoke because host architecture differs from target $ARCH"
fi

say "Generating MANIFEST.sha256.json"
python3 - "$STAGE_ROOT" "$VERSION" <<'PY'
from pathlib import Path
import hashlib,json,sys,datetime
root=Path(sys.argv[1]); version=sys.argv[2]; files={}
for p in sorted(x for x in root.rglob('*') if x.is_file() and x.name!='MANIFEST.sha256.json'):
    b=p.read_bytes(); files[p.relative_to(root).as_posix()]={'sha256':hashlib.sha256(b).hexdigest(),'bytes':len(b)}
manifest={'version':version,'generatedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'files':files}
(root/'MANIFEST.sha256.json').write_text(json.dumps(manifest,indent=2)+'\n',encoding='utf-8')
PY

say "Creating macOS archive"
rm -f "$ARCHIVE" "$SHA_FILE"
tar -czf "$ARCHIVE" -C "$STAGE_ROOT" .
hash="$(shasum -a 256 "$ARCHIVE" | awk '{print $1}')"
printf '%s  %s\n' "$hash" "$(basename "$ARCHIVE")" >"$SHA_FILE"
echo "Archive: $ARCHIVE"
echo "SHA256:  $hash"
echo "Files:   $(find "$STAGE_ROOT" -type f | wc -l | tr -d ' ')"
