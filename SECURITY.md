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
- OpenAI tunnel credentials are stored outside the repository in an ACL-restricted file;
- the local setup wizard binds only to `127.0.0.1`, uses a random port and one-time token, rejects cross-origin requests, disables caching, and exits after setup/timeout.

## Runtime API key

Use a Restricted OpenAI runtime API key with only the permissions needed by `tunnel-client`, normally Tunnels Read + Use.

GPT Agent must never:

- commit the runtime API key;
- place it in `config.example.json`;
- place the literal value in the tunnel YAML profile;
- print it to normal logs or diagnostic output;
- place it on a Scheduled Task command line.

The Windows setup wizard stores the key at `C:\GPTAgent\config\secrets\control-plane-api-key.txt`, removes inherited ACLs, and grants only the current Windows user and SYSTEM. The tunnel profile references it through `file:`.

## Workspace guidance

Use the narrowest practical workspace. Avoid exposing `C:\`, `D:\`, a home directory, cloud-sync root, password-manager directory, or other broad data roots unless you intentionally want ChatGPT to access everything underneath.

## FULL SHELL

FULL SHELL can execute arbitrary commands within the configured workspace context and should be enabled only when needed. The community defaults should remain time bounded. Do not distribute a build with a permanent FULL SHELL grant pre-enabled.

## Untrusted code

Windows Sandbox can improve containment for untrusted commands, but GPT Agent is not a malware-analysis platform. Do not execute deliberately malicious code or unknown binaries on a valuable host merely because Sandbox support exists.

## Reporting a vulnerability

Do not include real API keys, tokens, credentials, private repository contents, or user data in a public issue. For a private repository, report security problems privately to the repository owner. Before making the repository public, configure a dedicated private security contact or GitHub private vulnerability reporting.

## Release hygiene

Before every release, run `scripts/windows/Build-Release.ps1`. The release builder fails if it detects known legacy integration identifiers, obvious private-machine paths, common secret-key patterns, broken PowerShell syntax, failing Go tests/vet, or unexpected runtime source residue.
