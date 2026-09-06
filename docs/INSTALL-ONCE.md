# Install Once on Windows

## 1. Extract the release

Extract `gpt-agent-v0.1.0-windows-amd64.zip` to a temporary directory.

## 2. Choose a workspace

Pick the directory that contains the repositories you actually want ChatGPT to access, for example:

```text
D:\Projects
```

Avoid selecting an entire drive unless that is explicitly intended.

## 3. Run the installer

Open PowerShell in the extracted release directory:

```powershell
Set-ExecutionPolicy -Scope Process Bypass
.\scripts\windows\Install-GPTAgent.ps1 -WorkspaceRoot "D:\Projects"
```

The installer copies the runtime to `C:\GPTAgent`, creates local data/config directories, installs required developer tooling, starts the loopback MCP runtime, downloads the official OpenAI `tunnel-client`, verifies its published checksum, and opens the local connection wizard.

For unattended installation:

```powershell
.\scripts\windows\Install-GPTAgent.ps1 -WorkspaceRoot "D:\Projects" -SkipSetupWizard
```

Then run the wizard later:

```powershell
C:\GPTAgent\runtime\scripts\windows\Configure-OpenAITunnel.ps1
```

## 4. Complete the browser wizard

The local wizard guides you through:

1. creating/selecting an OpenAI Tunnel;
2. creating a Restricted runtime API key with Tunnels Read + Use;
3. saving the key locally with restricted ACLs;
4. validating and starting the tunnel;
5. opening ChatGPT Connectors to select the Tunnel connection.

See [CONNECT-CHATGPT.md](CONNECT-CHATGPT.md).

## 5. Verify local health

```powershell
Invoke-RestMethod http://127.0.0.1:8765/healthz
```

Expected fields include:

```text
ok: true
name: GPT Agent
version: 0.1.0
```

Check tasks:

```powershell
Get-ScheduledTask "GPT Agent Runtime","GPT Agent Tunnel" | Select TaskName,State
```

The tunnel task does not exist until the setup wizard validates a tunnel profile.

## Reconfigure the workspace

Edit:

```text
C:\GPTAgent\data\config.json
```

Then restart `GPT Agent Runtime`. Keep workspace roots as narrow as practical.

## Update

For v0.1.0, update by downloading a newer release and rerunning its installer. Mutable data and tunnel secrets live outside the copied runtime tree under `C:\GPTAgent\data` and `C:\GPTAgent\config`.

## Uninstall

Use the bundled uninstaller from the installed runtime or from an extracted release:

```powershell
C:\GPTAgent\runtime\scripts\windows\Uninstall-GPTAgent.ps1 -Confirm UNINSTALL
```

By default it removes the runtime, tooling, logs, and the `GPT Agent Runtime` / `GPT Agent Tunnel` Scheduled Tasks, while preserving:

- `C:\GPTAgent\data` (learning state and runtime data);
- `C:\GPTAgent\config` (including local tunnel configuration/secrets);
- `C:\GPTAgent\workspace`.

To also remove local data/config:

```powershell
C:\GPTAgent\runtime\scripts\windows\Uninstall-GPTAgent.ps1 -PurgeLocalData -Confirm UNINSTALL
```

Only add `-PurgeWorkspace` if you explicitly want the default workspace directory deleted. Review that directory first because it may contain project files.
