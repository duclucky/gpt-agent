# Connect GPT Agent to ChatGPT

GPT Agent uses the official OpenAI Tunnel client to expose the local MCP endpoint to ChatGPT without opening a public inbound port on your machine.

## What you need

You need two separate values:

- **Tunnel ID**: an identifier such as `tunnel_...` from OpenAI Platform Tunnels.
- **Runtime API key**: a Restricted API key with **Tunnels Read + Use** permission.

The runtime API key is not the Tunnel ID. The key is used by `tunnel-client` to authenticate to the OpenAI tunnel control plane.

## Recommended setup

After installing GPT Agent, the installer automatically launches the local setup wizard. If you skipped it, run:

```powershell
C:\GPTAgent\runtime\scripts\windows\Configure-OpenAITunnel.ps1
```

The wizard runs only on `127.0.0.1` using a random port and a one-time random URL token.

### Step 1: Create or select a Tunnel

Use the **Open Platform Tunnels** button in the wizard. Create/select the tunnel associated with the ChatGPT workspace where you want to use GPT Agent, then copy the `tunnel_...` ID.

### Step 2: Create a Restricted runtime API key

Use the **Open Runtime API Keys** button. Create a Restricted runtime key with:

- Tunnels: **Read**
- Tunnels: **Use**

Do not use a long-lived Admin API key for the tunnel daemon. If you create the tunnel in the Platform UI, an Admin key is not required by GPT Agent.

### Step 3: Save and validate

Paste the Tunnel ID and runtime API key into the local wizard and click **Save and connect**.

The wizard will:

1. store the runtime key in `C:\GPTAgent\config\secrets\control-plane-api-key.txt`;
2. remove inherited ACLs and restrict the file to the current Windows user plus SYSTEM;
3. generate a `tunnel-client` profile that references the secret via `file:` instead of containing the key;
4. run `tunnel-client doctor --explain`;
5. register and start the `GPT Agent Tunnel` Scheduled Task;
6. wait for the tunnel client's local `/readyz` endpoint to become healthy.

The wizard never writes the runtime key into this repository or a committed config file.

### Step 4: Add the Tunnel in ChatGPT

When the wizard reports success, click **Open ChatGPT Connectors**.

In ChatGPT:

1. Open **Settings**.
2. Open **Connectors**.
3. Choose **Connection: Tunnel**.
4. Select your Tunnel, or paste the same Tunnel ID if the UI requests it.
5. Enable/connect it for the ChatGPT workspace you intend to use.

Once connected, ChatGPT can discover the GPT Agent MCP tools through the tunnel.

## Troubleshooting

Check the local runtime:

```powershell
Invoke-RestMethod http://127.0.0.1:8765/healthz
```

Check Scheduled Tasks:

```powershell
Get-ScheduledTask "GPT Agent Runtime","GPT Agent Tunnel" | Select TaskName,State
```

Rerun the setup wizard:

```powershell
C:\GPTAgent\runtime\scripts\windows\Configure-OpenAITunnel.ps1
```

Tunnel logs are stored at:

```text
C:\GPTAgent\logs\tunnel.log
```

The tunnel client's dynamically selected local health URL is stored at:

```text
C:\GPTAgent\data\tunnel-health.url
```

If `doctor` reports a permission error, confirm the runtime key is Restricted and has Tunnels Read + Use, and confirm the selected Tunnel belongs to the expected organization/workspace scope.
