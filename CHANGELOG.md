# Changelog

Each release records its user-visible changes here. GitHub release notes are
extracted from the matching dated section by `scripts/release-notes.sh`.
Version policy and the release checklist are in [CONTRIBUTING.md](CONTRIBUTING.md).

## [Unreleased]

## [2.2.0] - 2026-10-28

### Added

- `shrink info` without files shows the running binary's size, version, platform and recorded queue compression stats. Passing files still shows media metadata.

## [2.1.0] - 2026-10-28

### Changed

- A clearer `shrink update` display with current/latest versions, binary size and percentage change, short release highlights, the full changelog link, and an explicit completion message.

## [2.0.0] - 2026-10-28

### Added

- A persistent background queue (`que`/`queue`) with manual restart, parallel jobs, stop/retry/cancel controls, and no boot-time service.
- Saved queue CPU/concurrency settings, per-job CPU overrides, and `--next`/exclusive `--boost` scheduling.
- Local queue compression stats that discard completed filenames and retain only aggregate counts, bytes, timing and file types.

### Changed

- Releases now verify, build and publish locally using the existing Go cache. GitHub Actions remains available for pull-request checks and manual release recovery.
- Interrupted releases can be resumed; all archives upload into a draft before a complete release is published.

### Fixed

- Late queue cancellation discarding completion records and statistics for an already published output.
- Queue cancellation killing the encoder helper before it can stop ffmpeg on Windows.

### Compatibility notes

- Queue processing defaults to a 20% CPU allowance and one active job. Native CPU caps depend on platform support; the thread-based fallback, including on macOS, is approximate.
- Queued jobs do not restart automatically after reboot. Run `shrink que start`; interrupted jobs encode again from the beginning.

## [1.0.0] - 2026-10-27

### Added

- A shared job planner for previews, output names, folder reruns and encoding.
- Command and format registries to make new tools and formats easier to add.
- Per-invocation application state and a testable CLI boundary.
- Regression tests for output safety, cancellation, format changes and stream preservation.
- A version policy, maintained changelog and release tooling that requires matching release notes.

### Changed

- Encodes write to temporary files, verify the result, and publish only after success.
- Folder runs validate existing outputs against the requested codec, container, size and duration.
- Rescaling uses the shared quality settings and honours hardware-encoder preference.
- Video conversion preserves all audio tracks; original deletion checks for lost auxiliary streams.
- CLI parsing supports `--help`, literal filenames after `--`, and command-specific flag validation.
- Release tags must pass the shared CI checks before publishing. Both installers require SHA-256 verification.

### Fixed

- Default folder reruns repeatedly encoding originals and creating duplicate outputs.
- Format requests being skipped for earlier shrink outputs, including MP4-to-MKV conversion.
- Infinite or NaN speed factors passing validation and potentially hanging.
- Failed encodes leaving partial files that later runs treated as completed outputs.
- Cancellation leaving partial outputs created after the cleanup check.
- Failed overwrites replacing existing destinations, and concurrent output publication clobbering files.
- The special rescale path lowering audio quality and bypassing hardware preference.
- Animated image compression flattening animations and permitting deletion of their originals.

### Compatibility notes

- Animated images are now rejected with a clear error and their originals retained. Still images remain supported.
- Flags that do not apply to a command now produce an error instead of being silently ignored.
- In machine output, `kind` consistently describes the output media kind, including extracted audio.

Earlier releases retain their notes in the [GitHub release history](https://github.com/matthewsawatzky/shrink/releases).
