# install.ps1 - install shrink on Windows from its GitHub releases.
#
# usage:  irm https://raw.githubusercontent.com/matthewsawatzky/shrink/main/install.ps1 | iex
#    or:  ./install.ps1 [-Version v0.1.0]   (default: latest)
param([string]$Version = "latest")

$Repo = "matthewsawatzky/shrink"
$ErrorActionPreference = "Stop"

# --- detect platform ---
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }

# --- pick the release ---
if ($Version -eq "latest") {
    Write-Host "shrink: looking up the latest release..."
    $latest = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
    $Version = $latest.tag_name
}
Write-Host "shrink: installing shrink $Version (windows/$arch)"

$asset = "shrink-$Version-windows-$arch.tar.gz"
$url = "https://github.com/$Repo/releases/download/$Version/$asset"

# --- download + extract (Windows 10+ ships tar, which handles tar.gz) ---
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Force $tmp | Out-Null
try {
    Invoke-WebRequest -Uri $url -OutFile (Join-Path $tmp $asset)
    tar -xzf (Join-Path $tmp $asset) -C $tmp
}
catch {
    Write-Error "shrink: download failed - $Version may not have a windows/$arch build"
}

# --- install ---
$dir = "$env:LOCALAPPDATA\Programs\shrink"
New-Item -ItemType Directory -Force $dir | Out-Null
Move-Item -Force (Join-Path $tmp "shrink.exe") (Join-Path $dir "shrink.exe")
Write-Host "shrink: installed $dir\shrink.exe"

# --- PATH hint ---
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$dir*") {
    Write-Host "shrink: note: $dir is not on your PATH - add it with:"
    Write-Host "  [Environment]::SetEnvironmentVariable('Path', `"$userPath;$dir`", 'User')"
}

# --- shrink needs ffmpeg (ffprobe ships with it); offer to install if missing ---
if (Get-Command ffmpeg -ErrorAction SilentlyContinue) {
    if (-not (Get-Command ffprobe -ErrorAction SilentlyContinue)) {
        Write-Host "shrink: warning: ffprobe not found - complete ffmpeg installs ship both; reinstall ffmpeg"
    }
}
elseif (Get-Command winget -ErrorAction SilentlyContinue) {
    $reply = Read-Host "shrink: ffmpeg is not installed. Install it now with 'winget install Gyan.FFmpeg'? [y/N]"
    if ($reply -match '^[yY]') {
        winget install --id Gyan.FFmpeg --accept-source-agreements --accept-package-agreements
        if (Get-Command ffmpeg -ErrorAction SilentlyContinue) {
            Write-Host "shrink: ffmpeg installed - shrink is ready"
        }
        else {
            Write-Host "shrink: warning: still no ffmpeg on PATH - open a new terminal and retry"
        }
    }
    else {
        Write-Host "shrink: note: shrink needs ffmpeg - install it yourself with: winget install Gyan.FFmpeg"
    }
}
else {
    Write-Host "shrink: warning: ffmpeg not found - shrink needs it (winget install Gyan.FFmpeg)"
}

& "$dir\shrink.exe" -V
Remove-Item -Recurse -Force $tmp