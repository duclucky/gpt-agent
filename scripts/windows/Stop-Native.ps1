$ErrorActionPreference = "Continue"
& schtasks.exe /End /TN "GPT Agent Tunnel" 2>$null | Out-Null
& schtasks.exe /End /TN "GPT Agent Runtime" 2>$null | Out-Null
Write-Host "GPT Agent tasks stopped." -ForegroundColor Yellow
