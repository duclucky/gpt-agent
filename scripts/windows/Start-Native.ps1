param([string]$InstallRoot = "C:\GPTAgent")
$ErrorActionPreference = "Stop"
& schtasks.exe /Run /TN "GPT Agent Runtime" | Out-Null
$deadline = (Get-Date).AddSeconds(90)
do {
  try {
    $r = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:8765/healthz" -TimeoutSec 2
    if ($r.StatusCode -eq 200) { break }
  } catch {}
  Start-Sleep -Seconds 1
} while ((Get-Date) -lt $deadline)
if ((Get-Date) -ge $deadline) { throw "GPT Agent runtime did not become healthy." }
$task = Get-ScheduledTask -TaskName "GPT Agent Tunnel" -ErrorAction SilentlyContinue
if ($task) { & schtasks.exe /Run /TN "GPT Agent Tunnel" | Out-Null }
Write-Host "GPT Agent runtime started." -ForegroundColor Green
if (-not $task) { Write-Host "Tunnel is not configured yet. Run Configure-OpenAITunnel.ps1 when ready." -ForegroundColor Yellow }
