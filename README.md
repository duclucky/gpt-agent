# GPT Agent

[![CI](https://github.com/duclucky/gpt-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/duclucky/gpt-agent/actions/workflows/ci.yml)
[![Latest Release](https://img.shields.io/github/v/release/duclucky/gpt-agent)](https://github.com/duclucky/gpt-agent/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**GPT Agent** is a local developer runtime for ChatGPT on Windows. It gives ChatGPT controlled access to your codebase, Git, terminal, language servers, debugger, SQLite tools, local HTTP services, reusable project memory, checkpoints, audit logs, and Windows Sandbox-backed command execution.

> Independent community project. Not an official OpenAI product and not affiliated with or endorsed by OpenAI.

## What it is

GPT Agent is intentionally **ChatGPT-only**. It does not embed a second coding agent, model router, external coding harness, or model-provider gateway. ChatGPT remains the reasoning/agent loop; GPT Agent is the local execution layer.

The Windows v0.1.0 build exposes **83 MCP tools**: 79 native tools plus 4 bounded async job tools.

**[Download the latest Windows release](https://github.com/duclucky/gpt-agent/releases/latest)**

Installation guide: [docs/INSTALL-ONCE.md](docs/INSTALL-ONCE.md)

## Architecture

```text
ChatGPT
  |
  | OpenAI Tunnel
  v
GPT Agent on 127.0.0.1:8765
  |
  +-- files / search / repo map
  +-- Git / checkpoints / patching
  +-- SAFE terminal / bounded FULL SHELL
  +-- LSP / diagnostics / symbol navigation
  +-- Python DAP debugger
  +-- SQLite read-only inspection
  +-- local HTTP requests
  +-- memory / reusable coding skills
  +-- audit chain / self-check
  +-- Windows Sandbox for isolated execution
```

Browser automation is intentionally not included in v0.1.0. It will only be added when it has a complete independently distributable runtime.

## Install on Windows

The release ZIP contains prebuilt `gpt-agent.exe` and `gpt-agent-setup.exe` binaries plus the installer scripts.

Open PowerShell in the extracted release directory and run:

```powershell
Set-ExecutionPolicy -Scope Process Bypass
.\scripts\windows\Install-GPTAgent.ps1 -WorkspaceRoot "D:\Projects"
```

Use the directory that actually contains the projects you want ChatGPT to access. Do **not** use an entire drive unless you intentionally want to expose that whole drive as one workspace.

By default the installer:

- installs GPT Agent under `C:\GPTAgent`;
- creates and starts the `GPT Agent Runtime` logon task;
- installs required/optional developer tooling;
- downloads the official OpenAI `tunnel-client` and verifies its published SHA-256 checksum;
- starts a one-time local setup wizard in your browser.

To skip the browser wizard during unattended installation:

```powershell
.\scripts\windows\Install-GPTAgent.ps1 -WorkspaceRoot "D:\Projects" -SkipSetupWizard
```

Run it later with:

```powershell
C:\GPTAgent\runtime\scripts\windows\Configure-OpenAITunnel.ps1
```

## Local setup wizard

The setup wizard binds only to `127.0.0.1` on a random port and uses a one-time random URL token. It guides you through:

1. Create or select an OpenAI Tunnel and copy its `tunnel_...` ID.
2. Create a **Restricted runtime API key** with **Tunnels Read + Use**.
3. Paste the Tunnel ID and runtime key into the local wizard.
4. GPT Agent saves the key into an ACL-restricted local secret file. The tunnel profile stores only a `file:` reference, never the literal key.
5. The wizard runs `tunnel-client doctor --explain`, registers/starts `GPT Agent Tunnel`, and waits for `/readyz`.
6. Open ChatGPT Settings → Connectors, choose **Connection: Tunnel**, then select or paste the same Tunnel ID.

See [docs/CONNECT-CHATGPT.md](docs/CONNECT-CHATGPT.md) for the full flow.

## Recommended ChatGPT setup

For ongoing engineering work, use GPT Agent inside a **ChatGPT Project** rather than a standalone one-off chat. Projects keep related chats, reference files, and Project Instructions together, which makes the engineering rules and repository context easier to reuse across sessions.

After connecting the Tunnel, create/open a ChatGPT Project for the codebase and add a small set of engineering rules under **Project settings → Project Instructions**.

See [Recommended ChatGPT Project setup](docs/PROJECT-INSTRUCTIONS.md) for a ready-to-paste public-safe Project Instructions template.

## Security defaults

- MCP server binds only to loopback.
- Secret-like files are blocked by default.
- SAFE command execution is preferred.
- Network is off by default for SAFE execution.
- FULL SHELL requires a local time-bounded grant.
- Destructive filesystem tools require explicit literal confirmation values.
- Git checkpoints are available before broad changes.
- Audit events are SHA-256 hash chained.
- Runtime API keys are not stored in source code or committed config.

Read [SECURITY.md](SECURITY.md) before exposing sensitive repositories.

## Developer commands

From `go-core`:

```powershell
go test ./...
go vet ./...
go build -trimpath -o ..\gpt-agent.exe .
go build -trimpath -o ..\gpt-agent-setup.exe .\cmd\setup
```

Build the distributable package from the repository root:

```powershell
.\scripts\windows\Build-Release.ps1
```

The builder runs tests, vet, PowerShell syntax checks, a legacy/secret hygiene gate, compiles both binaries, generates `MANIFEST.sha256.json`, creates the Windows ZIP, and writes its SHA-256 file.

## Documentation

- [Install once](docs/INSTALL-ONCE.md)
- [Connect ChatGPT](docs/CONNECT-CHATGPT.md)
- [Recommended ChatGPT Project setup](docs/PROJECT-INSTRUCTIONS.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Tool catalog](docs/TOOL-CATALOG.md)
- [Windows runtime](docs/WINDOWS-NATIVE.md)
- [Security policy](SECURITY.md)
- [Contributing](CONTRIBUTING.md)
- [Changelog](CHANGELOG.md)
- [Roadmap](ROADMAP.md)

## Maintainer and support

- Maintainer: [@duclucky](https://github.com/duclucky)
- Bugs: use the [bug report form](https://github.com/duclucky/gpt-agent/issues/new?template=bug_report.yml).
- Feature requests: use the [feature request form](https://github.com/duclucky/gpt-agent/issues/new?template=feature_request.yml).
- Security vulnerabilities: report them privately through [GitHub Private Vulnerability Reporting](https://github.com/duclucky/gpt-agent/security/advisories/new). Do not open a public security issue.

## License

MIT. See [LICENSE](LICENSE).
