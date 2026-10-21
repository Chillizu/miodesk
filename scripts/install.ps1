#requires -version 5.1
<#
.SYNOPSIS
  Download and verify the latest miodesk Windows x64 release.
.DESCRIPTION
  Installs only one verified binary. Does not edit PATH, registry, services,
  user profiles, firewall rules, or OpenAI tunnel credentials. Supports a local
  release mirror for offline verification/testing.
#>
[CmdletBinding()]
param(
  [string]$Version = "latest",
  [string]$BinDir = "",
  [string]$Repository = "Chillizu/miodesk",
  [string]$ReleaseBase = ""
)
$ErrorActionPreference = 'Stop'

if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
  throw "This installer targets Windows x64 only. For Linux/macOS use scripts/install.sh."
}
if (-not [Environment]::Is64BitOperatingSystem) {
  throw "miodesk has no native Windows 32-bit release. Check Releases for supported platforms."
}
$osArch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
if ($osArch -ne 'X64') {
  throw "Official Windows release supports x64 only (detected $osArch). Check Releases before proceeding."
}
if ([string]::IsNullOrWhiteSpace($BinDir)) {
  $BinDir = Join-Path $env:LOCALAPPDATA 'Programs\miodesk'
}
if ($Version -ne 'latest') {
  $Version = $Version.TrimStart('v')
  if ($Version -notmatch '^\d+\.\d+\.\d+(-[A-Za-z0-9.-]+)?$') {
    throw "Invalid version. Example: 0.2.3"
  }
}
if ($Repository -notmatch '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$') {
  throw "Invalid GitHub owner/repository name."
}
if ($ReleaseBase) {
  $base = $ReleaseBase.TrimEnd('/')
} elseif ($Version -eq 'latest') {
  $base = "https://github.com/$Repository/releases/latest/download"
} else {
  $base = "https://github.com/$Repository/releases/download/v$Version"
}
$asset = 'miodesk-windows-amd64.exe'
$tempDir = Join-Path ([IO.Path]::GetTempPath()) ('miodesk-install-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tempDir -Force | Out-Null
try {
  # Windows PowerShell 5.1 uses TLS 1.2 for github.com.
  [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
  $downloadPath = Join-Path $tempDir $asset
  $sumsPath = Join-Path $tempDir 'checksums.txt'
  Write-Host "[INFO] downloading $asset"
  Invoke-WebRequest -Uri "$base/$asset" -OutFile $downloadPath -UseBasicParsing
  Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile $sumsPath -UseBasicParsing

  $expected = $null
  foreach ($line in Get-Content -LiteralPath $sumsPath) {
    if ($line -match '^([a-fA-F0-9]{64})\s+\*?miodesk-windows-amd64\.exe\s*$') {
      $expected = $matches[1].ToLowerInvariant()
      break
    }
  }
  if (-not $expected) { throw "Release checksums.txt does not list $asset" }
  $actual = (Get-FileHash -LiteralPath $downloadPath -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($actual -ne $expected) { throw "Checksum mismatch for $asset; refusing to install" }
  Write-Host "[OK] SHA-256 verified"

  New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
  $dest = Join-Path $BinDir 'miodesk.exe'
  # Keep the existing binary intact if the download or checksum is invalid.
  # A running old binary may block replacement on Windows; report that cleanly.
  $stage = Join-Path $BinDir ('.miodesk-' + [Guid]::NewGuid().ToString('N') + '.exe')
  try {
    Copy-Item -LiteralPath $downloadPath -Destination $stage
    Move-Item -LiteralPath $stage -Destination $dest -Force
  } finally {
    Remove-Item -LiteralPath $stage -Force -ErrorAction SilentlyContinue
  }

  Write-Host "[OK] installed: $dest"
  & $dest version
  Write-Host ""
  Write-Host "Next (PowerShell, copy each line):"
  # The default GitHub Release may be older than current main: advertise
  # newly added commands only when this installed binary supports them.
  $cliHelp = (& $dest help | Out-String)
  if ($cliHelp -match '(?m)^\s+preflight\s+') {
    Write-Host "  & '$dest' preflight --json"
  }
  Write-Host "  & '$dest' setup --workspace 'C:\path\to\your\project'"
  Write-Host "  & '$dest' doctor --json"
  Write-Host "  & '$dest' serve"
  Write-Host "Local MCP works without an OpenAI Tunnel. See docs/AGENT_SETUP.md for ChatGPT setup."
  Write-Host "Windows systemd services are not supported; start Core in a terminal first."
} finally {
  Remove-Item -LiteralPath $tempDir -Recurse -Force -ErrorAction SilentlyContinue
}
