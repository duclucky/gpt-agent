# Contributing to GPT Agent

Thanks for helping improve GPT Agent. The project is Windows-first and aims to keep local engineering access explicit, auditable, and safe by default.

## Before you start

- Search existing issues before opening a duplicate.
- For security vulnerabilities, do **not** open a public issue. Use GitHub Private Vulnerability Reporting instead: https://github.com/duclucky/gpt-agent/security/advisories/new
- Keep changes focused. Avoid unrelated cleanup in the same pull request.
- Never include API keys, tokens, credentials, private repository contents, or machine-specific secrets in issues, commits, fixtures, screenshots, or logs.

## Development setup

The Go runtime lives in `go-core` and currently targets Go 1.26.

From `go-core`:

```powershell
go test ./...
go vet ./...
```

For formatting, use the standard Go formatter before submitting changes:

```powershell
go fmt ./...
```

PowerShell scripts under `scripts/windows` must parse cleanly. CI runs Go tests, `go vet`, and PowerShell syntax validation on Windows.

For release-affecting changes, run the repository release builder from the repository root when practical:

```powershell
.\scripts\windows\Build-Release.ps1
```

The release builder performs formatting, tests, vet, PowerShell parsing, public-hygiene checks, builds the Windows binaries, smoke-tests the runtime, and generates the distributable archive and manifest.

## Making a change

1. Inspect the relevant implementation, tests, callers, and configuration before editing.
2. Prefer the smallest maintainable change that fixes the root cause or implements the requested behavior.
3. Preserve backwards compatibility unless a breaking change is intentional and documented.
4. Add or update tests when behavior changes.
5. Run the relevant checks locally.
6. Review your final diff for accidental edits, secrets, generated artifacts, and private-machine data.

## Pull requests

A good pull request should explain:

- what problem it solves;
- what changed;
- why the chosen approach fits the existing architecture;
- what tests/checks were run and their results;
- any security, compatibility, migration, or operational impact;
- screenshots for visible UI changes when useful.

Keep generated release files, local runtime state, secrets, caches, and machine-specific configuration out of commits.

## Scope and design

GPT Agent intentionally keeps ChatGPT as the reasoning/agent loop and provides a local execution layer. Changes that add a second embedded model, external coding harness, or model-provider gateway are outside the current public product scope.

For broader product direction, see [ROADMAP.md](ROADMAP.md).

## Code of conduct

Be respectful, technical, and constructive. Focus discussion on reproducible behavior, evidence, and maintainable solutions.
