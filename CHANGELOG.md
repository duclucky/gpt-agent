# Changelog

All notable changes to GPT Agent are documented here.

## [Unreleased]

### Planned

- Ongoing setup UX, documentation, verification, and security hardening.

## [0.1.0] - 2026-09-05

### Added

- Initial Windows release of the GPT Agent local developer runtime.
- 83 MCP tools: 79 native tools plus 4 bounded asynchronous job tools.
- Repository/file search, Git inspection and checkpoints, command execution, LSP navigation, DAP debugging, SQLite inspection, local HTTP tooling, durable project memory, and audit tooling.
- SAFE command execution with network disabled by default and time-bounded FULL SHELL escalation.
- Windows Sandbox-backed isolated execution support where the Windows feature is available.
- OpenAI Tunnel integration and a local setup wizard for configuring the Tunnel ID and restricted runtime key.
- Windows installer and Scheduled Task based runtime/tunnel startup.
- Release builder with Go tests, vet, PowerShell syntax validation, public-hygiene checks, runtime smoke testing, SHA-256 manifest generation, ZIP packaging, and checksum output.
- GitHub Actions CI for pushes and pull requests.

### Security

- Loopback-only MCP runtime by default.
- Workspace boundaries and secret-like file blocking.
- ACL-restricted local storage for the tunnel runtime key.
- Explicit confirmation for destructive filesystem operations.
- SHA-256 hash-chained audit events.

[Unreleased]: https://github.com/duclucky/gpt-agent/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/duclucky/gpt-agent/releases/tag/v0.1.0
