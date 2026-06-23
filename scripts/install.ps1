#Requires -Version 5.1
<#
  Installs the latest gidm CLI + daemon on Windows. No Go toolchain required.

    irm https://raw.githubusercontent.com/K-RED90/gidm/main/scripts/install.ps1 | iex

  Override the destination with $env:GIDM_BIN_DIR (default
  %LOCALAPPDATA%\Programs\gidm), or pin a version with $env:GIDM_VERSION (e.g. v0.1.0).
#>
$ErrorActionPreference = 'Stop'

$repo = 'K-RED90/gidm'
$binDir = if ($env:GIDM_BIN_DIR) { $env:GIDM_BIN_DIR } else { "$env:LOCALAPPDATA\Programs\gidm" }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  default { throw "gidm: unsupported architecture '$env:PROCESSOR_ARCHITECTURE'" }
}

# Resolve the release tag (e.g. v0.1.0): honour $env:GIDM_VERSION, else ask GitHub
# for the latest. The asset name carries the version without the leading "v".
$tag = $env:GIDM_VERSION
if (-not $tag) {
  $tag = (Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest").tag_name
}
if (-not $tag) { throw 'gidm: could not determine the latest release' }
$ver = $tag.TrimStart('v')

$asset = "gidm_${ver}_windows_${arch}.zip"
$url = "https://github.com/$repo/releases/download/$tag/$asset"

$work = Join-Path ([System.IO.Path]::GetTempPath()) "gidm-$ver"
$zip = "$work.zip"
Write-Host "Downloading $asset ($tag)..."
Invoke-WebRequest -Uri $url -OutFile $zip
if (Test-Path $work) { Remove-Item $work -Recurse -Force }
Expand-Archive -Path $zip -DestinationPath $work -Force

New-Item -ItemType Directory -Force -Path $binDir | Out-Null
Copy-Item (Join-Path $work 'gidm.exe'), (Join-Path $work 'gidmd.exe') $binDir -Force
Remove-Item $zip, $work -Recurse -Force

# Add to the user PATH (Windows only) if it isn't already there.
if ($env:OS -eq 'Windows_NT') {
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  if (($userPath -split ';') -notcontains $binDir) {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$binDir".Trim(';'), 'User')
    Write-Host "Added $binDir to your PATH. Open a new terminal to use 'gidm'."
  }
}
Write-Host "Installed gidm $ver -> $binDir\{gidm.exe,gidmd.exe}"
