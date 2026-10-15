# uninstall.ps1 - remove shrink from this machine.
#
# usage: irm https://raw.githubusercontent.com/matthewsawatzky/shrink/main/uninstall.ps1 | iex
$ErrorActionPreference = "Stop"

$dir = "$env:LOCALAPPDATA\Programs\shrink"
if (Test-Path "$dir\shrink.exe") {
    Remove-Item -Recurse -Force $dir
    Write-Host "shrink: removed $dir"
}
elseif (Get-Command shrink -ErrorAction SilentlyContinue) {
    $shrinkPath = (Get-Command shrink).Source
    Remove-Item -Force $shrinkPath
    Write-Host "shrink: removed $shrinkPath"
}
else {
    Write-Host "shrink: not found - nothing to remove"
}
Write-Host "shrink: ffmpeg was left alone; winget uninstall Gyan.FFmpeg removes it if you want"