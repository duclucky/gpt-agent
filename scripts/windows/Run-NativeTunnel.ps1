param(
  [string]$InstallRoot = "C:\GPTAgent",
  [string]$TempRoot = ""
)

$ErrorActionPreference = "Stop"
$Bin = Join-Path $InstallRoot "bin\tunnel-client.exe"
$ProfileDir = Join-Path $InstallRoot "config\tunnel-client"
$DataRoot = Join-Path $InstallRoot "data"
$LogRoot = Join-Path $InstallRoot "logs"
$TmpRoot = if ($TempRoot) { [IO.Path]::GetFullPath($TempRoot) } else { Join-Path $InstallRoot "tmp" }
$HealthUrlFile = Join-Path $DataRoot "tunnel-health.url"
New-Item -ItemType Directory -Force -Path $DataRoot,$LogRoot,$TmpRoot | Out-Null
Remove-Item -Force -ErrorAction SilentlyContinue $HealthUrlFile
$env:TEMP = $TmpRoot
$env:TMP = $TmpRoot
if (-not (Test-Path $Bin)) { throw "tunnel-client.exe is missing: $Bin" }
if (-not (Test-Path (Join-Path $ProfileDir "gpt-agent.yaml"))) { throw "GPT Agent tunnel is not configured. Run Configure-OpenAITunnel.ps1 first." }

$deadline = (Get-Date).AddSeconds(90)
do {
  try {
    $health = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:8765/healthz" -TimeoutSec 2
    if ($health.StatusCode -eq 200) { break }
  } catch {}
  Start-Sleep -Seconds 1
} while ((Get-Date) -lt $deadline)
if ((Get-Date) -ge $deadline) { throw "GPT Agent runtime did not become healthy before tunnel startup." }

& $Bin run --profile gpt-agent --profile-dir $ProfileDir `
  --health.url-file $HealthUrlFile `
  --log.file (Join-Path $LogRoot "tunnel.log")
exit $LASTEXITCODE
