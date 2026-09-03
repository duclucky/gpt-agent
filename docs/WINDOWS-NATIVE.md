# Windows Native Runtime

GPT Agent v0.1.0 targets Windows x64.

## Default layout

```text
C:\GPTAgent\
  bin\
    gpt-agent.exe
    gpt-agent-setup.exe
    tunnel-client.exe
    gopls.exe                 optional
  config\
    tunnel-client\
      gpt-agent.yaml
    secrets\
      control-plane-api-key.txt
  data\
    config.json
    tunnel-health.url
    learning\
    processes\
    artifacts\
  logs\
    runtime.log
    tunnel.log
  runtime\
    source/runtime distribution + MANIFEST.sha256.json
  tooling-node\
  tmp\
  workspace\                   default if another workspace is not chosen
```

## Scheduled Tasks

The installer creates:

- `GPT Agent Runtime`: starts the loopback MCP runtime at logon.
- `GPT Agent Tunnel`: created only after the tunnel wizard validates the profile; starts the OpenAI tunnel client at logon.

Check them with:

```powershell
Get-ScheduledTask "GPT Agent Runtime","GPT Agent Tunnel" | Select TaskName,State
```

## Runtime endpoint

The MCP runtime binds only to:

```text
http://127.0.0.1:8765/mcp
```

Health endpoint:

```text
http://127.0.0.1:8765/healthz
```

The runtime refuses non-loopback server configuration.

## Workspace selection

Choose the narrowest directory that contains the projects you want ChatGPT to modify:

```powershell
.\scripts\windows\Install-GPTAgent.ps1 -WorkspaceRoot "D:\Projects"
```

The installer does not automatically expose an entire drive or the GPT Agent source repository.

## Tooling

The installer ensures Node.js LTS and Git, then installs an isolated set of language-server packages. Optional CLI tools are installed through winget when available. Verified portable packages are used for selected tools where the installer pins a known release and SHA-256.

The generated runtime config only advertises language servers that were successfully installed or already available.

## Windows Sandbox

GPT Agent can use Windows Sandbox for isolated command execution where the feature is available. The runtime still treats sandbox execution as a security boundary with limits; it is not a substitute for a dedicated malware-analysis environment.

## Runtime key storage

The OpenAI tunnel runtime API key is stored outside the source tree at:

```text
C:\GPTAgent\config\secrets\control-plane-api-key.txt
```

The setup wizard removes inherited ACLs and grants access only to the current Windows user and SYSTEM. The `tunnel-client` YAML profile stores only a `file:` reference to this path.

## Start and stop

```powershell
C:\GPTAgent\runtime\scripts\windows\Start-Native.ps1
C:\GPTAgent\runtime\scripts\windows\Stop-Native.ps1
```

To rerun tunnel configuration:

```powershell
C:\GPTAgent\runtime\scripts\windows\Configure-OpenAITunnel.ps1
```

## Logs

```text
C:\GPTAgent\logs\runtime.log
C:\GPTAgent\logs\tunnel.log
```

Do not paste full logs publicly without reviewing them for project paths or other environment-specific information.
