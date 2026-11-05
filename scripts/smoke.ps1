# Installs a release from local archives through install.ps1, then checks
# doctor, update and uninstall, as a user would run them.
#
# Usage: scripts/smoke.ps1 <dir with archives and SHA256SUMS> <tag>
# The tag is a vX.Y.Z release, installed from the stable channel, or canary,
# installed from the latest channel.
param(
  [Parameter(Mandatory = $true, Position = 0)] [string]$Dist,
  [Parameter(Mandatory = $true, Position = 1)] [string]$Tag
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$port = if ($env:SMOKE_PORT) { $env:SMOKE_PORT } else { "8765" }

$work = Join-Path ([System.IO.Path]::GetTempPath()) ("prefactor-smoke-" + [System.Guid]::NewGuid().ToString("N"))
$env:USERPROFILE = Join-Path $work "home"
$env:HOME = $env:USERPROFILE
$releaseDir = Join-Path $work "releases\download\$Tag"
New-Item -ItemType Directory -Path $env:USERPROFILE, $releaseDir -Force | Out-Null
Copy-Item -Path (Join-Path $Dist "*.zip"), (Join-Path $Dist "SHA256SUMS") -Destination $releaseDir

$server = Start-Process -FilePath python -ArgumentList @("-m", "http.server", $port, "--bind", "127.0.0.1", "--directory", $work) -PassThru -WindowStyle Hidden
try {
  Start-Sleep -Seconds 2
  $env:PREFACTOR_RELEASE_BASE_URL = "http://127.0.0.1:$port/releases/download"
  # The stable channel reads releases/latest, which GitHub redirects to the
  # newest tag; here it is that tag's directory.
  $env:PREFACTOR_RELEASE_LATEST_BASE_URL = "$env:PREFACTOR_RELEASE_BASE_URL/$Tag"
  $channel = if ($Tag -eq "canary") { "latest" } else { "stable" }
  $prefactor = Join-Path $env:USERPROFILE ".prefactor\bin\prefactor.exe"

  function Get-Doctor {
    $output = & $prefactor doctor 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0) { throw "doctor failed: $output" }
    return $output
  }

  & (Join-Path $repoRoot "install.ps1") $channel
  & $prefactor --version
  if ($LASTEXITCODE -ne 0) { throw "--version failed" }
  $doctor = Get-Doctor
  Write-Host $doctor
  if ($doctor -notmatch "channel: $channel") { throw "doctor does not show channel $channel" }

  # On Windows, update replaces the binary after this process exits.
  $update = & $prefactor update 2>&1 | Out-String
  Write-Host $update
  if ($LASTEXITCODE -ne 0 -or $update -notmatch "Update started") { throw "update failed: $update" }
  $updated = $false
  for ($attempt = 0; $attempt -lt 60; $attempt++) {
    Start-Sleep -Milliseconds 500
    $doctor = Get-Doctor
    if ($doctor -match "resolvedTag: $([regex]::Escape($Tag))\r?\n") { $updated = $true; break }
  }
  Write-Host $doctor
  if (-not $updated) { throw "update did not install $Tag" }

  # The managed binary uninstalls itself, although Windows locks it.
  & $prefactor uninstall
  if ($LASTEXITCODE -ne 0) { throw "uninstall failed" }
  if (Test-Path -LiteralPath $prefactor) { throw "uninstall left $prefactor" }
  Write-Host "Smoke test passed for $Tag."
} finally {
  Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
  Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
