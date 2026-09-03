param(
  [string]$InstallRoot = "C:\GPTAgent"
)

$ErrorActionPreference = "Stop"
$SetupBin = Join-Path $InstallRoot "bin\gpt-agent-setup.exe"
if (-not (Test-Path $SetupBin)) {
  throw "gpt-agent-setup.exe is missing. Reinstall GPT Agent or build go-core/cmd/setup."
}

# The wizard needs the local MCP runtime to be reachable before validating the tunnel.
$healthy = $false
try {
  $r = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:8765/healthz" -TimeoutSec 2
  $healthy = $r.StatusCode -eq 200
} catch {}
if (-not $healthy) {
  & schtasks.exe /Run /TN "GPT Agent Runtime" | Out-Null
  $deadline = (Get-Date).AddSeconds(60)
  do {
    try {
      $r = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:8765/healthz" -TimeoutSec 2
      if ($r.StatusCode -eq 200) { $healthy = $true; break }
    } catch {}
    Start-Sleep -Milliseconds 500
  } while ((Get-Date) -lt $deadline)
}
if (-not $healthy) { throw "GPT Agent runtime is not healthy on 127.0.0.1:8765." }

& $SetupBin --install-root $InstallRoot
exit $LASTEXITCODE
