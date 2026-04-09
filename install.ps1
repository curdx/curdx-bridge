<#
.SYNOPSIS
  CCB Installer for Windows — downloads pre-built binaries from GitHub Release.

.DESCRIPTION
  One-line install (PowerShell):
    irm https://raw.githubusercontent.com/curdx/curdx-bridge/main/install.ps1 | iex

  Or run locally:
    .\install.ps1 install
    .\install.ps1 uninstall

.PARAMETER Command
  install or uninstall (default: install)
#>
param(
  [Parameter(Position = 0)]
  [ValidateSet("install", "uninstall")]
  [string]$Command = "install",

  [string]$Version = "",
  [string]$InstallDir = ""
)

$ErrorActionPreference = "Stop"

# UTF-8 output
try { $OutputEncoding = [System.Text.UTF8Encoding]::new($false) } catch {}
try { [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false) } catch {}
try { chcp 65001 | Out-Null } catch {}

$Repo = "curdx/curdx-bridge"

if (-not $InstallDir) {
  $InstallDir = Join-Path $env:LOCALAPPDATA "ccb\bin"
}
$ShareDir = Join-Path $env:LOCALAPPDATA "ccb\share"

function Write-Info  { param($msg) Write-Host "  > $msg" -ForegroundColor Cyan }
function Write-Ok    { param($msg) Write-Host "  OK $msg" -ForegroundColor Green }
function Write-Warn  { param($msg) Write-Host "  ! $msg" -ForegroundColor Yellow }
function Write-Fail  { param($msg) Write-Host "  X $msg" -ForegroundColor Red; exit 1 }

function Get-LatestVersion {
  try {
    $resp = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing
    return $resp.tag_name
  } catch {
    return ""
  }
}

function Install-CCB {
  Write-Host ""
  Write-Host "  CCB Installer for Windows" -ForegroundColor White
  Write-Host ""

  $arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { Write-Fail "32-bit Windows is not supported" }

  $ver = $Version
  if (-not $ver) {
    Write-Info "Checking latest version..."
    $ver = Get-LatestVersion
    if (-not $ver) {
      Write-Fail "Could not determine latest version. Set -Version v5.3.0 manually."
    }
  }

  $archive = "ccb-windows-${arch}.zip"
  $url = "https://github.com/$Repo/releases/download/$ver/$archive"

  $tmpDir = Join-Path $env:TEMP "ccb-install-$(Get-Random)"
  New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null

  try {
    Write-Info "Downloading CCB $ver for windows/$arch..."
    $zipPath = Join-Path $tmpDir $archive
    Invoke-WebRequest -Uri $url -OutFile $zipPath -UseBasicParsing

    Write-Info "Extracting..."
    Expand-Archive -Path $zipPath -DestinationPath $tmpDir -Force

    $srcDir = Join-Path $tmpDir "ccb-windows-${arch}"
    if (-not (Test-Path $srcDir)) {
      Write-Fail "Archive structure unexpected — missing $srcDir"
    }

    # Install binaries
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    $count = 0
    Get-ChildItem $srcDir -File | ForEach-Object {
      if ($_.Extension -eq ".exe") {
        Copy-Item $_.FullName -Destination (Join-Path $InstallDir $_.Name) -Force
        $count++
      }
    }

    # Install skills and config
    New-Item -ItemType Directory -Path $ShareDir -Force | Out-Null
    foreach ($dir in @("claude_skills", "codex_skills", "config")) {
      $src = Join-Path $srcDir $dir
      if (Test-Path $src) {
        $dst = Join-Path $ShareDir $dir
        if (Test-Path $dst) { Remove-Item $dst -Recurse -Force }
        Copy-Item $src -Destination $dst -Recurse
      }
    }

    Write-Ok "Installed $count binaries to $InstallDir"

    # Add to PATH if needed
    $userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
    if ($userPath -notlike "*$InstallDir*") {
      Write-Info "Adding $InstallDir to user PATH..."
      [Environment]::SetEnvironmentVariable("PATH", "$InstallDir;$userPath", "User")
      $env:PATH = "$InstallDir;$env:PATH"
      Write-Ok "Added to PATH (restart terminal to take effect)"
    }

    Write-Host ""
    Write-Ok "Installation complete!"
    Write-Host ""
    Write-Host "  Get started:" -ForegroundColor White
    Write-Host "    ccb --help          # Show help"
    Write-Host "    ccb codex claude    # Start with Codex + Claude"
    Write-Host ""

  } finally {
    Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
  }
}

function Uninstall-CCB {
  Write-Host ""
  Write-Host "  CCB Uninstaller" -ForegroundColor White
  Write-Host ""

  if (Test-Path $InstallDir) {
    $exes = Get-ChildItem $InstallDir -Filter "*.exe" | Where-Object { $_.Name -match "^(ccb|ask|cask|gask|oask|lask|cpend|gpend|lpend|cping|gping|lping|askd|laskd|autoloop|autonew|ctx-transfer|ccb-|pend)\b" }
    foreach ($exe in $exes) {
      Remove-Item $exe.FullName -Force -ErrorAction SilentlyContinue
      Write-Info "Removed $($exe.Name)"
    }
  }

  if (Test-Path $ShareDir) {
    Remove-Item $ShareDir -Recurse -Force -ErrorAction SilentlyContinue
    Write-Info "Removed $ShareDir"
  }

  # Remove from PATH
  $userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
  if ($userPath -like "*$InstallDir*") {
    $newPath = ($userPath -split ";" | Where-Object { $_ -ne $InstallDir }) -join ";"
    [Environment]::SetEnvironmentVariable("PATH", $newPath, "User")
    Write-Info "Removed $InstallDir from PATH"
  }

  Write-Ok "Uninstall complete"
  Write-Host ""
}

switch ($Command) {
  "install"   { Install-CCB }
  "uninstall" { Uninstall-CCB }
}
