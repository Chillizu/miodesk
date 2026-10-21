# Regression test for Windows PowerShell installation. Run on Windows runners
# with Go and Python available. Uses a local release fixture, no GitHub network
# dependency, no changes to PATH, startup or OS services.
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$testDir = Join-Path ([IO.Path]::GetTempPath()) ('miodesk-win-install-test-' + [Guid]::NewGuid().ToString('N'))
$release = Join-Path $testDir 'release'
$binDir = Join-Path $testDir 'bin'
$config = Join-Path $testDir 'config'
$project = Join-Path $testDir 'project'
New-Item -ItemType Directory -Force -Path $release,$binDir,$config,$project | Out-Null
$asset = Join-Path $release 'miodesk-windows-amd64.exe'
$server = $null
try {
  & go build -buildvcs=false -o $asset ./cmd/miodesk
  if ($LASTEXITCODE -ne 0) { throw 'Could not build Windows fixture' }
  $digest = (Get-FileHash -LiteralPath $asset -Algorithm SHA256).Hash.ToLowerInvariant()
  "$digest  miodesk-windows-amd64.exe" | Set-Content -LiteralPath (Join-Path $release 'checksums.txt') -Encoding Ascii
  $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,0)
  $listener.Start()
  $port = $listener.LocalEndpoint.Port
  $listener.Stop()
  $log = Join-Path $testDir 'http.log'
  $errLog = Join-Path $testDir 'http.err'
  $server = Start-Process -FilePath 'python' -ArgumentList @('-u','-m','http.server',"$port",'--bind','127.0.0.1','--directory',('"' + $release + '"')) -PassThru -WindowStyle Hidden -RedirectStandardOutput $log -RedirectStandardError $errLog
  $base = "http://127.0.0.1:$port"
  $ready = $false
  for ($i=0; $i -lt 60; $i++) {
    try {
      $r = Invoke-WebRequest "$base/checksums.txt" -UseBasicParsing -TimeoutSec 1
      if ($r.StatusCode -eq 200) { $ready = $true; break }
    } catch { Start-Sleep -Milliseconds 150 }
  }
  if (-not $ready) { throw "Local release fixture did not start: $(Get-Content $errLog -ErrorAction SilentlyContinue)" }

  & (Join-Path $root 'scripts/install.ps1') -ReleaseBase $base -BinDir $binDir
  $installed = Join-Path $binDir 'miodesk.exe'
  if (!(Test-Path $installed)) { throw 'Binary not installed' }
  if ((Get-FileHash $installed -Algorithm SHA256).Hash.ToLowerInvariant() -ne $digest) { throw 'Installed binary differs from verified fixture' }
  $env:XDG_CONFIG_HOME = $config
  $preflight = (& $installed preflight --json | ConvertFrom-Json)
  if ($preflight.kind -ne 'preflight' -or $preflight.os -ne 'windows' -or $preflight.release_asset -ne 'miodesk-windows-amd64.exe') {
    throw 'Windows preflight returned unexpected platform'
  }
  & $installed setup --non-interactive --workspace $project
  if ($LASTEXITCODE -ne 0) { throw 'Non-interactive Windows setup failed' }
  $doctor = (& $installed doctor --json | ConvertFrom-Json)
  if (-not $doctor.checks) { throw 'Windows doctor JSON was empty' }
  Write-Host '[OK] Windows installer and preflight / setup / doctor'

  Add-Content -LiteralPath $asset -Value 'corrupted'
  $rejected = $false
  try {
    & (Join-Path $root 'scripts/install.ps1') -ReleaseBase $base -BinDir $binDir | Out-Null
  } catch { $rejected = $_.Exception.Message -like '*Checksum mismatch*' }
  if (-not $rejected) { throw 'Corrupt release asset was not rejected' }
  if ((Get-FileHash $installed -Algorithm SHA256).Hash.ToLowerInvariant() -ne $digest) {
    throw 'Failed install modified existing binary'
  }
  Write-Host '[OK] Windows checksum mismatch preserves previous installation'
} finally {
  if ($server -ne $null) { Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue }
  Remove-Item Env:XDG_CONFIG_HOME -ErrorAction SilentlyContinue
  Remove-Item -LiteralPath $testDir -Recurse -Force -ErrorAction SilentlyContinue
}
