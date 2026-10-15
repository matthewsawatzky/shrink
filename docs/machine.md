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
| `kind` | string | `video`, `image`, or `audio` |
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
