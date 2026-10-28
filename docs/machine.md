# Machine-readable output (`-m` / `--machine`)

With `-m`, shrink replaces all human-facing output (progress bar, summaries, errors) with a single JSON document on stdout, so scripts and agents can parse one thing. Exit codes are unchanged: `0` success, `1` failure, `130` cancelled.

```bash
shrink clip.mov 1080p mp4 -m | jq '.files[0].savings_percent'
shrink folder ~/Media jpeg mp4 mp3 -m -j 4 | jq '.summary'
```

## Example: one file

```json
{
  "version": "v0.3.0",
  "files": [
    {
      "input": "clip.mov",
      "output": "clip-1080p.mp4",
      "kind": "video",
      "operation": "compress",
      "input_bytes": 432537600,
      "output_bytes": 39976550,
      "savings_percent": 90.76,
      "took_ms": 42123,
      "steps": [
        { "name": "inspect input", "ms": 12 },
        { "name": "encode (compressing + scaling)", "ms": 42080 },
        { "name": "stat output", "ms": 0 }
      ]
    }
  ],
  "summary": {
    "processed": 1,
    "failed": 0,
    "skipped": 0,
    "input_bytes": 432537600,
    "output_bytes": 39976550,
    "savings_percent": 90.76,
    "took_ms": 42205
  }
}
```

## Document fields

| Field | Type | Notes |
|-------|------|-------|
| `version` | string | e.g. `v0.3.0`; `dev` for local builds |
| `dry_run` | bool | present only for `-n` runs: entries are what *would* happen |
| `note` | string | present when a run did nothing (e.g. already-compressed input) |
| `folder` | string | present in folder mode |
| `recursive` | bool | present in folder mode when `-r` was given |
| `files` | array | one entry per processed (or, with `-n`, planned) file; `[]` when none |
| `failed` | array | `{ "input", "error" }` per failed file |
| `skipped` | object | folder mode: files passed over because their kind had no format target, keyed by kind |
| `already_done` | int | files skipped because their output already existed (or they already look converted) |
| `summary` | object | see below |

## `files[]` entry

| Field | Type | Notes |
|-------|------|-------|
| `input` | string | input path |
| `output` | string | output path |
| `kind` | string | output media kind: `video`, `image`, or `audio` |
| `operation` | string | `compress`, `rescale`, `extract_audio`, or `grab_frame` |
| `input_bytes` / `output_bytes` | int | sizes used for the savings figure |
| `savings_percent` | float | `(1 - out/in) * 100`, two decimals |
| `took_ms` | int | wall time for this file |
| `warning` | string | e.g. output ended up larger than the input |
| `deleted_input` | bool | `-d` removed the original |
| `steps` | array | `{ "name", "ms" }` per stage (always included in machine mode) |

## `summary`

| Field | Type | Notes |
|-------|------|-------|
| `processed` | int | files compressed successfully |
| `failed` | int | files that failed (`failed[]` has details) |
| `skipped` | int | files skipped because their kind had no target, plus `already_done` |
| `deleted` | int | originals removed by `-d` (omitted when zero) |
| `input_bytes` / `output_bytes` | int | totals across `files[]` |
| `savings_percent` | float | total savings |
| `took_ms` | int | wall time for the whole run |

## Errors

Errors for bad input (unknown target, unsupported file type, conflicting targets, single-file failures) come back as a JSON document with exit code `1`:

```json
{
  "error": "unknown target: pngg",
  "valid_targets": ["340p", "480p", "720p", "1080p", "1440p", "4k", "compress", "spacesaver", "neversee", "av1", "avi", "hevc", "mkv", "mov", "mp4", "webm", "gif", "jpeg", "png", "webp", "flac", "m4a", "mp3", "ogg", "opus", "wav"]
}
```

| Field | Notes |
|-------|-------|
| `error` | human-readable message |
| `input` | offending input, when relevant |
| `valid_targets` | valid targets, for unknown-target errors |
| `valid_inputs` | read-supported extensions, for unsupported-type errors |
| `suggestions` | suggested commands, when one file was asked for two formats |

Folder-mode failures keep the normal document shape: the run completes, failures land in `failed[]`, and the process exits `1` if any file failed.

`shrink make -m` reports the generated file like a normal run, with `"input": "(generated)"` and `"operation": "make"`.

## Other commands

`shrink info -m` without files returns an object with `version`, `platform`, `binary_size_bytes`, `stats_scope` (currently `"queue"`), and `stats`. The stats are the same aggregate counters returned by `shrink stats -m`; foreground runs are not tracked. It does not need ffmpeg or ffprobe. With files, `shrink info <file>... -m` continues to return an array of media metadata objects.

`shrink doctor -m` and `shrink update -m` emit their own documents instead of the run document.

`shrink doctor -m` exits `1` when `ok` is `false` (ffmpeg or ffprobe missing). `encoders` covers every encoder a target depends on; `missing` lists the target groups that lack one:

```json
{
  "version": "v0.6.0",
  "platform": "darwin/arm64",
  "ffmpeg": { "available": true, "version": "ffmpeg version 7.1 ..." },
  "ffprobe": { "available": true, "version": "ffprobe version 7.1 ..." },
  "encoders": {
    "aac": true, "flac": true, "gif": true, "libmp3lame": true,
    "libopus": true, "libsvtav1": true, "libvorbis": false,
    "libvpx-vp9": true, "libwebp": false, "libx264": true,
    "libx265": true, "mjpeg": true, "pcm_s16le": true, "png": true
  },
  "missing": ["webp", "ogg"],
  "hardware": { "available": true, "note": "VideoToolbox - -f encodes on the GPU" },
  "ok": true
}
```

`shrink update -m` reports what happened (or would happen, with `-n`):

```json
{ "version": "v0.6.0", "updated": true, "updated_from": "v0.5.0", "updated_to": "v0.6.0" }
```

`updated` is always present: it stays `false` when already current or Homebrew-managed (a `note` then explains why), and a dry run sets `updated_to` while `updated` stays `false`.


## Queue commands

`que status -m` returns `running`, the queue schema `version` (currently 1), `paused`, saved `settings`, `worker` resource information, `jobs`, and anonymous `stats`. Unfinished jobs include their paths and targets; completed/cancelled records omit those fields. `setting -m` returns `cpu_percent` and `jobs`. `stats -m` returns queue-only aggregate counters and a `file_types` map keyed by input extension. See [queue.md](queue.md) for CPU precedence, restart behaviour and privacy.

Enqueue responses include `id` and `queued`; if the job was saved but worker startup failed, `worker_error` explains it. `que start`, `stop`, `retry`, `cancel`, and `clear` return a JSON acknowledgement. Failed jobs remain in the queue with a generic note; raw ffmpeg messages are not stored.
