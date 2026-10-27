# Contributing

Thanks for wanting to help. This covers building, testing, and releasing shrink.

## Prerequisites

- Go (the version pinned in [go.mod](go.mod))
- `ffmpeg` and `ffprobe` on your `PATH` for manual runs and integration tests. Integration tests skip when these binaries are unavailable. CI installs them and runs the complete suite.

## Build

```bash
./build.sh          # local build -> ./shrink, version "dev"
./build.sh v0.2.0   # cross-compiled release archives in dist/
```

## Test

```bash
go test -race ./...
```

CI runs the same on every push and pull request ([ci.yml](.github/workflows/ci.yml)):

```bash
gofmt -l .       # must print nothing
go vet ./...
go test -race ./...
go build ./...
```

## Code layout

| File | What lives there |
|------|------------------|
| `app.go` | per-invocation state, injected output writers, context, exit boundary |
| `cli.go` | flag parsing and command registry used by dispatch, usage and completion |
| `main.go` | target parsing/validation, resolutions, levels, media command coordination |
| `formats.go` | output encoders, probe codecs, containers, aliases and audio dependencies |
| `plan.go` | shared media job planning, request matching and execution |
| `media.go` | ffprobe metadata, codec helpers, output naming and result/deletion policy |
| `batch.go` | folder mode: scanning, worklist, parallel workers, summaries |
| `doctor.go` | `shrink doctor`, fail-fast dependency check, install hints |
| `make.go` | `shrink make`: test-media generation and its target vocabulary |
| `update.go` | `shrink update`: release lookup, checksum verify, binary swap |
| `machine.go` | the `-m` JSON document |
| `progress.go` | transactional subprocess runner, verification and progress rendering |
| `style.go` | ANSI styling (auto-disabled for pipes, `NO_COLOR`, `TERM=dumb`) |
| `verbose.go` | `-v` step timings |
| `completions.go` | zsh/bash completion scripts |
| `install.sh`, `install.ps1`, `uninstall.sh`, `uninstall.ps1` | installer/uninstaller scripts shipped with releases |

The format metadata in `formats.go` drives parsing, completion, doctor checks, and request matching. Add commands in the `commands` registry in `cli.go`. See [architecture.md](docs/architecture.md) for the job lifecycle and extension guide.

## Version policy

Use `MAJOR.MINOR.PATCH` and Git tags such as `v1.0.0`. Choose the highest applicable change category in the release:

| Change | Bump | Example |
|---|---|---|
| Major change or substantial addition: a new major tool, a foundation redesign, or a breaking change | Major; reset minor and patch to zero | `8.4.1` → `9.0.0` |
| Smaller feature, quality-of-life addition, or intentional change to how an existing function works | Minor; reset patch to zero | `8.4.1` → `8.5.0` |
| Bug fix, malfunction correction, or maintenance that restores intended behaviour without adding a feature | Patch | `8.4.1` → `8.4.2` |

These rules apply before `1.0.0` too. A substantial addition can warrant a major release even without a breaking change. Documentation-only fixes use a patch when a release is needed. Fixes can travel alongside a minor or major release; do not bump each component separately.

## Changelog and contribution workflow

1. Make a focused change and add a regression test for corrected behaviour.
2. Add a short user-facing bullet under `## [Unreleased]` in [CHANGELOG.md](CHANGELOG.md). Use only the sections needed: `Added`, `Changed`, `Fixed`, `Removed`, and `Compatibility notes`.
3. Explain what users gain or what now works. Include migration advice for changed behaviour; avoid copying commit messages or listing internal edits with no user impact.
4. Run formatting, vet and tests before submitting the change. CI also runs the shell release-tool tests as part of `go test`.

## Releasing

Release from an up-to-date `main` branch with remote tags fetched. GitHub Actions is the sole publisher; the local script validates and pushes the commit and tag together.

1. Fetch current tags: `git fetch origin --tags`.
2. Choose `major`, `minor` or `patch` using the table above. Preview the next version with `./release.sh --next major` (or `minor`/`patch`).
3. Move the unreleased bullets into a dated section such as `## [1.1.0] - 2026-11-02`. Keep an empty `## [Unreleased]` section at the top. Review compatibility notes and the README.
4. Preview the exact public release notes: `./scripts/release-notes.sh v1.1.0`.
5. Commit the code, documentation and changelog. Then run `./release.sh minor`, or give the exact next version: `./release.sh v1.1.0`.
6. Wait for the Release workflow to pass and confirm that the published release contains all six platform archives and the checksums file.

The script rejects skipped version numbers, malformed versions, missing/empty changelog entries, a dirty checkout, and releases outside `main`. It runs vet and race-enabled tests before creating a tag, then pushes `main` and that tag atomically. If a push fails, fix the cause and push the existing tag; do not create a different release tag for the same intended version.

The workflow extracts notes from the committed changelog using the same helper, verifies the tag, runs CI, builds macOS/Linux/Windows for arm64 and amd64, and publishes the archives with checksums. `install.sh` and `shrink update` pick up the latest release immediately. Do not publish a second release manually with `gh release create`.
