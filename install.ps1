# Ogcode Installer for Windows
# Usage: irm http://ogcode.xyz/install.ps1 | iex

$ErrorActionPreference = "Stop"

# Ensure TLS 1.2 for the GitHub API on older Windows PowerShell (5.1), where the
# default SecurityProtocol may not include it and the API call would fail.
[Net.ServicePointManager]::SecurityProtocol = `
    [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$repo = "prasenjeet-symon/ogcode"
$installDir = "$env:LOCALAPPDATA\ogcode"

# Detect architecture. In a 32-bit (WOW64) shell on 64-bit Windows,
# PROCESSOR_ARCHITECTURE reports "x86"; PROCESSOR_ARCHITEW6432 holds the real
# OS architecture, so prefer it when present.
$procArch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
$arch = switch ($procArch) {
    "AMD64" { "x86_64" }
    "ARM64" { "arm64" }
    default {
        Write-Host "Unsupported architecture: $procArch" -ForegroundColor Red
        exit 1
    }
}

# Fetch latest release
Write-Host "Fetching latest release..." -ForegroundColor Cyan
$releases = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest"
$tag = $releases.tag_name
# GoReleaser strips the 'v' prefix from the version in asset filenames
$versionNoV = $tag -replace "^v"

$assetName = "ogcode_${versionNoV}_Windows_${arch}.zip"

$asset = $releases.assets | Where-Object { $_.name -eq $assetName } | Select-Object -First 1
if (-not $asset) {
    Write-Host "Could not find release asset: $assetName" -ForegroundColor Red
    exit 1
}

$downloadUrl = $asset.browser_download_url
$zipPath = "$env:TEMP\ogcode.zip"

# Download
Write-Host "Downloading ogcode $tag for $arch..." -ForegroundColor Cyan
Invoke-WebRequest -Uri $downloadUrl -OutFile $zipPath -UseBasicParsing

# Extract (replace any previous install)
if (Test-Path $installDir) {
    try {
        Remove-Item $installDir -Recurse -Force
    } catch {
        Write-Host "Could not replace $installDir - is ogcode still running? Close it and re-run." -ForegroundColor Red
        exit 1
    }
}
New-Item -ItemType Directory -Path $installDir -Force | Out-Null

Write-Host "Extracting to $installDir..." -ForegroundColor Cyan
Expand-Archive -Path $zipPath -DestinationPath $installDir -Force
Remove-Item $zipPath

# Add to PATH if not already there
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$installDir*") {
    Write-Host "Adding $installDir to your PATH..." -ForegroundColor Cyan
    $newPath = "$userPath;$installDir"
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    Write-Host "You will need to open a new terminal window for PATH changes to take effect." -ForegroundColor Yellow
}

# Verify
$binaryPath = "$installDir\ogcode.exe"
$version = & $binaryPath version 2>$null
Write-Host ""
Write-Host "ogcode $version installed successfully!" -ForegroundColor Green
Write-Host ""
Write-Host "Usage:" -ForegroundColor Cyan
Write-Host "  ogcode              # Start in Build Mode" -ForegroundColor White
Write-Host "  ogcode plan         # Start in Plan Mode" -ForegroundColor White
Write-Host "  ogcode version      # Check version" -ForegroundColor White
Write-Host ""
Write-Host "Next step: set your AI provider API key (see README for options)." -ForegroundColor Yellow

# ── Web search ──────────────────────────────────────────────────────────────
# Search is built into the binary: it queries JS-free search endpoints over
# plain HTTP and extracts page text in-process. Nothing to install, and it is
# on by default.
Write-Host ""
Write-Host "Web search is built in and enabled by default." -ForegroundColor Green
