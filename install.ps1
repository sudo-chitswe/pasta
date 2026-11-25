# Pasta Windows Installer
# Downloads and installs Pasta from GitHub releases without needing to clone the repository.
#
# Quick install in PowerShell:
#   irm https://raw.githubusercontent.com/sudochitswe-v2/pasta/master/install.ps1 | iex

param(
  [string]$Version = "latest",
  [string]$Repo = "sudochitswe-v2/pasta"
)

$ErrorActionPreference = "Stop"

Write-Host "==> Installing Pasta for Windows..." -ForegroundColor Green

# 1. Prepare installation path
$destDir = Join-Path $env:LOCALAPPDATA "Pasta"
$dest = Join-Path $destDir "pasta.exe"
$cliDest = Join-Path $destDir "pasta-cli.exe"

# 2. Stop running instance to avoid file locks
Stop-Process -Name pasta -ErrorAction SilentlyContinue | Out-Null
Start-Sleep -Milliseconds 800

New-Item -ItemType Directory -Force -Path $destDir | Out-Null

# 3. Check for local build if running from a cloned repository script
$localBin = $null
$localCli = $null
if (-not [string]::IsNullOrEmpty($PSScriptRoot)) {
  $localBin = Join-Path $PSScriptRoot "bin\pasta.exe"
  $localCli = Join-Path $PSScriptRoot "bin\pasta-cli.exe"
}

if ($localBin -and $localCli -and (Test-Path $localBin) -and (Test-Path $localCli)) {
  Write-Host "--> Using local build from current directory..." -ForegroundColor Cyan
  Copy-Item -Force $localBin $dest
  Copy-Item -Force $localCli $cliDest
} else {
  # Remote GitHub install
  $zipUrl = if ($Version -eq "latest") {
    "https://github.com/$Repo/releases/latest/download/pasta-windows-amd64.zip"
  } else {
    "https://github.com/$Repo/releases/download/$Version/pasta-windows-amd64.zip"
  }

  $tempZip = Join-Path $env:TEMP "pasta-windows-$([guid]::NewGuid().ToString('N')).zip"
  $tempExtract = Join-Path $env:TEMP "pasta-extract-$([guid]::NewGuid().ToString('N'))"

  try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
  } catch {}

  Write-Host "--> Downloading Pasta from GitHub ($Version)..." -ForegroundColor Cyan
  try {
    Invoke-WebRequest -Uri $zipUrl -OutFile $tempZip -UseBasicParsing
    Expand-Archive -Path $tempZip -DestinationPath $tempExtract -Force
    
    $extractedBin = Get-ChildItem -Path $tempExtract -Filter "pasta.exe" -Recurse | Select-Object -First 1
    $extractedCli = Get-ChildItem -Path $tempExtract -Filter "pasta-cli.exe" -Recurse | Select-Object -First 1

    if (-not $extractedBin) {
      throw "pasta.exe not found in release archive."
    }
    Copy-Item -Force $extractedBin.FullName $dest
    if ($extractedCli) { Copy-Item -Force $extractedCli.FullName $cliDest }
  } catch {
    Write-Host "--> Release archive unavailable; downloading binaries from repository..." -ForegroundColor Yellow
    Invoke-WebRequest -Uri "https://raw.githubusercontent.com/$Repo/master/bin/pasta.exe" -OutFile $dest -UseBasicParsing
    Invoke-WebRequest -Uri "https://raw.githubusercontent.com/$Repo/master/bin/pasta-cli.exe" -OutFile $cliDest -UseBasicParsing
  } finally {
    Remove-Item -Path $tempZip -Force -ErrorAction SilentlyContinue
    Remove-Item -Path $tempExtract -Recurse -Force -ErrorAction SilentlyContinue
  }
}

Write-Host "✓ Installed binaries to $destDir" -ForegroundColor Green

# 4. Add to User PATH if missing
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
$pathEntries = if ([string]::IsNullOrEmpty($userPath)) { @() } else { $userPath -split ';' | Where-Object { $_ -ne '' } }
if ($pathEntries -notcontains $destDir) {
  $newPath = if ([string]::IsNullOrEmpty($userPath)) { $destDir } elseif ($userPath.EndsWith(";")) { $userPath + $destDir } else { $userPath + ";" + $destDir }
  [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
  $env:Path = "$destDir;" + $env:Path
  Write-Host "✓ Added $destDir to user PATH." -ForegroundColor Green
}

# 5. Register auto-start in registry Run key
$runKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"
if (-not (Test-Path $runKey)) {
  New-Item -Path $runKey -Force | Out-Null
}
New-ItemProperty -Path $runKey -Name "Pasta" -Value "`"$dest`"" -PropertyType String -Force | Out-Null
Write-Host "✓ Registered auto-start at logon (HKCU Run key)." -ForegroundColor Green

# 6. Print QR code pairing link before launching daemon so config.json token is initialized first
Write-Host ""
Write-Host "====================================================" -ForegroundColor Green
Write-Host "   Pasta successfully installed!                    " -ForegroundColor Green
Write-Host "====================================================" -ForegroundColor Green
Write-Host ""

if (Test-Path $cliDest) {
  & $cliDest --qr
}

# 7. Launch background process
Write-Host "✓ Launching Pasta background daemon..." -ForegroundColor Green
Start-Process $dest
