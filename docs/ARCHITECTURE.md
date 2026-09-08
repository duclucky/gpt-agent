# Architecture

GPT Agent is a local-first MCP developer runtime for ChatGPT. `v0.1.0` shipped Windows first; the same Go core now also targets Apple Silicon macOS under `[Unreleased]`.

## Control plane

The `gpt-agent` Go binary (`gpt-agent.exe` on Windows) is the only MCP runtime process. It binds to `127.0.0.1:8765`, serves `/mcp` and `/healthz`, owns the tool catalog, dispatches all native tools, manages async jobs, LSP sessions, debugger sessions, managed processes, learning state, and audit state.

```text
ChatGPT
  |
  | OpenAI Tunnel
  v
OpenAI tunnel-client
  |
  | http://127.0.0.1:8765/mcp
  v
GPT Agent Go runtime
  |
  +-- project/file/Git operations
  +-- process manager
  |   +-- Windows Sandbox backend (Windows only)
  +-- LSP manager
  +-- Python DAP manager
  +-- SQLite inspection
  +-- local HTTP client
  +-- persistent memory + procedural skills
  +-- audit chain + self-check
```

There is no embedded LLM, secondary coding agent, model router, or model-provider gateway. ChatGPT remains the sole semantic decision maker and agent loop.

## Tool surface

The public MCP surface contains 83 tools:

- 79 synchronous Go-native tools;
- 4 async job tools for operations that may outlive the tunnel response window.

All public tool names use the `gpt_agent_` prefix.

## Workspace boundary

Every filesystem/project tool requires a configured workspace ID. Paths are resolved beneath that workspace root. GPT Agent does not expose arbitrary machine paths merely because they exist.

The platform installer configures one `main` workspace from the user-supplied workspace root. Additional workspaces can be added manually to the local config later.

## Command execution

GPT Agent has two execution classes:

- **SAFE**: executable + argv, no shell interpolation, workspace/project scoped, network off by default.
- **UNTRUSTED**: disposable secret-sanitized project copy with a verified isolation backend. Windows uses Windows Sandbox. macOS MVP returns an explicit capability error until an equivalent isolation backend is implemented and verified.
- **FULL SHELL**: arbitrary PowerShell on Windows or native POSIX shell on macOS, available only while a local time-bounded grant is active.

The default policy is to prefer specialized tools and SAFE execution.

## Local setup wizard

`gpt-agent-setup` (`gpt-agent-setup.exe` on Windows) is a separate one-time configuration helper. It:

- listens only on IPv4 loopback;
- uses a random port;
- requires a one-time random token on every local request;
- rejects cross-origin requests;
- sets no-store/referrer/CSP protections;
- never stores the runtime API key in source code or the tunnel profile;
- shuts down when setup is finished or after a timeout.

The wizard writes the OpenAI tunnel runtime key to a permissions-restricted local secret file (Windows ACL or macOS mode `0600`), then asks `tunnel-client` to reference it by `file:`. Windows registers Scheduled Tasks; macOS registers user LaunchAgents with `launchd`.

## Persistent data

The default installation separates immutable-ish runtime files from mutable data:

Windows:

```text
C:\GPTAgent\runtime\   copied release/runtime source
C:\GPTAgent\bin\       runtime/setup/helper binaries
C:\GPTAgent\data\      config, learning state, artifacts
C:\GPTAgent\config\    tunnel profile and local secrets
C:\GPTAgent\logs\      runtime/tunnel logs
```

macOS defaults to the same logical layout under `~/.local/share/gpt-agent/`, with user LaunchAgents stored under `~/Library/LaunchAgents/`.

## Learning

The learning layer stores curated project-scoped facts, decisions, lessons, and reusable procedural `SKILL.md` content. `gpt_agent_coding_brief` opens a bounded evolution session that records outcome evidence such as mutations, verification passes/failures, broad failure classes, and selected skills. It does not persist raw tool arguments or raw tool results.

Completed tasks become `verified`, `failed`, `corrected`, or `incomplete`. Evidence-backed candidates remain `pending` until semantic review. `gpt_agent_learn` can mark reviewed candidates `promoted` while saving durable memory/skill changes, or `dismissed` when the evidence is noise or too task-specific. Only pending candidates are returned in future coding briefs.

Task text is sanitized before persistence. Automatic learning is bounded and is not intended to persist credentials, transient logs, guesses, or private chain-of-thought. See [SELF-LEARNING.md](SELF-LEARNING.md).

## Audit

Security-sensitive operations are written to a SHA-256 hash-chained JSONL audit log. The runtime exposes tools to read, verify, and explicitly repair a broken chain while preserving a backup of the original content.

## Distribution integrity

Release builds contain `MANIFEST.sha256.json`. `gpt_agent_self_check` verifies listed runtime files against their expected size and SHA-256 values.
