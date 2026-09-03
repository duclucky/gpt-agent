param(
  [string]$InstallRoot = "C:\GPTAgent",
  [string]$TempRoot = ""
)
$ErrorActionPreference = "Stop"
$RuntimeRoot = Join-Path $InstallRoot "runtime"
$DataRoot = Join-Path $InstallRoot "data"
$LogRoot = Join-Path $InstallRoot "logs"
$TmpRoot = if ($TempRoot) { [IO.Path]::GetFullPath($TempRoot) } else { Join-Path $InstallRoot "tmp" }
$GoSdkRoot = Join-Path $InstallRoot "go-sdk\go"
$NativeToolsRoot = Join-Path ([IO.Path]::GetPathRoot($InstallRoot)) "GPTAgentTools"
New-Item -ItemType Directory -Force -Path $LogRoot,$TmpRoot | Out-Null
$env:TEMP = $TmpRoot
$env:TMP = $TmpRoot
$env:GPT_AGENT_HOME = $DataRoot
$env:PIP_CACHE_DIR = Join-Path $TmpRoot "pip-cache"
$env:GOPATH = Join-Path $TmpRoot "go"
$env:GOMODCACHE = Join-Path $env:GOPATH "pkg\mod"
$env:GOCACHE = Join-Path $TmpRoot "go-cache"
if (Test-Path (Join-Path $GoSdkRoot "bin\go.exe")) { $env:GOROOT = $GoSdkRoot }
New-Item -ItemType Directory -Force -Path $env:PIP_CACHE_DIR,$env:GOPATH,$env:GOMODCACHE,$env:GOCACHE | Out-Null
$clangd = Get-ChildItem -Path (Join-Path $InstallRoot "clangd") -Recurse -Filter "clangd.exe" -File -ErrorAction SilentlyContinue | Select-Object -First 1
$clangdBin = if ($clangd) { $clangd.DirectoryName } else { $null }
$extraPath = @(
  (Join-Path $NativeToolsRoot "bin"),
  (Join-Path $InstallRoot "bin"),
  (Join-Path $InstallRoot "go-sdk\go\bin"),
  $clangdBin,
  (Join-Path $InstallRoot "tooling-node\node_modules\.bin"),
  (Join-Path $RuntimeRoot ".venv-tools\Scripts"),
  (Join-Path $env:USERPROFILE ".cargo\bin")
) | Where-Object { $_ -and (Test-Path $_) }
if ($extraPath.Count -gt 0) { $env:PATH = (($extraPath -join ";") + ";" + $env:PATH) }
Set-Location $RuntimeRoot
$env:GPT_AGENT_APP_ROOT = $RuntimeRoot
$env:GPT_AGENT_LOG_PATH = Join-Path $LogRoot "runtime.log"
$CoreBin = Join-Path $InstallRoot "bin\gpt-agent.exe"
if (-not (Test-Path $CoreBin)) { throw "GPT Agent runtime binary is missing: $CoreBin" }
& $CoreBin
exit $LASTEXITCODE
