# Shrink - a media shrinking tool
`We are like a dwarf perched on the shoulders of a giant` ~ William of Conches

> ffmpeg is the background tool, thank you FFmpeg Devs



## About:
'Shrink' is a CLI tool that allows for easier media conversion and compression: short commands to encode and manipulate media, whether that be Videos, Images, or Audio.

Shrink relies on FFmpeg in the background to do the compression and encoding, and can only do what FFmpeg can do.

Shrink supports:
- Compressing video, image, and audio files with three preset levels (compress, spacesaver, neversee)
- Turning videos (with audio) into audio files
- Grabbing a frame from a video as an image
- Changing the resolution on videos and images (scaling = lossy; smaller inputs are never upscaled)
- Compressing or standardizing all media inside a folder (recursive with "-r", parallel with "-j")
- Better* 3rd party CLI support and Agent handling when using machine options ("-m" flag)

## Requirements:
- ffmpeg and ffprobe on your PATH
- A full FFmpeg build for every output format - stripped-down builds are missing encoders like webp and vorbis (`brew install ffmpeg` gets everything)

## Install:
```bash
curl -fsSL https://raw.githubusercontent.com/matthewsawatzky/shrink/main/install.sh | sh
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
| `<video format>` | mp4, mkv, webm, mov, avi |
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
| `-V`, `--version` | print the version and exit |

### Exit codes:
| Code | Meaning |
|------|---------|
| 0 | success |
| 1 | error (bad target, failed encode, no media found) |
| 130 | cancelled with Ctrl+C (partial files are cleaned up) |

*better than it could have been... idk what to say

## Releasing (maintainer):
```bash
./release.sh v0.1.0 "release notes here"
```
That tags, pushes, cross-builds (macOS/Linux, arm64/amd64) and publishes the release with checksums - `install.sh` then picks it up automatically.