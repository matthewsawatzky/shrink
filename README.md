# Shrink - a media shrinking tool
`We are like a dwarf perched on the shoulders of a giant` ~ William of Conches

[![CI](https://github.com/matthewsawatzky/shrink/actions/workflows/ci.yml/badge.svg)](https://github.com/matthewsawatzky/shrink/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/matthewsawatzky/shrink?logo=github)](https://github.com/matthewsawatzky/shrink/releases)
[![license](https://img.shields.io/github/license/matthewsawatzky/shrink)](LICENSE)
[![Go](https://img.shields.io/github/go-version/go/matthewsawatzky/shrink?logo=go)](go.mod)

> ffmpeg is the background tool, thank you FFmpeg Devs



## About:
'Shrink' is a CLI tool that allows for easier media conversion and compression: short commands to encode and manipulate media, whether that be Videos, Images, or Audio.

Shrink relies on FFmpeg in the background to do the compression and encoding, and can only do what FFmpeg can do.

Shrink supports:
- Compressing video, image, and audio files with three preset levels (compress, spacesaver, neversee)
- Modern codecs: hevc and av1 targets (much smaller files at the same quality), and hardware encoders with "-f" (VideoToolbox on macOS - much faster)
- Turning videos (with audio) into audio files
- Grabbing a frame from a video as an image
- Changing the resolution on videos and images (scaling = lossy; smaller inputs are never upscaled)
- Compressing or standardizing all media inside a folder (recursive with "-r", parallel with "-j", dry-run preview with "-n")
- Incremental folder runs: files that were already converted are skipped, so re-running never double-compresses ("-o" redoes them)
- Better* 3rd party CLI support and Agent handling when using machine options ("-m" flag)

## Requirements:
- ffmpeg and ffprobe on your PATH
- A full FFmpeg build for every output format - stripped-down builds are missing encoders like webp and vorbis (`brew install ffmpeg` gets everything)

## Install:
macOS / Linux (arm64 + amd64):
```bash
curl -fsSL https://raw.githubusercontent.com/matthewsawatzky/shrink/main/install.sh | sh
```
Homebrew:
```bash
brew tap matthewsawatzky/tap
brew trust matthewsawatzky/tap    # required once by modern Homebrew
brew install shrink
```
Go:
```bash
go install github.com/matthewsawatzky/shrink@latest
```
Windows (10+, amd64/arm64):
```powershell
irm https://raw.githubusercontent.com/matthewsawatzky/shrink/main/install.ps1 | iex
```
Or grab a binary from the [releases page](https://github.com/matthewsawatzky/shrink/releases) and put it on your PATH.

## CLI:

Usage:
```bash
shrink <input> [targets...]                      # one file
shrink folder <folder> [formats...] [level]      # every file in a folder
```
Targets can be given in any order; flags can appear anywhere.

Example:
```bash
$ shrink input.MOV 1080p mp4
-> input-1080p.mp4
```
Outputs are written next to the input, and existing files are never overwritten (a counter is appended instead).

### Targets:
| Types | Options |
|-------|---------|
| `<resolution>` | 340p, 480p, 720p, 1080p, 1440p, 4k |
| `<video format>` | mp4, mkv, webm, mov, avi, hevc, av1 |
| `<image format>` | jpeg, png, webp, gif |
| `<audio format>` | mp3, aac, m4a, opus, ogg, wav, flac |
| `<compression level>` | compress, spacesaver, neversee |

In folder mode, give one format per media kind (e.g. `shrink folder ~/Media jpeg mp4 mp3 spacesaver`); each applies to its matching files only.

### Flags:
| Flags | Descriptions |
|-------|--------------|
| `-m`, `--machine` | outputs JSON (for scripts/LLMs) |
| `-v`, `--verbose` | step timings, and ms and KB everywhere |
| `-r`, `--recursive` | with a folder, also scan subfolders |
| `-j`, `--jobs N` | folder mode: encode N files at once (default 2) |
| `-f`, `--fast` | prefer hardware video encoders where available |
| `-n`, `--dry-run` | show what would happen without encoding anything |
| `-o`, `--overwrite` | redo outputs that already exist |
| `-c`, `--completions` | print shell completions (zsh or bash) and exit |
| `-V`, `--version` | print the version and exit |

### Shell completions:
```bash
shrink --completions zsh > ~/.zfunc/_shrink    # then "fpath=(~/.zfunc $fpath)" before compinit
shrink --completions bash > /usr/local/etc/bash_completion.d/shrink
```

### Exit codes:
| Code | Meaning |
|------|---------|
| 0 | success |
| 1 | error (bad target, failed encode, no media found) |
| 130 | cancelled with Ctrl+C (partial files are cleaned up) |

*better than it could have been... idk what to say

## Releasing (maintainer):
```bash
git tag v0.2.0 && git push --tags     # CI builds + publishes the release
```
GitHub Actions cross-builds every platform (macOS/Linux/Windows, arm64/amd64), attaches checksums and publishes the release - `install.sh` picks it up immediately. (`./release.sh v0.2.0 "notes"` does the same locally.)