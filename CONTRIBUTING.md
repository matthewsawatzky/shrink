# Contributing

Thanks for wanting to help. This covers building, testing, and releasing shrink.

## Prerequisites

- Go (the version pinned in [go.mod](go.mod))
- `ffmpeg` and `ffprobe` on your `PATH` for manual runs — the unit tests don't need them

## Build

```bash
./build.sh          # local build -> ./shrink, version "dev"
./build.sh v0.2.0   # cross-compiled release archives in dist/
```

## Test

```bash
go test ./...
```

CI runs the same on every push and pull request ([ci.yml](.github/workflows/ci.yml)):

```bash
gofmt -l .       # must print nothing
go vet ./...
go test ./...
go build ./...
```

## Code layout

| File | What lives there |
|------|------------------|
| `main.go` | CLI parsing, target tables (formats, resolutions, levels), routing |
| `media.go` | ffprobe metadata, ffmpeg command lines, output naming |
| `batch.go` | folder mode: scanning, worklist, parallel workers, summaries |
| `doctor.go` | `shrink doctor`, fail-fast dependency check, install hints |
| `make.go` | `shrink make`: test-media generation and its target vocabulary |
| `update.go` | `shrink update`: release lookup, checksum verify, binary swap |
| `machine.go` | the `-m` JSON document |
| `progress.go` | live progress bar, shared batch line, Ctrl+C cleanup |
| `style.go` | ANSI styling (auto-disabled for pipes, `NO_COLOR`, `TERM=dumb`) |
| `verbose.go` | `-v` step timings |
| `completions.go` | zsh/bash completion scripts |
| `install.sh`, `install.ps1`, `uninstall.sh`, `uninstall.ps1` | installer/uninstaller scripts shipped with releases |

The format/resolution/level tables in `main.go` drive parsing, completions, and error messages, so adding a target there wires it up everywhere.

## Releasing

Pushing a `v*` tag triggers CI to build every platform (macOS/Linux/Windows, arm64/amd64), attach checksums, and publish the release:

```bash
git tag v0.2.0 && git push --tags
```

To do the same locally (requires a clean tree and the [GitHub CLI](https://cli.github.com) logged in):

```bash
./release.sh v0.2.0 "release notes"
```

`install.sh` picks up the latest release immediately.
