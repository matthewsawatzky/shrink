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

shrink checks for these up front: if ffmpeg is missing it fails immediately with the install command for your platform, and [`shrink doctor`](#doctor) shows what's installed, which targets are available, and whether hardware encoding works.

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

The installers also notice when ffmpeg is missing and offer to install it for you (brew, apt/dnf/pacman/apk/zypper, or winget), falling back to a printed hint when there's no terminal to prompt on.

### Updating

| Installed via | Update with |
|---------------|-------------|
| curl installer or manual binary | `shrink update` |
| Homebrew | `brew upgrade shrink` |
| Go | `go install github.com/matthewsawatzky/shrink@latest` |

`shrink update` checks GitHub for the latest release, verifies its SHA-256 checksum, and swaps the binary in place — a no-op when already current. Homebrew-managed installs are detected and pointed at `brew upgrade shrink` instead, and local dev builds require `-o` to be replaced.

### Uninstalling

The uninstaller removes the binary, the Homebrew package (if that's how it was installed), and the shell completions. ffmpeg is left alone, since other tools use it.

```bash
curl -fsSL https://raw.githubusercontent.com/matthewsawatzky/shrink/main/uninstall.sh | sh
```

Homebrew installs can also go with `brew uninstall shrink`. On Windows: `irm https://raw.githubusercontent.com/matthewsawatzky/shrink/main/uninstall.ps1 | iex`.

## Usage

```bash
shrink <input> [target ...]          # compress or convert one file
shrink folder <folder> [target ...]  # every matching file in a folder
shrink info <file> ...               # inspect codecs, duration, fps, bitrate
shrink doctor                        # check ffmpeg and encoder availability
shrink make [target ...]             # generate test media
shrink update                        # self-update to the latest release
```

Targets can be given in any order, and flags can appear anywhere:

```bash
shrink clip.mov 1080p mp4          # scale to 1080p and convert to mp4
shrink clip.mp4 mp3                # extract the audio track
shrink clip.mp4 png                # grab the first frame as an image
shrink photo.heic jpeg spacesaver  # convert and compress heavily
shrink song.wav flac               # lossless conversion (level ignored)
shrink clip.mp4 x5                 # 5x faster playback
shrink clip.mp4 x0.1 spacesaver    # 10x slower, heavily compressed
shrink podcast.mp3 x2              # double-speed audio
shrink folder ~/Media jpeg mp4 mp3 spacesaver
```

With no targets at all, shrink compresses at the `compress` level; with targets but no level, only the asked-for operations run (see [When compression happens](#when-compression-happens)).

### Targets

| Type | Values |
|------|--------|
| Resolution | `340p` `480p` `720p` `1080p` `1440p` `4k` |
| Video format | `mp4` `mkv` `webm` `mov` `avi` `hevc` `av1` |
| Image format | `jpeg` (`jpg`) `png` `webp` `gif` |
| Audio format | `mp3` `aac` (`m4a`) `opus` `ogg` `wav` `flac` |
| Speed | `x<number>`, e.g. `x2` `x5` `x0.1` `x2.5` |
| Compression level | `compress` `spacesaver` `neversee` |

- `hevc` and `av1` outputs are written into `.mp4` containers; `aac` is written as `.m4a`.
- Resolution targets only ever downscale: an input smaller than the target keeps its size.
- `wav` and `flac` are lossless, so they ignore the compression level.
- A speed target over 1 speeds up, under 1 slows down: `x5` plays five times faster (a 1-minute video becomes 12 seconds), `x0.1` ten times slower (6 minutes). Video frames are re-timed and audio is stretched with ffmpeg's tempo filter, so pitch stays the same. Speed applies to video and audio only (images cannot be sped), and it combines with format, resolution and level targets.

### When compression happens

Compression only applies when you ask for it: an explicit level (`compress`, `spacesaver`, `neversee`), or a bare `shrink <input>` run with no other targets (which defaults to `compress`). Any specific run — a scale, a speed factor, a format conversion, an audio extraction — performs just that operation at near-lossless quality (an AAC track is even copied through untouched). Add a level to compress on top of an operation:

```bash
shrink cam.mp4 720p               # downscale only, near-lossless
shrink cam.mp4 720p spacesaver    # downscale and compress heavily
```

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
| `shrink clip.mp4 x5` | `clip-x5.mp4` |
| `shrink clip.mp4 x0.1 spacesaver` | `clip-x0.1-spacesaver.mp4` |
| `shrink pic.jpg spacesaver` | `pic-spacesaver.jpg` |

### Folder mode

`shrink folder <folder>` compresses every supported file in the folder, in place:

- Give one format per media kind (e.g. `jpeg mp4 mp3`); each applies only to matching files. With no formats, each kind uses its default.
- `-r` walks subfolders too; hidden files and folders are always skipped.
- `-j N` encodes N files at once (default `2`).
- Re-running is safe: files whose output already exists, or that look like earlier shrink output, are skipped. `-o` redoes them.
- `-n` shows exactly what a run would do, without writing anything.
- `-d` deletes each original once a smaller replacement exists (compressed or rescaled same-kind outputs only).

### Doctor

`shrink doctor` prints a checklist of everything shrink depends on — the core tools, every encoder behind each target, and hardware encoding (`-f`) support. A stripped-down ffmpeg build shows exactly which targets are unavailable, e.g. `webp` needs `libwebp`. Exits `0` when the core tools are present (missing optional encoders are reported, not fatal), `1` when they are not; `-m` emits the same report as JSON.

```console
$ shrink doctor
shrink v0.6.0 doctor - darwin/arm64

  ✓  ffmpeg        ffmpeg version 7.1 Copyright (c) 2000-2025 the FFmpeg developers
  ✓  ffprobe       ffprobe version 7.1 Copyright (c) 2007-2025 the FFmpeg developers

  ✓  libx264       mp4, mkv, mov, avi
  ✓  libx265       hevc
  ✓  libvpx-vp9    webm
  ...
  ✓  hardware (-f) VideoToolbox - -f encodes on the GPU

✓ shrink is ready
```

### Make

`shrink make` generates test media with ffmpeg's lavfi sources — solid colors, SMPTE bars, test patterns, tones, and noise — for trying shrink out or feeding test scripts. Files are named after what's inside and written to the current directory:

```bash
shrink make mp4 red 1080p 60fps 30s    # red-1080p-60fps-30s.mp4
shrink make jpeg teal 720p             # teal-720p.jpg
shrink make wav noise 5s               # noise-440hz-5s.wav
shrink make mp4 bars 30fps 2m neversee # bars-1080p-30fps-2m-neversee.mp4
```

The format picks what gets made (`mp4` by default); everything else tunes it:

| Target | Values |
|--------|--------|
| format | any video, image, or audio format — `mp4` (default), `jpeg`, `wav`, ... |
| content | a named color (`red`, `blue`, `gray`, ...), `bars`, `test`, `noise`; audio takes `sine` (default) or `noise` |
| resolution | any resolution target (default `1080p`) |
| fps | `24fps` `25fps` `30fps` `50fps` `60fps` (default) `120fps` |
| duration | `10s`, `2m`, ... (default `10s`, up to `60m`; video and audio only) |
| frequency | `440hz`, ... (20–20000, audio tone; default `440hz`) |
| level | a compression level, encoded exactly like a normal run |

Videos include a sine soundtrack, so audio paths get tested too. `-n` previews the filename; `-m` reports the result as JSON.

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
| `1` | error — bad target, failed encode, no media found, missing ffmpeg |
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
