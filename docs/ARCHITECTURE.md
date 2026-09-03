# Architecture

GPT Agent v0.1.0 is a Windows-native, local-first MCP developer runtime for ChatGPT.

## Control plane

`gpt-agent.exe` is the only MCP runtime process. It binds to `127.0.0.1:8765`, serves `/mcp` and `/healthz`, owns the tool catalog, dispatches all native tools, manages async jobs, LSP sessions, debugger sessions, managed processes, learning state, and audit state.

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
  +-- process manager + Windows Sandbox
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

The Windows installer configures one `main` workspace from the user-supplied `-WorkspaceRoot` value. Additional workspaces can be added manually to the local config later.

## Command execution

GPT Agent has two execution classes:

- **SAFE**: executable + argv, no shell interpolation, workspace/project scoped, network off by default. On Windows the runtime can use Windows Sandbox for stronger isolation where available.
- **FULL SHELL**: arbitrary PowerShell/Bash command text, available only while a local time-bounded grant is active.

The default policy is to prefer specialized tools and SAFE execution.

## Local setup wizard

`gpt-agent-setup.exe` is a separate one-time configuration helper. It:

- listens only on IPv4 loopback;
- uses a random port;
- requires a one-time random token on every local request;
- rejects cross-origin requests;
- sets no-store/referrer/CSP protections;
- never stores the runtime API key in source code or the tunnel profile;
- shuts down when setup is finished or after a timeout.

The wizard writes the OpenAI tunnel runtime key to an ACL-restricted local secret file, then asks `tunnel-client` to reference it by `file:`.

## Persistent data

The default installation separates immutable-ish runtime files from mutable data:

```text
C:\GPTAgent\runtime\   copied release/runtime source
C:\GPTAgent\bin\       gpt-agent.exe, gpt-agent-setup.exe, helper binaries
C:\GPTAgent\data\      config, logs/state pointers, learning state, artifacts
C:\GPTAgent\config\    tunnel profile and local secrets
C:\GPTAgent\logs\      runtime/tunnel logs
C:\GPTAgent\workspace\ default workspace if the user does not choose another
```

## Learning

The learning layer stores curated project-scoped facts, decisions, lessons, and reusable procedural `SKILL.md` content. Automatic learning is bounded and is not intended to persist credentials, transient logs, guesses, or private chain-of-thought.

## Audit

Security-sensitive operations are written to a SHA-256 hash-chained JSONL audit log. The runtime exposes tools to read, verify, and explicitly repair a broken chain while preserving a backup of the original content.

## Distribution integrity

Release builds contain `MANIFEST.sha256.json`. `gpt_agent_self_check` verifies listed runtime files against their expected size and SHA-256 values.
