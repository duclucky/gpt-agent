param(
  [string]$Version = "0.1.0"
)

$ErrorActionPreference = "Stop"
$SourceRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$TempRoot = Join-Path $env:TEMP ("gpt-agent-release-contract-" + [guid]::NewGuid().ToString("N"))
$OutputRoot = Join-Path $TempRoot "release"
$ExtractRoot = Join-Path $TempRoot "extracted"
$FakeInstallRoot = Join-Path $TempRoot "fake-install"

function Fail([string]$Text) { throw $Text }

try {
  New-Item -ItemType Directory -Force -Path $OutputRoot | Out-Null

  & (Join-Path $PSScriptRoot "Build-Release.ps1") -Version $Version -OutputDir $OutputRoot
  if ($LASTEXITCODE -ne 0) { Fail "Build-Release.ps1 failed with exit code $LASTEXITCODE" }

  $stageName = "gpt-agent-v$Version-windows-amd64"
  $zipPath = Join-Path $OutputRoot ($stageName + ".zip")
  $shaPath = $zipPath + ".sha256"
  if (-not (Test-Path -LiteralPath $zipPath -PathType Leaf)) { Fail "Release ZIP missing: $zipPath" }
  if (-not (Test-Path -LiteralPath $shaPath -PathType Leaf)) { Fail "Release checksum missing: $shaPath" }

  $expectedZipHash = ((Get-Content -Raw -LiteralPath $shaPath).Trim() -split '\s+')[0].ToLowerInvariant()
  $actualZipHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $zipPath).Hash.ToLowerInvariant()
  if ($actualZipHash -ne $expectedZipHash) {
    Fail "Release ZIP checksum mismatch: expected $expectedZipHash actual $actualZipHash"
  }

  Expand-Archive -LiteralPath $zipPath -DestinationPath $ExtractRoot -Force

  foreach ($required in @(
    "gpt-agent.exe",
    "gpt-agent-setup.exe",
    "MANIFEST.sha256.json",
    "scripts\windows\Install-GPTAgent.ps1",
    "scripts\windows\Uninstall-GPTAgent.ps1",
    "docs\SELF-LEARNING.md",
    "go-core\tool_catalog.json"
  )) {
    $path = Join-Path $ExtractRoot $required
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { Fail "Required release file missing: $required" }
  }

  $manifest = Get-Content -Raw -LiteralPath (Join-Path $ExtractRoot "MANIFEST.sha256.json") | ConvertFrom-Json
  if ($manifest.version -ne $Version) { Fail "Manifest version is $($manifest.version), expected $Version" }
  foreach ($entry in $manifest.files.PSObject.Properties) {
    $relative = $entry.Name
    $meta = $entry.Value
    $path = Join-Path $ExtractRoot ($relative -replace '/', '\')
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { Fail "Manifest file missing: $relative" }
    $file = Get-Item -LiteralPath $path
    if ([int64]$meta.bytes -ne $file.Length) { Fail "Manifest size mismatch: $relative" }
    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $path).Hash.ToLowerInvariant()
    if ($hash -ne [string]$meta.sha256) { Fail "Manifest SHA256 mismatch: $relative" }
  }

  $catalog = Get-Content -Raw -LiteralPath (Join-Path $ExtractRoot "go-core\tool_catalog.json") | ConvertFrom-Json
  if (@($catalog).Count -ne 83) { Fail "Tool catalog count is $(@($catalog).Count), expected 83" }
  $learn = @($catalog) | Where-Object { $_.name -eq "gpt_agent_learn" } | Select-Object -First 1
  if (-not $learn) { Fail "gpt_agent_learn missing from release tool catalog" }
  if (-not $learn.inputSchema.properties.candidateIds -or -not $learn.inputSchema.properties.dismissCandidateIds) {
    Fail "gpt_agent_learn candidate lifecycle schema is missing"
  }

  foreach ($relative in @("bin","runtime","logs","tooling-node","tmp","clangd","go-sdk","data","config","workspace")) {
    $dir = Join-Path $FakeInstallRoot $relative
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    Set-Content -LiteralPath (Join-Path $dir "sentinel.txt") -Value $relative
  }
  $installer = Join-Path $ExtractRoot "scripts\windows\Install-GPTAgent.ps1"
  $preflightRoot = Join-Path $TempRoot "preflight-install"
  $preflightWorkspace = Join-Path $TempRoot "preflight-workspace"
  New-Item -ItemType Directory -Force -Path $preflightWorkspace | Out-Null
  & $installer -InstallRoot $preflightRoot -WorkspaceRoot $preflightWorkspace -PreflightOnly -SkipOptionalTooling -SkipTunnelDownload -SkipSetupWizard
  if ($LASTEXITCODE -ne 0) { Fail "Installer preflight failed with exit code $LASTEXITCODE" }
  if (Test-Path -LiteralPath $preflightRoot) { Fail "Installer preflight created InstallRoot despite no-side-effect contract" }

  $uninstaller = Join-Path $ExtractRoot "scripts\windows\Uninstall-GPTAgent.ps1"
  $refused = $false
  try {
    & $uninstaller -InstallRoot $FakeInstallRoot -SkipScheduledTasks -KeepNativeTools
  } catch {
    $refused = $_.Exception.Message -match "Confirm UNINSTALL"
  }
  if (-not $refused) { Fail "Uninstaller did not refuse execution without explicit confirmation" }

  & $uninstaller -InstallRoot $FakeInstallRoot -SkipScheduledTasks -KeepNativeTools -Confirm UNINSTALL
  foreach ($relative in @("bin","runtime","logs","tooling-node","tmp","clangd","go-sdk")) {
    if (Test-Path -LiteralPath (Join-Path $FakeInstallRoot $relative)) { Fail "Runtime directory survived default uninstall: $relative" }
  }
  foreach ($relative in @("data","config","workspace")) {
    if (-not (Test-Path -LiteralPath (Join-Path $FakeInstallRoot "$relative\sentinel.txt"))) {
      Fail "Default uninstall did not preserve $relative"
    }
  }

  & $uninstaller -InstallRoot $FakeInstallRoot -SkipScheduledTasks -KeepNativeTools -PurgeLocalData -PurgeWorkspace -Confirm UNINSTALL
  if (Test-Path -LiteralPath $FakeInstallRoot) { Fail "Purge uninstall left fake install root" }

  Write-Host "Release contract passed: $stageName" -ForegroundColor Green
} finally {
  Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $TempRoot
}
