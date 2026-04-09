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

function Install-Skills {
  param(
    [string]$SrcName,
    [string]$DstDir
  )
  $src = Join-Path $ShareDir $SrcName
  if (-not (Test-Path $src)) { return }

  New-Item -ItemType Directory -Path $DstDir -Force | Out-Null

  # Clean obsolete skills
  foreach ($obs in @("cask","gask","oask","lask","cpend","gpend","opend","lpend","cping","gping","oping","lping","ping","auto")) {
    $obsDir = Join-Path $DstDir $obs
    if (Test-Path $obsDir) { Remove-Item $obsDir -Recurse -Force }
  }

  $count = 0
  Get-ChildItem $src -Directory | ForEach-Object {
    $skillName = $_.Name
    if ($skillName -eq "docs") { return }

    $skillMd = $null
    $bashMd = Join-Path $_.FullName "SKILL.md.bash"
    $plainMd = Join-Path $_.FullName "SKILL.md"
    if (Test-Path $bashMd) { $skillMd = $bashMd }
    elseif (Test-Path $plainMd) { $skillMd = $plainMd }
    else { return }

    $dstSkillDir = Join-Path $DstDir $skillName
    New-Item -ItemType Directory -Path $dstSkillDir -Force | Out-Null
    Copy-Item $skillMd -Destination (Join-Path $dstSkillDir "SKILL.md") -Force

    # Copy subdirectories (references/ etc.)
    Get-ChildItem $_.FullName -Directory | ForEach-Object {
      Copy-Item $_.FullName -Destination (Join-Path $dstSkillDir $_.Name) -Recurse -Force
    }
    $count++
  }

  # Copy shared docs
  $docsDir = Join-Path $src "docs"
  if (Test-Path $docsDir) {
    $dstDocs = Join-Path $DstDir "docs"
    if (Test-Path $dstDocs) { Remove-Item $dstDocs -Recurse -Force }
    Copy-Item $docsDir -Destination $dstDocs -Recurse
  }

  Write-Ok "Installed $count skills from $SrcName to $DstDir"
}

function Install-ClaudeMd {
  $claudeMd = Join-Path $env:USERPROFILE ".claude\CLAUDE.md"
  $template = Join-Path $ShareDir "config\claude-md-ccb.md"
  $startMarker = "<!-- CCB_CONFIG_START -->"
  $endMarker = "<!-- CCB_CONFIG_END -->"

  if (-not (Test-Path $template)) { return }

  $claudeDir = Join-Path $env:USERPROFILE ".claude"
  New-Item -ItemType Directory -Path $claudeDir -Force | Out-Null

  $templateContent = Get-Content $template -Raw

  if (Test-Path $claudeMd) {
    $content = Get-Content $claudeMd -Raw
    if ($content -match [regex]::Escape($startMarker)) {
      # Replace existing block
      $pattern = [regex]::Escape($startMarker) + "[\s\S]*?" + [regex]::Escape($endMarker)
      $content = [regex]::Replace($content, $pattern, $templateContent.TrimEnd())
      Set-Content $claudeMd -Value $content -NoNewline
      Write-Ok "Updated CCB config in CLAUDE.md"
    } else {
      Add-Content $claudeMd -Value ("`n" + $templateContent)
      Write-Ok "Added CCB config to CLAUDE.md"
    }
  } else {
    Set-Content $claudeMd -Value $templateContent
    Write-Ok "Created CLAUDE.md with CCB config"
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

    # Install Claude skills
    Install-Skills -SrcName "claude_skills" -DstDir (Join-Path $env:USERPROFILE ".claude\skills")

    # Install Codex skills
    $codexHome = if ($env:CODEX_HOME) { $env:CODEX_HOME } else { Join-Path $env:USERPROFILE ".codex" }
    Install-Skills -SrcName "codex_skills" -DstDir (Join-Path $codexHome "skills")

    # Inject CLAUDE.md config
    Install-ClaudeMd

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
