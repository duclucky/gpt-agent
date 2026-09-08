# Security Policy

GPT Agent gives ChatGPT controlled local developer capabilities. Treat it like a powerful developer tool, not like a harmless chat extension.

## Security model

The default design aims to reduce accidental overreach:

- the MCP server binds only to loopback;
- users explicitly choose workspace roots;
- secret-like files are blocked by default;
- SAFE command execution avoids shell interpolation and disables network by default;
- FULL SHELL is gated by a local time-bounded grant;
- destructive tools require explicit confirmation tokens;
- audit events are SHA-256 hash chained;
- OpenAI tunnel credentials are stored outside the repository in a permissions-restricted file (Windows ACL or macOS mode `0600`);
- the local setup wizard binds only to `127.0.0.1`, uses a random port and one-time token, rejects cross-origin requests, disables caching, and exits after setup/timeout.

## Runtime API key

Use a Restricted OpenAI runtime API key with only the permissions needed by `tunnel-client`, normally Tunnels Read + Use.

GPT Agent must never:

- commit the runtime API key;
- place it in `config.example.json`;
- place the literal value in the tunnel YAML profile;
- print it to normal logs or diagnostic output;
- place it on a Scheduled Task command line.

On Windows the setup wizard stores the key at `C:\GPTAgent\config\secrets\control-plane-api-key.txt`, removes inherited ACLs, and grants only the current Windows user and SYSTEM. On macOS it stores the key under `~/.local/share/gpt-agent/config/secrets/` with mode `0600`. In both cases the tunnel profile references it through `file:`.

## Workspace guidance

Use the narrowest practical workspace. Avoid exposing `C:\`, `D:\`, a home directory, cloud-sync root, password-manager directory, or other broad data roots unless you intentionally want ChatGPT to access everything underneath.

## FULL SHELL

FULL SHELL can execute arbitrary commands within the configured workspace context and should be enabled only when needed. The community defaults should remain time bounded. Do not distribute a build with a permanent FULL SHELL grant pre-enabled.

## Untrusted code

Windows Sandbox can improve containment for untrusted commands, but GPT Agent is not a malware-analysis platform. The macOS MVP does not claim an equivalent isolation backend, so `gpt_agent_untrusted_run` reports unavailable there instead of falling back to weaker execution. Do not execute deliberately malicious code or unknown binaries on a valuable host merely because an isolation backend exists.

## Reporting a vulnerability

Report security vulnerabilities privately through **GitHub Private Vulnerability Reporting**:

https://github.com/duclucky/gpt-agent/security/advisories/new

Do **not** open a public issue for a suspected vulnerability.

When reporting a vulnerability:

- describe the affected version and component;
- provide the smallest safe reproduction you can;
- explain the expected security boundary and the observed behavior;
- redact API keys, tokens, credentials, private repository contents, usernames, private paths, and other sensitive machine data;
- never upload real secrets merely to prove that a leak is possible.

For ordinary bugs and feature requests that are not security-sensitive, use the repository issue forms.

## Release hygiene

Before every release, run the platform release contract: `scripts/windows/Test-ReleaseContract.ps1` on Windows or `scripts/macos/test-release-contract.sh` on macOS. These gates build the distributable, run tests/vet and script syntax checks, verify manifest/archive integrity and required contents, and apply public-hygiene checks. Private maintainers should also provide the external private denylist used for public-release audits.
