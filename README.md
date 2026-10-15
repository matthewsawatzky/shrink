# shrink

**Media compression and conversion with one short command.**
A friendly wrapper around [ffmpeg](https://ffmpeg.org): sane defaults, three compression levels, modern codecs, and a folder mode for whole libraries.

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

## Requirements

- `ffmpeg` and `ffprobe` on your `PATH`
- A full ffmpeg build — stripped-down builds are missing encoders like webp and vorbis (`brew install ffmpeg` gets everything)

## Install

**macOS / Linux** (arm64 + amd64):

```bash
curl -fsSL https://raw.githubusercontent.com/matthewsawatzky/shrink/main/install.sh | sh
```

**Homebrew:**

```bash
brew tap matthewsawatzky/tap
brew trust matthewsawatzky/tap    # required once by modern Homebrew
brew install shrink
```

**Go:**

```bash
go install github.com/matthewsawatzky/shrink@latest
```

**Windows 10+** (amd64/arm64) — *experimental: builds are cross-compiled and not yet tested on real hardware; please [open an issue](https://github.com/matthewsawatzky/shrink/issues) if anything breaks.*

```powershell
irm https://raw.githubusercontent.com/matthewsawatzky/shrink/main/install.ps1 | iex
```

Every release is also available as a plain binary on the [releases page](https://github.com/matthewsawatzky/shrink/releases). macOS and Linux are the tested platforms.

The installers notice when ffmpeg is missing and offer to install it for you (via brew on macOS, apt/dnf/pacman/apk/zypper on Linux, winget on Windows — with a manual hint when run without a terminal, since `curl | sh` can't prompt).

## Usage

```bash
shrink <input> [target ...]          # one file
shrink folder <folder> [target ...]  # every matching file in a folder
```

Targets can be given in any order, and flags can appear anywhere:

```bash
shrink clip.mov 1080p mp4          # scale to 1080p and convert to mp4
shrink clip.mp4 mp3                # extract the audio track
shrink clip.mp4 png                # grab the first frame as an image
shrink photo.heic jpeg spacesaver  # convert and compress heavily
shrink song.wav flac               # lossless conversion (level ignored)
shrink folder ~/Media jpeg mp4 mp3 spacesaver
```

With no format target, shrink picks each kind's default (`mp4`, `jpeg`, `mp3`) and compresses at the `compress` level.

### Targets

| Type | Values |
|------|--------|
| Resolution | `340p` `480p` `720p` `1080p` `1440p` `4k` |
| Video format | `mp4` `mkv` `webm` `mov` `avi` `hevc` `av1` |
| Image format | `jpeg` (`jpg`) `png` `webp` `gif` |
| Audio format | `mp3` `aac` (`m4a`) `opus` `ogg` `wav` `flac` |
| Compression level | `compress` `spacesaver` `neversee` |

- `hevc` and `av1` outputs are written into `.mp4` containers; `aac` is written as `.m4a`.
- Resolution targets only ever downscale: an input smaller than the target keeps its size.
- `wav` and `flac` are lossless, so they ignore the compression level.

### Compression levels

| Level | Result |
|-------|--------|
| `compress` | decent quality, solid savings (the default) |
| `spacesaver` | noticeably compressed |
| `neversee` | very compressed |

### Output naming

Outputs are written next to the input, and existing files are never overwritten (a `-1` counter is appended on collision). The name records what was done:

| Command | Output |
|---------|--------|
| `shrink clip.mov 1080p mp4` | `clip-1080p.mp4` |
| `shrink clip.mov mp4` | `clip-compressed.mp4` |
| `shrink clip.mp4 mp3` | `clip-audio.mp3` |
| `shrink clip.mp4 png` | `clip-frame.png` |
| `shrink pic.jpg spacesaver` | `pic-spacesaver.jpg` |

### Folder mode

`shrink folder <folder>` compresses every supported file in the folder, in place:

- Give one format per media kind (e.g. `jpeg mp4 mp3`); each applies only to matching files. With no formats, each kind uses its default.
- `-r` walks subfolders too; hidden files and folders are always skipped.
- `-j N` encodes N files at once (default `2`).
- Re-running is safe: files whose output already exists, or that look like earlier shrink output, are skipped. `-o` redoes them.
- `-n` shows exactly what a run would do, without writing anything.
- `-d` deletes each original once a smaller replacement exists (compressed or rescaled same-kind outputs only).

### Supported inputs

| Kind | Extensions |
|------|------------|
| Video | `.mp4` `.mkv` `.webm` `.mov` `.avi` `.m4v` `.mpg` `.mpeg` `.flv` `.wmv` `.ts` |
| Image | `.jpg` `.jpeg` `.png` `.webp` `.gif` `.bmp` `.tiff` `.heic` `.heif` `.avif` |
| Audio | `.mp3` `.aac` `.m4a` `.opus` `.ogg` `.wav` `.flac` `.wma` `.aiff` |

### Flags

| Flag | Description |
|------|-------------|
| `-m`, `--machine` | emit one JSON document on stdout instead of human output ([schema](docs/machine.md)) |
| `-v`, `--verbose` | show per-step timings, and use ms and KB everywhere |
| `-r`, `--recursive` | with a folder, also scan subfolders |
| `-j`, `--jobs N` | folder mode: encode N files at once (default 2) |
| `-f`, `--fast` | prefer hardware video encoders where available (macOS VideoToolbox; x264/hevc targets only) |
| `-n`, `--dry-run` | show what would happen without encoding anything |
| `-o`, `--overwrite` | redo outputs that already exist |
| `-d`, `--delete` | delete the original once a smaller replacement exists (same-kind targets only) |
| `-c`, `--completions SHELL` | print shell completions for `zsh` or `bash` and exit |
| `-V`, `--version` | print the version and exit |

### Exit codes

| Code | Meaning |
|------|---------|
| `0` | success |
| `1` | error — bad target, failed encode, no media found |
| `130` | cancelled with Ctrl+C — partial files are cleaned up |

### Shell completions

```bash
shrink --completions zsh  > ~/.zfunc/_shrink    # add "fpath=(~/.zfunc $fpath)" before compinit
shrink --completions bash > /usr/local/etc/bash_completion.d/shrink
```

## Contributing

Building, testing, and releasing are covered in [CONTRIBUTING.md](CONTRIBUTING.md).

## Credits

All encoding is done by [ffmpeg](https://ffmpeg.org) (and ffprobe) — shrink is just the friendly wrapper. Thank you, FFmpeg devs.

> *"We are like a dwarf perched on the shoulders of a giant."* — William of Conches

## License

[MIT](LICENSE)
