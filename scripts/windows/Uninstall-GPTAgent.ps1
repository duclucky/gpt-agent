param(
  [string]$InstallRoot = "C:\GPTAgent",
  [switch]$PurgeLocalData,
  [switch]$PurgeWorkspace,
  [switch]$KeepNativeTools,
  [switch]$SkipScheduledTasks,
  [ValidateSet("UNINSTALL")]
  [string]$Confirm = ""
)

$ErrorActionPreference = "Stop"

if ($Confirm -ne "UNINSTALL") {
  throw "Refusing to uninstall without -Confirm UNINSTALL."
}

$InstallRoot = [IO.Path]::GetFullPath($InstallRoot)
if ([string]::IsNullOrWhiteSpace($InstallRoot) -or $InstallRoot -eq [IO.Path]::GetPathRoot($InstallRoot)) {
  throw "Refusing unsafe InstallRoot: $InstallRoot"
}

function Say([string]$Text) { Write-Host $Text -ForegroundColor Cyan }
function Warn([string]$Text) { Write-Host $Text -ForegroundColor Yellow }

if (-not $SkipScheduledTasks) {
  $schtasks = Join-Path $env:SystemRoot "System32\schtasks.exe"
  if (-not (Test-Path -LiteralPath $schtasks)) { throw "schtasks.exe is unavailable." }
  foreach ($name in @("GPT Agent Tunnel", "GPT Agent Runtime")) {
    & $schtasks /End /TN $name 2>$null | Out-Null
    & $schtasks /Delete /TN $name /F 2>$null | Out-Null
  }
} else {
  Warn "Scheduled Task removal skipped."
}

$runtimePaths = @(
  "bin",
  "runtime",
  "logs",
  "tooling-node",
  "tmp",
  "clangd",
  "go-sdk"
)
foreach ($relative in $runtimePaths) {
  $path = Join-Path $InstallRoot $relative
  if (Test-Path -LiteralPath $path) {
    Say "Removing $path"
    Remove-Item -LiteralPath $path -Recurse -Force
  }
}

$nativeToolsRoot = Join-Path ([IO.Path]::GetPathRoot($InstallRoot)) "GPTAgentTools"
if (-not $KeepNativeTools -and (Test-Path -LiteralPath $nativeToolsRoot)) {
  Say "Removing $nativeToolsRoot"
  Remove-Item -LiteralPath $nativeToolsRoot -Recurse -Force
} elseif ($KeepNativeTools -and (Test-Path -LiteralPath $nativeToolsRoot)) {
  Warn "Preserved: $nativeToolsRoot"
}

if ($PurgeLocalData) {
  foreach ($relative in @("data", "config")) {
    $path = Join-Path $InstallRoot $relative
    if (Test-Path -LiteralPath $path) {
      Say "Purging $path"
      Remove-Item -LiteralPath $path -Recurse -Force
    }
  }
} else {
  foreach ($relative in @("data", "config")) {
    $path = Join-Path $InstallRoot $relative
    if (Test-Path -LiteralPath $path) { Warn "Preserved: $path" }
  }
}

$workspacePath = Join-Path $InstallRoot "workspace"
if ($PurgeWorkspace) {
  if (Test-Path -LiteralPath $workspacePath) {
    Say "Purging $workspacePath"
    Remove-Item -LiteralPath $workspacePath -Recurse -Force
  }
} elseif (Test-Path -LiteralPath $workspacePath) {
  Warn "Preserved: $workspacePath"
}

if (Test-Path -LiteralPath $InstallRoot) {
  $remaining = @(Get-ChildItem -LiteralPath $InstallRoot -Force -ErrorAction SilentlyContinue)
  if ($remaining.Count -eq 0) {
    Remove-Item -LiteralPath $InstallRoot -Force
  } else {
    Warn "Uninstall completed with preserved files under $InstallRoot."
  }
}

Write-Host "GPT Agent uninstall completed." -ForegroundColor Green
