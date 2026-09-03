param(
  [int]$Minutes = 30,
  [string]$InstallRoot = "C:\GPTAgent"
)
$ErrorActionPreference = "Stop"
$Minutes = [Math]::Max(1, [Math]::Min(60, $Minutes))
Write-Host "FULL SHELL permits arbitrary PowerShell commands inside the selected workspace." -ForegroundColor Yellow
Write-Host "Repository build scripts can be malicious. This grant is temporary." -ForegroundColor Yellow
$confirm = Read-Host "Type exactly FULL ACCESS to unlock for $Minutes minutes"
if ($confirm -ne "FULL ACCESS") { throw "Confirmation mismatch." }
$now = [DateTimeOffset]::UtcNow
$grant = [ordered]@{
  confirmedByUser = $true
  grantedAt = $now.ToString("o")
  expiresAt = $now.AddMinutes($Minutes).ToString("o")
  user = "$env:USERDOMAIN\$env:USERNAME"
}
$path = Join-Path $InstallRoot "data\full-shell.grant.json"
New-Item -ItemType Directory -Force -Path (Split-Path $path) | Out-Null
$grant | ConvertTo-Json | Set-Content -LiteralPath $path -Encoding UTF8
Write-Host "FULL SHELL unlocked for $Minutes minutes." -ForegroundColor Green
