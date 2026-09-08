# macOS support

GPT Agent has an Apple Silicon macOS target built from the same Go core and the same 83-tool MCP catalog as Windows.

## Supported MVP

- macOS on Apple Silicon (`darwin/arm64`).
- User-level install; no root daemon is required.
- Runtime and Tunnel auto-start through `launchd` LaunchAgents.
- OpenAI Tunnel setup through the same local setup wizard used on Windows.
- Project memory, adaptive self-learning, Git/file/search/project tools, SAFE command execution, LSP, DAP, SQLite, local HTTP, checkpoints, audit chain, and async jobs.
- FULL SHELL through the native macOS shell (`zsh`, with `bash`/`sh` fallback) after a local time-bounded grant.

The public MCP catalog remains 83 tools. `gpt_agent_untrusted_run` is intentionally unavailable on macOS MVP because GPT Agent does not yet claim a local isolation backend equivalent to Windows Sandbox. It returns an explicit capability error instead of silently executing with weaker containment.

## Install

Download/extract the macOS arm64 release, then from that extracted directory:

```bash
./scripts/macos/install-gpt-agent.sh --workspace-root "$HOME/Projects" --preflight-only
./scripts/macos/install-gpt-agent.sh --workspace-root "$HOME/Projects"
```

Default install root:

```text
~/.local/share/gpt-agent
```

The installer keeps runtime-managed mutable state outside the copied runtime tree:

```text
~/.local/share/gpt-agent/data
~/.local/share/gpt-agent/config
~/.local/share/gpt-agent/logs
```

The configured workspace is separate and defaults to `$HOME/Projects` unless `--workspace-root` is supplied. Rerunning the installer preserves `data/config.json` by default. Use `--reset-runtime-config` only when you intentionally want to regenerate it from the release defaults and the supplied workspace root.

## Prerequisites

macOS provides `curl`, `unzip`, `rsync`, `launchctl`, and `lsof`. GPT Agent also needs Node/npm and Git. If one of those is missing and Homebrew is already installed, the installer can install the missing package through Homebrew. It does not install Homebrew itself.

Optional developer tooling is installed where available. Use `--skip-optional-tooling` for a leaner install.

## launchd

Runtime label:

```text
com.duclucky.gpt-agent.runtime
```

Tunnel label after Tunnel setup:

```text
com.duclucky.gpt-agent.tunnel
```

LaunchAgent plists live under:

```text
~/Library/LaunchAgents
```

Start/stop helpers:

```bash
./scripts/macos/start-native.sh
./scripts/macos/stop-native.sh
```

## Tunnel setup

If the browser wizard was skipped during installation:

```bash
~/.local/share/gpt-agent/runtime/scripts/macos/configure-openai-tunnel.sh
```

The wizard stores the restricted runtime key locally with file mode `0600`, writes only a `file:` reference into the Tunnel profile, registers the Tunnel LaunchAgent, and verifies `/readyz` before reporting success.

## FULL SHELL

FULL SHELL remains locally gated and time bounded:

```bash
~/.local/share/gpt-agent/runtime/scripts/macos/enable-full-shell.sh --minutes 30
```

The command requires the literal confirmation `FULL ACCESS`.

## Uninstall

Default uninstall removes runtime/tooling/LaunchAgents while preserving local data, Tunnel config, and the configured workspace root:

```bash
~/.local/share/gpt-agent/runtime/scripts/macos/uninstall-gpt-agent.sh --confirm UNINSTALL
```

To also remove GPT Agent's local data/config:

```bash
~/.local/share/gpt-agent/runtime/scripts/macos/uninstall-gpt-agent.sh \
  --purge-local-data --confirm UNINSTALL
```

`--purge-workspace` removes only `$INSTALL_ROOT/workspace` when that install-local directory exists; it does not follow or delete an external workspace such as `$HOME/Projects`.

## Build and release contract

On macOS:

```bash
./scripts/macos/build-release.sh --arch arm64
./scripts/macos/test-release-contract.sh --arch arm64
```

The release contract verifies Go tests/vet, shell syntax, native Apple Silicon runtime smoke, `launchd` runtime lifecycle, archive checksum, `MANIFEST.sha256.json`, the 83-tool catalog/self-learning schema, installer preflight no-side-effect behavior, and safe uninstaller preserve/purge behavior.

The current build workflow does not apply an Apple Developer ID signature or notarization. Treat CI/source-built archives as engineering artifacts until signing/notarization is added for a public macOS release.
