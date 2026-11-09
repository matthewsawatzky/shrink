# shrink

**Media compression and conversion with one short command.**

A friendly wrapper around [ffmpeg](https://ffmpeg.org): sane defaults, three compression levels, modern codecs, folder mode, and a background queue for long jobs.

[![CI](https://github.com/matthewsawatzky/shrink/actions/workflows/ci.yml/badge.svg)](https://github.com/matthewsawatzky/shrink/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/matthewsawatzky/shrink?logo=github)](https://github.com/matthewsawatzky/shrink/releases)
[![license](https://img.shields.io/github/license/matthewsawatzky/shrink)](LICENSE)
[![Go](https://img.shields.io/github/go-version/go/matthewsawatzky/shrink?logo=go)](go.mod)

## Quick start

```bash
shrink clip.mov 1080p mp4          # scale to 1080p and convert
shrink photo.heic webp spacesaver  # convert and compress hard
shrink song.wav mp3                # convert audio
shrink folder ~/Media -r           # compress a whole folder tree
```

```console
$ shrink clip.mov 1080p mp4
✓ clip-1080p.mp4  (412.51MB → 38.12MB)  ↓ 90%  took 42s
```

Outputs are written next to the input, and your originals are never touched (unless you ask for it with `-d`).

## Install

**macOS / Linux** (arm64 + amd64):

```bash
curl -fsSL https://raw.githubusercontent.com/matthewsawatzky/shrink/main/scripts/install.sh | sh
```

**Homebrew:**

```bash
brew tap matthewsawatzky/tap
brew trust matthewsawatzky/tap    # required once by modern Homebrew
brew install shrink
```

**Go:**

```bash
go install github.com/matthewsawatzky/shrink/cmd/shrink@latest
```

**Windows 10+** (experimental — builds are cross-compiled and not yet tested on real hardware; please [open an issue](https://github.com/matthewsawatzky/shrink/issues) if anything breaks):

```powershell
irm https://raw.githubusercontent.com/matthewsawatzky/shrink/main/scripts/install.ps1 | iex
```

Every release is also available as a plain binary on the [releases page](https://github.com/matthewsawatzky/shrink/releases). The installers also offer to install ffmpeg if it is missing.

Requires `ffmpeg` and `ffprobe` on your `PATH`. Use a full ffmpeg build — stripped-down builds are missing encoders like webp and vorbis (`brew install ffmpeg` gets everything).

### Updating / uninstalling

| Installed via | Update with |
|---------------|-------------|
| curl installer or manual binary | `shrink update` |
| Homebrew | `brew upgrade shrink` |
| Go | `go install github.com/matthewsawatzky/shrink/cmd/shrink@latest` |

`shrink update` verifies the release checksum and swaps the binary in place. To remove shrink, use `brew uninstall shrink` or:

```bash
curl -fsSL https://raw.githubusercontent.com/matthewsawatzky/shrink/main/scripts/uninstall.sh | sh
```

## Crash course

The shape is always `shrink <input> [targets...]`. Targets are plain words, and order doesn't matter:

```bash
shrink clip.mov 1080p mp4          # resize + convert
shrink clip.mp4 mp3                # pull out the audio
shrink photo.heic webp spacesaver  # convert + compress hard
```

There are four kinds of target:

- **resolution:** `340p` `480p` `720p` `1080p` `1440p` `4k`
- **format:** video `mp4` `mkv` `webm` `mov` `avi` `hevc` `av1`; image `jpeg` `png` `webp` `gif`; audio `mp3` `aac` `opus` `ogg` `wav` `flac`
- **speed:** `x2`, `x5`, `x0.1` (pitch stays the same)
- **level:** `compress`, `spacesaver`, `neversee`

**Compression is opt-in.** A bare `shrink file` compresses at the `compress` level. A specific operation (scale, convert, extract) runs near-lossless on its own — add a level to compress on top:

```bash
shrink cam.mp4 720p               # downscale only
shrink cam.mp4 720p spacesaver    # downscale and compress hard
```

**Outputs sit next to the input** and never overwrite existing files. The name records what happened: `clip-1080p.mp4`, `clip-audio.mp3`, `clip-x5.mp4`. Pass `-d` to delete the original once a smaller, verified replacement exists.

**Folders and long jobs are built in:**

```bash
shrink folder ~/Media jpeg mp4 mp3 -r -j 4   # recurse, four files at a time
shrink que clip.mov 720p spacesaver          # hand it to the background queue
```

Other commands: `info` (stats and media details), `doctor` (checks ffmpeg and encoders), `make` (generates test media), and `update`.

That's the whole model. Full targets, flags, naming rules and edge cases are in the **[reference](docs/reference.md)**.

## Documentation

| Guide | What it covers |
|-------|----------------|
| [Reference](docs/reference.md) | every command, target, flag and exit code |
| [Architecture](docs/architecture.md) | application and job lifecycle, plus how to extend shrink |
| [Machine-readable output](docs/machine.md) | the `-m` JSON schemas for every command |
| [Background queue](docs/queue.md) | CPU limits, recovery, privacy and all queue commands |
| [Changelog](docs/CHANGELOG.md) | user-visible changes in each release |
| [Contributing](docs/CONTRIBUTING.md) | building, testing, versioning and releasing |

## Credits

All encoding is done by [ffmpeg](https://ffmpeg.org) (and ffprobe) — shrink is just the friendly wrapper. Thank you, FFmpeg devs.

> *"We are like a dwarf perched on the shoulders of a giant."* — William of Conches

## License

[MIT](LICENSE)
