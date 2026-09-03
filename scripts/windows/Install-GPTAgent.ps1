param(
  [string]$InstallRoot = "C:\GPTAgent",
  [string]$WorkspaceRoot = "C:\GPTAgent\workspace",
  [string]$ScratchRoot = "",
  [string]$NativeToolsRoot = "",
  [switch]$SkipOptionalTooling,
  [switch]$SkipTunnelDownload,
  [switch]$SkipSetupWizard
)

$ErrorActionPreference = "Stop"
$SourceRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$RuntimeRoot = Join-Path $InstallRoot "runtime"
$DataRoot = Join-Path $InstallRoot "data"
$ConfigRoot = Join-Path $InstallRoot "config"
$BinRoot = Join-Path $InstallRoot "bin"
$LogRoot = Join-Path $InstallRoot "logs"
$NodeToolingRoot = Join-Path $InstallRoot "tooling-node"
$ScratchRoot = if ($ScratchRoot) { [IO.Path]::GetFullPath($ScratchRoot) } else { Join-Path $InstallRoot "tmp" }
$NativeToolsRoot = if ($NativeToolsRoot) { [IO.Path]::GetFullPath($NativeToolsRoot) } else { Join-Path ([IO.Path]::GetPathRoot($InstallRoot)) "GPTAgentTools" }
$ClangdRoot = Join-Path $InstallRoot "clangd"
$GoSdkRoot = Join-Path $InstallRoot "go-sdk"

function Say([string]$Text) { Write-Host $Text -ForegroundColor Cyan }
function Warn([string]$Text) { Write-Host $Text -ForegroundColor Yellow }
function Require-Command([string]$Name) {
  $cmd = Get-Command $Name -ErrorAction SilentlyContinue | Select-Object -First 1
  if (-not $cmd) { throw "Required command not found: $Name" }
  return $cmd.Source
}
function Invoke-RobocopyChecked([string]$From, [string]$To, [string[]]$RoboArgs) {
  & robocopy.exe $From $To @RoboArgs | Out-Host
  if ($LASTEXITCODE -gt 7) { throw "robocopy failed with exit code $LASTEXITCODE" }
}

function Refresh-ProcessPath {
  $machine = [Environment]::GetEnvironmentVariable("Path", "Machine")
  $user = [Environment]::GetEnvironmentVariable("Path", "User")
  $env:PATH = (($machine, $user) | Where-Object { $_ }) -join ";"
}
function Ensure-WingetCommand([string]$Command, [string]$PackageId) {
  $found = Get-Command $Command -ErrorAction SilentlyContinue | Select-Object -First 1
  if ($found) { return $found.Source }
  $winget = Get-Command winget.exe -ErrorAction SilentlyContinue | Select-Object -First 1
  if (-not $winget) { throw "$Command is required. Install $PackageId or install winget, then retry." }
  Say "Installing prerequisite: $PackageId"
  & $winget.Source install --id $PackageId --exact --silent --accept-package-agreements --accept-source-agreements --disable-interactivity
  if ($LASTEXITCODE -ne 0) { throw "Failed to install prerequisite $PackageId (exit $LASTEXITCODE)." }
  Refresh-ProcessPath
  $found = Get-Command $Command -ErrorAction SilentlyContinue | Select-Object -First 1
  if (-not $found) { throw "$Command was installed but is not visible in PATH. Open a new terminal and retry." }
  return $found.Source
}
$Node = Ensure-WingetCommand "node.exe" "OpenJS.NodeJS.LTS"
$Npm = Ensure-WingetCommand "npm.cmd" "OpenJS.NodeJS.LTS"
$Git = Ensure-WingetCommand "git.exe" "Git.Git"
$TaskPowerShell = (Get-Command pwsh.exe -ErrorAction SilentlyContinue | Select-Object -First 1).Source
if (-not $TaskPowerShell) { $TaskPowerShell = (Get-Command powershell.exe -ErrorAction Stop | Select-Object -First 1).Source }
Say "Installing GPT Agent Windows-native runtime"
Write-Host "Source:    $SourceRoot"
Write-Host "Runtime:   $RuntimeRoot"
Write-Host "Data:      $DataRoot"
Write-Host "Workspace: $WorkspaceRoot"

foreach ($dir in @(
  $InstallRoot,$RuntimeRoot,$DataRoot,$ConfigRoot,$BinRoot,$LogRoot,
  $WorkspaceRoot,$NodeToolingRoot,$ScratchRoot,$ClangdRoot,$GoSdkRoot,$NativeToolsRoot
)) {
  New-Item -ItemType Directory -Force -Path $dir | Out-Null
}

# Keep installers/build caches off the Windows system drive. This also makes
# native SAFE execution viable when the system TEMP drive is constrained.
$env:TEMP = $ScratchRoot
$env:TMP = $ScratchRoot
$env:npm_config_cache = Join-Path $ScratchRoot "npm-cache"
$env:PIP_CACHE_DIR = Join-Path $ScratchRoot "pip-cache"
New-Item -ItemType Directory -Force -Path $env:npm_config_cache,$env:PIP_CACHE_DIR | Out-Null

Say "Copying sanitized runtime distribution..."
Invoke-RobocopyChecked $SourceRoot $RuntimeRoot @(
  "/MIR","/R:2","/W:1","/NFL","/NDL","/NJH","/NJS","/NP",
  "/XD",".git","node_modules",".venv-tools","data",".gpt-agent","dist","release"
)
$runtimeDataRoot = Join-Path $RuntimeRoot "data"
Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $runtimeDataRoot
New-Item -ItemType Directory -Force -Path $runtimeDataRoot | Out-Null
$sourceDataKeep = Join-Path $SourceRoot "data\.gitkeep"
if (Test-Path $sourceDataKeep) { Copy-Item -Force -LiteralPath $sourceDataKeep -Destination (Join-Path $runtimeDataRoot ".gitkeep") }

Say "Installing isolated Windows Node language tooling..."
$toolingPackage = [ordered]@{
  name = "gpt-agent-windows-tooling"
  private = $true
  version = "1.0.0"
  dependencies = [ordered]@{
    "typescript" = "6.0.3"
    "typescript-language-server" = "5.3.0"
    "pyright" = "1.1.411"
    "bash-language-server" = "5.6.0"
    "vscode-langservers-extracted" = "4.10.0"
    "yaml-language-server" = "1.24.0"
  }
  overrides = [ordered]@{
    "editorconfig" = [ordered]@{
      "minimatch" = "10.2.6"
    }
  }
}
$toolingPackage | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $NodeToolingRoot "package.json") -Encoding UTF8
Push-Location $NodeToolingRoot
try {
  & $Npm install --package-lock-only --ignore-scripts
  if ($LASTEXITCODE -ne 0) { throw "npm tooling lockfile generation failed: $LASTEXITCODE" }
  & $Npm ci --ignore-scripts
  if ($LASTEXITCODE -ne 0) { throw "npm tooling ci failed: $LASTEXITCODE" }
  & $Npm audit --audit-level=high
  if ($LASTEXITCODE -ne 0) { throw "Node tooling npm audit found high/critical vulnerabilities." }
} finally { Pop-Location }

$PythonCmd = Get-Command python.exe -ErrorAction SilentlyContinue | Select-Object -First 1
if ($PythonCmd) {
  Say "Creating Windows Python tools venv..."
  $Venv = Join-Path $RuntimeRoot ".venv-tools"
  & $PythonCmd.Source -m venv $Venv
  if ($LASTEXITCODE -ne 0) { throw "python -m venv failed: $LASTEXITCODE" }
  $VenvPython = Join-Path $Venv "Scripts\python.exe"
  & $VenvPython -m pip install --disable-pip-version-check --upgrade pip wheel
  if ($LASTEXITCODE -ne 0) { throw "pip bootstrap failed: $LASTEXITCODE" }
  & $VenvPython -m pip install --disable-pip-version-check debugpy semgrep
  if ($LASTEXITCODE -ne 0) { throw "Python tooling install failed: $LASTEXITCODE" }
} else {
  Warn "python.exe not found; Python DAP/Semgrep will be unavailable."
}

if (-not $SkipOptionalTooling) {
  $Winget = Get-Command winget.exe -ErrorAction SilentlyContinue | Select-Object -First 1
  if ($Winget) {
    $packages = @(
      [pscustomobject]@{ id="BurntSushi.ripgrep.MSVC"; command="rg.exe" },
      [pscustomobject]@{ id="sharkdp.fd"; command="fd.exe" },
      [pscustomobject]@{ id="jqlang.jq"; command="jq.exe" },
      [pscustomobject]@{ id="SQLite.SQLite"; command="sqlite3.exe" },
      [pscustomobject]@{ id="koalaman.shellcheck"; command="shellcheck.exe" }
    )
    foreach ($package in $packages) {
      $existing = Get-Command $package.command -ErrorAction SilentlyContinue | Select-Object -First 1
      if ($existing) {
        Say "Windows tool already available: $($package.command)"
        continue
      }
      Say "Ensuring Windows tool: $($package.id)"
      & $Winget.Source install --id $package.id --exact --silent --accept-package-agreements --accept-source-agreements --disable-interactivity
      if ($LASTEXITCODE -ne 0) { Warn "Optional winget package failed/skipped: $($package.id) (exit $LASTEXITCODE)" }
    }
  } else {
    Warn "winget.exe not found; portable CLI package installation skipped."
  }

  if (-not (Test-Path (Join-Path $BinRoot "sg.exe"))) {
    Say "Installing verified ast-grep 0.45.1..."
    $astUrl = "https://github.com/ast-grep/ast-grep/releases/download/0.45.1/app-x86_64-pc-windows-msvc.zip"
    $astExpected = "b816b375df6a30f8e5d91d706dccae6127346e68970788cf8c2decee54bcaa31"
    $astTmp = Join-Path $ScratchRoot ("ast-grep-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $astTmp | Out-Null
    try {
      $astZip = Join-Path $astTmp "ast-grep.zip"
      Invoke-WebRequest -UseBasicParsing -Uri $astUrl -OutFile $astZip
      $astActual = (Get-FileHash -Algorithm SHA256 -LiteralPath $astZip).Hash.ToLowerInvariant()
      if ($astActual -ne $astExpected) { throw "ast-grep SHA256 mismatch" }
      Expand-Archive -LiteralPath $astZip -DestinationPath $astTmp -Force
      $astBins = Get-ChildItem -Path $astTmp -Recurse -File | Where-Object { $_.Name -in @("sg.exe","ast-grep.exe") }
      if (-not $astBins) { throw "ast-grep executable missing from verified archive" }
      foreach ($bin in $astBins) {
        Copy-Item -Force -LiteralPath $bin.FullName -Destination (Join-Path $BinRoot $bin.Name)
      }
    } finally {
      Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $astTmp
    }
  }

  $existingClangd = Get-ChildItem -Path $ClangdRoot -Recurse -Filter "clangd.exe" -File -ErrorAction SilentlyContinue | Select-Object -First 1
  if (-not $existingClangd) {
    Say "Installing verified standalone clangd 22.1.6..."
    $clangdRelease = Invoke-RestMethod -UseBasicParsing -Headers @{"User-Agent"="GPT-Agent-Windows-Installer"} -Uri "https://api.github.com/repos/clangd/clangd/releases/tags/22.1.6"
    $clangdAsset = $clangdRelease.assets | Where-Object { $_.name -eq "clangd-windows-22.1.6.zip" } | Select-Object -First 1
    if (-not $clangdAsset -or -not $clangdAsset.digest -or -not $clangdAsset.digest.StartsWith("sha256:")) {
      throw "clangd release asset or digest unavailable"
    }
    $clangdExpected = $clangdAsset.digest.Substring(7).ToLowerInvariant()
    $clangdTmp = Join-Path $ScratchRoot ("clangd-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $clangdTmp | Out-Null
    try {
      $clangdZip = Join-Path $clangdTmp $clangdAsset.name
      Invoke-WebRequest -UseBasicParsing -Uri $clangdAsset.browser_download_url -OutFile $clangdZip
      $clangdActual = (Get-FileHash -Algorithm SHA256 -LiteralPath $clangdZip).Hash.ToLowerInvariant()
      if ($clangdActual -ne $clangdExpected) { throw "clangd SHA256 mismatch" }
      Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $ClangdRoot
      New-Item -ItemType Directory -Force -Path $ClangdRoot | Out-Null
      Expand-Archive -LiteralPath $clangdZip -DestinationPath $ClangdRoot -Force
      $existingClangd = Get-ChildItem -Path $ClangdRoot -Recurse -Filter "clangd.exe" -File | Select-Object -First 1
      if (-not $existingClangd) { throw "clangd.exe missing from verified archive" }
    } finally {
      Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $clangdTmp
    }
  }

  $portableGo = Join-Path $GoSdkRoot "go\bin\go.exe"
  $portableGoVersion = if (Test-Path $portableGo) { (& $portableGo version 2>$null) } else { "" }
  if ($portableGoVersion -ne "go version go1.26.5 windows/amd64") {
    Say "Installing verified portable Go 1.26.5..."
    $goUrl = "https://go.dev/dl/go1.26.5.windows-amd64.zip"
    $goExpected = "97e6b2a833b6d89f9ff17d25419ac0a7e3b482a044e9ab18cdef834bd834fd38"
    $goTmp = Join-Path $ScratchRoot ("go-sdk-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $goTmp | Out-Null
    try {
      $goZip = Join-Path $goTmp "go.zip"
      Invoke-WebRequest -UseBasicParsing -Uri $goUrl -OutFile $goZip
      $goActual = (Get-FileHash -Algorithm SHA256 -LiteralPath $goZip).Hash.ToLowerInvariant()
      if ($goActual -ne $goExpected) { throw "Go SDK SHA256 mismatch" }
      Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $GoSdkRoot
      New-Item -ItemType Directory -Force -Path $GoSdkRoot | Out-Null
      Expand-Archive -LiteralPath $goZip -DestinationPath $GoSdkRoot -Force
      $portableGo = Join-Path $GoSdkRoot "go\bin\go.exe"
      if (-not (Test-Path $portableGo)) { throw "portable go.exe missing from verified archive" }
    } finally {
      Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $goTmp
    }
  }
}

# Refresh PATH after installers and prefer verified runtime-local tooling without
# dropping Windows system paths such as System32.
$nodeToolingBin = Join-Path $NodeToolingRoot "node_modules\.bin"
$portableGoBin = Join-Path $GoSdkRoot "go\bin"
$cargoBin = Join-Path $env:USERPROFILE ".cargo\bin"
$clangdFile = Get-ChildItem -Path $ClangdRoot -Recurse -Filter "clangd.exe" -File -ErrorAction SilentlyContinue | Select-Object -First 1
$clangdBin = if ($clangdFile) { $clangdFile.DirectoryName } else { $null }
$prependPath = @(
  (Join-Path $NativeToolsRoot "bin"),$BinRoot,$portableGoBin,$clangdBin,$nodeToolingBin,$cargoBin
) | Where-Object { $_ -and (Test-Path $_) }
if ($prependPath.Count -gt 0) {
  $env:PATH = (($prependPath -join ";") + ";" + $env:PATH)
}

$portableGo = Join-Path $GoSdkRoot "go\bin\go.exe"
$Go = if (Test-Path $portableGo) {
  [pscustomobject]@{ Source=$portableGo; Portable=$true }
} else {
  $cmd = Get-Command go.exe -ErrorAction SilentlyContinue | Select-Object -First 1
  if ($cmd) { [pscustomobject]@{ Source=$cmd.Source; Portable=$false } } else { $null }
}
if ($Go) {
  Say "Ensuring gopls v0.23.0..."
  $goWorkRoot = Join-Path $ScratchRoot "go"
  $goModCache = Join-Path $goWorkRoot "pkg\mod"
  $goBuildCache = Join-Path $ScratchRoot "go-cache"
  New-Item -ItemType Directory -Force -Path $goWorkRoot,$goModCache,$goBuildCache | Out-Null
  if ($Go.Portable) { $env:GOROOT = Join-Path $GoSdkRoot "go" }
  $env:GOPATH = $goWorkRoot
  $env:GOMODCACHE = $goModCache
  $env:GOCACHE = $goBuildCache
  $env:GOBIN = $BinRoot
  & $Go.Source install "golang.org/x/tools/gopls@v0.23.0"
  if ($LASTEXITCODE -ne 0) { Warn "gopls install failed; Go LSP will be removed from native config if no previous binary exists." }
}

$coreSource = Join-Path $RuntimeRoot "go-core"
$coreBinary = Join-Path $BinRoot "gpt-agent.exe"
$prebuiltCore = Join-Path $SourceRoot "gpt-agent.exe"
if (Test-Path $prebuiltCore) {
  Say "Installing prebuilt GPT Agent runtime..."
  Copy-Item -Force -LiteralPath $prebuiltCore -Destination $coreBinary
} else {
  if (-not $Go) { throw "No prebuilt gpt-agent.exe was found and Go is unavailable for a source build." }
  Say "Building GPT Agent Go runtime from source..."
  if (-not (Test-Path (Join-Path $coreSource "go.mod"))) { throw "Go source is missing: $coreSource" }
  Push-Location $coreSource
  try {
    $env:GOWORK = "off"
    & $Go.Source test ./...
    if ($LASTEXITCODE -ne 0) { throw "Go core tests failed: $LASTEXITCODE" }
    & $Go.Source build -trimpath -ldflags "-s -w" -o $coreBinary .
    if ($LASTEXITCODE -ne 0) { throw "Go core build failed: $LASTEXITCODE" }
  } finally { Pop-Location }
}
if (-not (Test-Path $coreBinary)) { throw "GPT Agent binary missing after installation: $coreBinary" }

$setupBinary = Join-Path $BinRoot "gpt-agent-setup.exe"
$prebuiltSetup = Join-Path $SourceRoot "gpt-agent-setup.exe"
if (Test-Path $prebuiltSetup) {
  Say "Installing prebuilt GPT Agent setup wizard..."
  Copy-Item -Force -LiteralPath $prebuiltSetup -Destination $setupBinary
} else {
  if (-not $Go) { throw "No prebuilt gpt-agent-setup.exe was found and Go is unavailable for a source build." }
  Say "Building GPT Agent local setup wizard from source..."
  Push-Location $coreSource
  try {
    $env:GOWORK = "off"
    & $Go.Source build -trimpath -ldflags "-s -w" -o $setupBinary .\cmd\setup
    if ($LASTEXITCODE -ne 0) { throw "GPT Agent setup wizard build failed: $LASTEXITCODE" }
  } finally { Pop-Location }
}
if (-not (Test-Path $setupBinary)) { throw "GPT Agent setup wizard binary missing after installation: $setupBinary" }

Say "Creating Windows-native config..."
$cfg = Get-Content -Raw -LiteralPath (Join-Path $RuntimeRoot "config.example.json") | ConvertFrom-Json
$cfg.security.requireLinuxFilesystem = $false
$cfg.security.processSandbox = $false
$cfg.security.requireProcessSandbox = $false
$cfg.security.safeNetworkDefault = $false
$cfg.security.requireProjectCwdForDriveWorkspace = $true
$cfg.security.windowsSandboxMemoryMb = 2048
$cfg.workspaces = @(
  [pscustomobject]@{ id="main"; root=$WorkspaceRoot; enabled=$true }
)

$nodeServerEntrypoints = @{
  "typescript" = "typescript-language-server\lib\cli.mjs"
  "javascript" = "typescript-language-server\lib\cli.mjs"
  "python" = "pyright\langserver.index.js"
  "bash" = "bash-language-server\out\cli.js"
  "json" = "vscode-langservers-extracted\bin\vscode-json-language-server"
  "yaml" = "yaml-language-server\bin\yaml-language-server"
}
foreach ($language in $nodeServerEntrypoints.Keys) {
  $entry = $cfg.lspServers.$language
  if (-not $entry) { continue }
  $serverScript = Join-Path (Join-Path $NodeToolingRoot "node_modules") $nodeServerEntrypoints[$language]
  if (-not (Test-Path $serverScript)) { throw "Node LSP entrypoint missing: $serverScript" }
  $entry.command = $Node
  $entry.args = @($serverScript) + @($entry.args)
}

$rustAnalyzer = Get-Command rust-analyzer.exe -ErrorAction SilentlyContinue | Select-Object -First 1
if ($rustAnalyzer) { $cfg.lspServers.rust.command = $rustAnalyzer.Source }

$goplsPath = Join-Path $BinRoot "gopls.exe"
if (Test-Path $goplsPath) {
  $cfg.lspServers.go.command = $goplsPath
} else {
  $cfg.lspServers.PSObject.Properties.Remove("go")
}

$clangdFile = Get-ChildItem -Path $ClangdRoot -Recurse -Filter "clangd.exe" -File -ErrorAction SilentlyContinue | Select-Object -First 1
if ($clangdFile) {
  $cfg.lspServers.c.command = $clangdFile.FullName
  $cfg.lspServers.cpp.command = $clangdFile.FullName
} else {
  $cfg.lspServers.PSObject.Properties.Remove("c")
  $cfg.lspServers.PSObject.Properties.Remove("cpp")
}

$ConfigPath = Join-Path $DataRoot "config.json"
$configJson = $cfg | ConvertTo-Json -Depth 20
[IO.File]::WriteAllText($ConfigPath, $configJson, (New-Object Text.UTF8Encoding($false)))
New-Item -ItemType Directory -Force -Path (Join-Path $DataRoot "processes"),(Join-Path $DataRoot "artifacts") | Out-Null

if (-not $SkipTunnelDownload) {
  Say "Installing latest official OpenAI tunnel-client for Windows amd64..."
  $release = Invoke-RestMethod -UseBasicParsing -Headers @{"User-Agent"="GPT-Agent-Windows-Installer"} -Uri "https://api.github.com/repos/openai/tunnel-client/releases/latest"
  $zipAsset = $release.assets | Where-Object { $_.name -match 'windows-amd64\.zip$' } | Select-Object -First 1
  $sumAsset = $release.assets | Where-Object { $_.name -eq 'SHA256SUMS.txt' } | Select-Object -First 1
  if (-not $zipAsset -or -not $sumAsset) { throw "Required tunnel-client release assets were not found." }
  $tmp = Join-Path $ScratchRoot ("gpt-agent-tunnel-" + [guid]::NewGuid().ToString("N"))
  New-Item -ItemType Directory -Force -Path $tmp | Out-Null
  try {
    $zip = Join-Path $tmp $zipAsset.name
    $sums = Join-Path $tmp "SHA256SUMS.txt"
    Invoke-WebRequest -UseBasicParsing -Uri $zipAsset.browser_download_url -OutFile $zip
    Invoke-WebRequest -UseBasicParsing -Uri $sumAsset.browser_download_url -OutFile $sums
    $expectedLine = Get-Content $sums | Where-Object { $_ -match [regex]::Escape($zipAsset.name) } | Select-Object -First 1
    if (-not $expectedLine) { throw "Checksum entry missing for $($zipAsset.name)" }
    $expected = ($expectedLine -split '\s+')[0].ToLowerInvariant()
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash.ToLowerInvariant()
    if ($actual -ne $expected) { throw "tunnel-client SHA256 mismatch" }
    Expand-Archive -LiteralPath $zip -DestinationPath $tmp -Force
    $exe = Get-ChildItem -Path $tmp -Filter "tunnel-client.exe" -Recurse | Select-Object -First 1
    if (-not $exe) { throw "tunnel-client.exe missing from verified archive" }
    Copy-Item -Force -LiteralPath $exe.FullName -Destination (Join-Path $BinRoot "tunnel-client.exe")
  } finally {
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $tmp
  }
}

Say "Registering GPT Agent runtime task..."
$runtimeRunner = Join-Path $RuntimeRoot "scripts\windows\Run-NativeRuntime.ps1"
$runtimeAction = "`"$TaskPowerShell`" -NoLogo -NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File `"$runtimeRunner`" -InstallRoot `"$InstallRoot`" -TempRoot `"$ScratchRoot`""
$SchTasks = Join-Path $env:SystemRoot "System32\schtasks.exe"
& $SchTasks /Create /TN "GPT Agent Runtime" /SC ONLOGON /TR $runtimeAction /F | Out-Null
if ($LASTEXITCODE -ne 0) { throw "Failed to register GPT Agent Runtime task." }

Say "Starting GPT Agent runtime..."
& $SchTasks /Run /TN "GPT Agent Runtime" | Out-Null
$deadline = (Get-Date).AddSeconds(90)
do {
  try {
    $health = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:8765/healthz" -TimeoutSec 2
    if ($health.StatusCode -eq 200) { break }
  } catch {}
  Start-Sleep -Seconds 1
} while ((Get-Date) -lt $deadline)
if ((Get-Date) -ge $deadline) { throw "GPT Agent did not become healthy after installation." }

Say "GPT Agent installation completed successfully."
Write-Host "Runtime task: GPT Agent Runtime"
Write-Host "MCP: http://127.0.0.1:8765/mcp"

if (-not $SkipTunnelDownload -and -not $SkipSetupWizard) {
  Say "Opening the local ChatGPT tunnel setup wizard..."
  & $setupBinary --install-root $InstallRoot
  if ($LASTEXITCODE -ne 0) {
    Warn "The runtime is installed, but tunnel setup was not completed. Rerun scripts\windows\Configure-OpenAITunnel.ps1 when ready."
  }
} elseif ($SkipTunnelDownload) {
  Warn "Tunnel setup skipped because tunnel-client download was disabled."
} else {
  Write-Host "Tunnel setup: run scripts\windows\Configure-OpenAITunnel.ps1 when ready." -ForegroundColor Yellow
}
