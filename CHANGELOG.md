# Changelog

All notable changes to GPT Agent are documented here.

## [Unreleased]

### Changed

- Added a no-side-effect installer `-PreflightOnly` mode that reports prerequisites, Scheduled Task/port conflicts, and runtime-config preservation intent before install/update.
- Made installer reruns preserve existing `data/config.json` by default; `-ResetRuntimeConfig` is now required to regenerate runtime config from release defaults.
- Hardened update/reinstall runtime replacement by stopping the existing `GPT Agent Runtime` task, waiting for port `8765` to be released, and refusing to start over an unrelated listener.
- Added a Windows CI release-contract gate that builds the distributable, verifies ZIP/manifest integrity and required release contents, checks the self-learning schema, and exercises safe uninstaller preserve/purge behavior.
- Made release builds fail on unformatted Go sources instead of mutating source files with `gofmt -w` during packaging.
- Added a safe Windows uninstaller that removes runtime/tooling and Scheduled Tasks while preserving local data, config, and workspace by default.
- Updated GitHub Actions dependencies and `golang.org/x/sys` through verified dependency maintenance.
- Added weekly Dependabot update configuration for Go modules and GitHub Actions.
- Upgraded the embedded Learning Loop skill pack to `1.4.0` with explicit candidate review lifecycle and clearer adaptive-learning guidance.
- Added candidate resolution to `gpt_agent_learn`: reviewed evidence can be marked `promoted` or `dismissed` so resolved candidates do not keep resurfacing.

### Security

- Revalidate every HTTP redirect against the configured host allowlist and keep setup/diagnostic health probes on HTTP(S) loopback addresses.
- Enforce runtime bounds for internal search/context limits instead of relying only on tool schema validation.
- Validate background process IDs before filesystem use and derive process log paths from validated IDs rather than stored metadata.
- Canonically contain distribution-manifest entries under the application root before reading files.
- Harden automatic-learning task sanitization for Bearer authorization values and common token prefixes; regression tests verify raw tool arguments/results are not persisted to evolution state.

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
