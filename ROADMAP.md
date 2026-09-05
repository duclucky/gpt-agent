# GPT Agent Roadmap

This roadmap describes current priorities, not guaranteed dates or release commitments. Priorities may change based on bugs, security findings, contributor feedback, and platform changes.

## Current priorities

### Setup and onboarding

- Continue improving the local setup wizard for clarity, accessibility, and recovery from configuration errors.
- Keep installation and ChatGPT connection steps short, explicit, and safe for non-expert users.
- Improve diagnostics around Tunnel, runtime health, workspace configuration, and required tooling.

### Reliability and verification

- Expand regression coverage for filesystem, Git, process, async-job, LSP, debugger, and runtime edge cases.
- Strengthen deterministic verification and recovery paths for long-running local operations.
- Keep release packaging reproducible and auditable.

### Security hardening

- Continue tightening workspace boundaries, secret detection, shell escalation, and audit behavior.
- Expand public-release hygiene checks without weakening normal developer workflows.
- Keep credentials outside repositories, committed configuration, and normal logs.

### Open-source usability

- Improve contributor documentation and issue/PR workflows.
- Keep the tool catalog and architecture documentation synchronized with shipped behavior.
- Make installation, upgrade, troubleshooting, and release verification easier to follow.

## Future exploration

- Better runtime observability and self-diagnostics.
- Deeper LSP/DAP quality and language/toolchain coverage where it can be distributed reliably.
- More robust isolated execution workflows using Windows Sandbox.
- Browser automation only when it can ship as a complete, independently distributable runtime with clear security boundaries.

## Non-goals for the current public product

GPT Agent is a local execution layer for ChatGPT. The public project does not aim to embed a second reasoning model, external coding harness, or model-provider gateway.

## Contributing to the roadmap

Open a feature request describing the user problem, expected workflow, security implications, and why the change belongs in the public GPT Agent runtime. See [CONTRIBUTING.md](CONTRIBUTING.md).
