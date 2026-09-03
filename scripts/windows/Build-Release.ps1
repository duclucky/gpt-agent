param(
  [string]$Version = "0.1.0",
  [string]$OutputDir = ""
)

$ErrorActionPreference = "Stop"
$SourceRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
if (-not $OutputDir) { $OutputDir = Join-Path $SourceRoot "release" }
$OutputDir = [IO.Path]::GetFullPath($OutputDir)
$DistRoot = Join-Path $SourceRoot "dist"
$StageName = "gpt-agent-v$Version-windows-amd64"
$StageRoot = Join-Path $DistRoot $StageName
$ZipPath = Join-Path $OutputDir ($StageName + ".zip")
$ShaPath = $ZipPath + ".sha256"

function Say([string]$Text) { Write-Host "==> $Text" -ForegroundColor Cyan }
function Fail([string]$Text) { throw $Text }

Say "Cleaning release staging"
Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $StageRoot
New-Item -ItemType Directory -Force -Path $StageRoot,$OutputDir | Out-Null

Say "Formatting Go sources"
& gofmt.exe -w (Get-ChildItem -Path (Join-Path $SourceRoot "go-core") -Recurse -Filter "*.go" | ForEach-Object FullName)
if ($LASTEXITCODE -ne 0) { Fail "gofmt failed" }

Say "Running Go tests and vet"
Push-Location (Join-Path $SourceRoot "go-core")
try {
  & go.exe test ./...
  if ($LASTEXITCODE -ne 0) { Fail "go test failed" }
  & go.exe vet ./...
  if ($LASTEXITCODE -ne 0) { Fail "go vet failed" }
} finally { Pop-Location }

Say "Parsing PowerShell scripts"
$parseErrors = @()
Get-ChildItem -Path (Join-Path $SourceRoot "scripts\windows") -Filter "*.ps1" -File | ForEach-Object {
  $tokens = $null
  $errors = $null
  [void][System.Management.Automation.Language.Parser]::ParseFile($_.FullName,[ref]$tokens,[ref]$errors)
  if ($errors) { $parseErrors += $errors }
}
if ($parseErrors.Count) {
  $parseErrors | ForEach-Object { Write-Host $_.Message -ForegroundColor Red }
  Fail "PowerShell syntax gate failed"
}

Say "Running public hygiene gate"
$scanFiles = Get-ChildItem -Path $SourceRoot -Recurse -File | Where-Object {
  $_.FullName -notmatch '[\\/](?:\.git|dist|release|node_modules|\.venv-tools)[\\/]' -and
  $_.Extension -notin @('.exe','.zip')
}
$forbidden = @(
  [pscustomobject]@{ Source = 'built-in'; Pattern = '(?i)[A-Z]:\\Users\\[^\\\r\n]+\\' }
)

$privateDenylistPath = $env:GPT_AGENT_PUBLIC_DENYLIST_FILE
if ($privateDenylistPath) {
  if (-not (Test-Path -LiteralPath $privateDenylistPath -PathType Leaf)) {
    Fail "GPT_AGENT_PUBLIC_DENYLIST_FILE does not point to a readable file."
  }
  Get-Content -LiteralPath $privateDenylistPath | ForEach-Object {
    $pattern = $_.Trim()
    if ($pattern -and -not $pattern.StartsWith('#')) {
      $forbidden += [pscustomobject]@{ Source = 'external'; Pattern = $pattern }
    }
  }
}

$violations = @()
foreach ($file in $scanFiles) {
  $text = Get-Content -Raw -LiteralPath $file.FullName -ErrorAction SilentlyContinue
  if ($null -eq $text) { continue }
  foreach ($rule in $forbidden) {
    if ($text -match $rule.Pattern) {
      $violations += "$($file.FullName): public hygiene denylist match ($($rule.Source))"
    }
  }
  if ($file.Name -notlike '*_test.go') {
    if ($text -match '(?i)\b(?:sk|ghp|gho|github_pat|rk|sess)-[A-Za-z0-9._-]{20,}\b') {
      $violations += "$($file.FullName): secret-like token pattern"
    }
  }
}
if ($violations.Count) {
  $violations | Sort-Object -Unique | ForEach-Object { Write-Host $_ -ForegroundColor Red }
  Fail "Public hygiene gate failed"
}

Say "Copying source distribution"
& robocopy.exe $SourceRoot $StageRoot /E /R:2 /W:1 /NFL /NDL /NJH /NJS /NP /XD .git dist release node_modules .venv-tools /XF *.exe *.zip *.sha256 | Out-Null
if ($LASTEXITCODE -gt 7) { Fail "robocopy failed with exit code $LASTEXITCODE" }

Say "Building Windows binaries"
Push-Location (Join-Path $SourceRoot "go-core")
try {
  $env:GOWORK = "off"
  & go.exe build -trimpath -ldflags "-s -w" -o (Join-Path $StageRoot "gpt-agent.exe") .
  if ($LASTEXITCODE -ne 0) { Fail "gpt-agent.exe build failed" }
  & go.exe build -trimpath -ldflags "-s -w" -o (Join-Path $StageRoot "gpt-agent-setup.exe") .\cmd\setup
  if ($LASTEXITCODE -ne 0) { Fail "gpt-agent-setup.exe build failed" }
} finally { Pop-Location }

Say "Smoke testing runtime binary"
$SmokeRoot = Join-Path $env:TEMP ("gpt-agent-release-smoke-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $SmokeRoot | Out-Null
try {
  $cfg = Get-Content -Raw -LiteralPath (Join-Path $StageRoot "config.example.json") | ConvertFrom-Json
  $cfg.server.port = 18765
  $cfg.workspaces = @([pscustomobject]@{id="main";root=$StageRoot;enabled=$true})
  $cfg.lspServers = [pscustomobject]@{}
  $cfg.debugAdapters = [pscustomobject]@{}
  $cfg | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath (Join-Path $SmokeRoot "config.json") -Encoding UTF8
  $stdout = Join-Path $SmokeRoot "stdout.log"
  $stderr = Join-Path $SmokeRoot "stderr.log"
  $oldHome = $env:GPT_AGENT_HOME
  $oldRoot = $env:GPT_AGENT_APP_ROOT
  $env:GPT_AGENT_HOME = $SmokeRoot
  $env:GPT_AGENT_APP_ROOT = $StageRoot
  $proc = Start-Process -FilePath (Join-Path $StageRoot "gpt-agent.exe") -PassThru -WindowStyle Hidden -RedirectStandardOutput $stdout -RedirectStandardError $stderr
  try {
    $deadline = (Get-Date).AddSeconds(20)
    $healthy = $false
    do {
      try {
        $h = Invoke-RestMethod -Uri "http://127.0.0.1:18765/healthz" -TimeoutSec 2
        if ($h.ok -eq $true -and $h.name -eq "GPT Agent" -and $h.version -eq $Version) { $healthy = $true; break }
      } catch {}
      Start-Sleep -Milliseconds 250
    } while ((Get-Date) -lt $deadline)
    if (-not $healthy) {
      $outText = (Get-Content -Raw -ErrorAction SilentlyContinue $stdout) + (Get-Content -Raw -ErrorAction SilentlyContinue $stderr)
      Fail "runtime smoke test failed: $outText"
    }
  } finally {
    if ($proc -and -not $proc.HasExited) { Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue }
    if ($null -eq $oldHome) { Remove-Item Env:GPT_AGENT_HOME -ErrorAction SilentlyContinue } else { $env:GPT_AGENT_HOME = $oldHome }
    if ($null -eq $oldRoot) { Remove-Item Env:GPT_AGENT_APP_ROOT -ErrorAction SilentlyContinue } else { $env:GPT_AGENT_APP_ROOT = $oldRoot }
  }
} finally {
  Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $SmokeRoot
}

Say "Generating MANIFEST.sha256.json"
$manifestFiles = [ordered]@{}
Get-ChildItem -Path $StageRoot -Recurse -File | Where-Object { $_.Name -ne 'MANIFEST.sha256.json' } | Sort-Object FullName | ForEach-Object {
  $rel = [IO.Path]::GetRelativePath($StageRoot,$_.FullName).Replace('\\','/')
  $manifestFiles[$rel] = [ordered]@{
    sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $_.FullName).Hash.ToLowerInvariant()
    bytes = $_.Length
  }
}
$manifest = [ordered]@{
  version = $Version
  generatedAt = [DateTime]::UtcNow.ToString('o')
  files = $manifestFiles
}
[IO.File]::WriteAllText((Join-Path $StageRoot 'MANIFEST.sha256.json'),($manifest | ConvertTo-Json -Depth 8),[Text.UTF8Encoding]::new($false))

Say "Creating release ZIP"
Remove-Item -Force -ErrorAction SilentlyContinue $ZipPath,$ShaPath
Compress-Archive -Path (Join-Path $StageRoot '*') -DestinationPath $ZipPath -CompressionLevel Optimal
$zipHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $ZipPath).Hash.ToLowerInvariant()
[IO.File]::WriteAllText($ShaPath,"$zipHash  $([IO.Path]::GetFileName($ZipPath))`n",[Text.UTF8Encoding]::new($false))

Say "Release ready"
Write-Host "ZIP:    $ZipPath"
Write-Host "SHA256: $zipHash"
Write-Host "Files:  $($manifestFiles.Count + 1)"
