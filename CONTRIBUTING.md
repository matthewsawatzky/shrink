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

CI runs the same on pull requests and manual runs ([ci.yml](.github/workflows/ci.yml)):

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
| `queue_store.go`, `queue_worker.go`, `queue_cli.go` | durable queue data, scheduling, controls and aggregate stats |
| `queue_cpu*.go`, `queue_platform*.go`, `queue_helper.go` | CPU budgets, OS locks, detached workers and encoder lifetime protection |
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

Queue behaviour and isolated integration tests are described in [docs/queue.md](docs/queue.md). Tests use `SHRINK_DATA_DIR` overrides and do not touch the user's queue.

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

Release from an up-to-date `main` branch. Releases run on your machine, reusing the Go build cache instead of waiting for a new GitHub runner. Install Go, ffmpeg/ffprobe, the GitHub CLI (`gh auth login`), and the usual tar/SHA-256 tools first.

1. Fetch current tags: `git fetch origin --tags`. The release command also fetches them before selecting its version.
2. Choose `major`, `minor` or `patch` using the table above. Preview the next version with `./release.sh --next major` (or `minor`/`patch`).
3. Move the unreleased bullets into a dated section such as `## [1.1.0] - 2026-11-02`. Keep an empty `## [Unreleased]` section at the top. Review compatibility notes and the README.
4. Preview the exact public release notes: `./scripts/release-notes.sh v1.1.0`.
5. Commit the code, documentation and changelog. Then run `./release.sh minor`, or give the exact next version: `./release.sh v1.1.0`.
6. The command runs checks and integration tests locally, builds all six platform archives, pushes the commit/tag, and uploads a draft with the changelog and checksums. It publishes only when every upload succeeds, then prints the release URL. No Actions run is required.

The script rejects skipped version numbers, malformed versions, missing/empty changelog entries, a dirty checkout, and releases outside `main`. It requires ffmpeg/ffprobe so integration tests cannot silently skip. It runs formatting, vet and race-enabled tests before building. Your Go cache is reused, so later release builds are faster. `install.sh` and `shrink update` pick up the release as soon as the command finishes.

If a push or upload fails after a tag was created, retry with `./release.sh --resume v1.1.0` from that tag's commit on `main`. This rebuilds and verifies the same release, completes a draft if present, and publishes it after all assets are uploaded. Already-published releases are never overwritten.

## What GitHub Actions still does

- Pull requests run the full verification suite automatically. Superseded runs are cancelled.
- The CI workflow can also be started manually. Normal pushes to `main` and release tags do not trigger duplicate checks or release builds.
- **Release fallback** is a manual recovery path when local publishing is unavailable. Push the intended release tag, then run `gh workflow run release.yml -f tag=v1.1.0`. It checks out and verifies that exact tag before building and publishing with the same helpers. Use it for tags created after the local release tooling was introduced.

Both local and fallback publishing share `scripts/publish-release.sh`, which validates the six archives and their checksums, uploads into a draft, and publishes only once the complete release is present. Keep the changelog and version process the same regardless of where the build runs.
