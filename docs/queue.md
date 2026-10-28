# Background queue

The queue is a background process, not a permanently installed service. It exits when no runnable jobs remain. Nothing starts automatically at boot. After a restart, use `shrink que start` to process unfinished jobs again.

```bash
shrink que clip.mov 720p spacesaver
shrink que picture.heic webp
shrink que status
shrink que stop
shrink que start
```

Adding a file normally starts the worker if it is not running. After an intentional `que stop`, additions stay queued until `que start`. `stop` cancels active encodes, waits for their cleanup in the worker, and retains unfinished jobs. The command returns after requesting the stop; `que status` shows when the worker has exited.

`queue` is an alias for `que`; `settings` is an alias for `setting`.

## Targets and files

A queued job saves the ordinary media targets and the `-f`, `-o`, and `-d` flags. It references the original file by absolute path; it does not copy or move the input. Only individual regular media files are accepted in this version. Folder mode remains a foreground command.

The worker checks the queued file's size and modification time before starting. Missing or changed inputs fail safely. Restore an unchanged original and retry, or cancel and enqueue the replacement file. Failed jobs do not block the remaining queue and do not retry indefinitely.

```bash
shrink que retry j-000001
shrink que retry all
shrink que cancel j-000002
shrink que clear
```

`retry` applies to failed jobs. `cancel` removes a waiting/failed job's paths immediately, or requests cancellation of a running job. `clear` removes queue records after the worker has stopped; it preserves aggregate stats and settings.

If a running job has already finished publishing its output when cancellation arrives, it is recorded as completed and included in statistics. Cancellation does not remove a successfully published file.

## Interrupted work

An unfinished encode always starts from the beginning. Shrink never resumes a partial ffmpeg stream. Each attempt has a private temporary output, and recovery cleans the recorded partial before retrying. A helper watches the worker connection and shuts down its ffmpeg process if the worker is killed.

Completed jobs and statistics are committed together. Completed jobs are not rerun. Original deletion is deferred until completion is durably recorded, and the existing smaller-output/stream-preservation checks still apply. If another queued job needs the same input, the original is retained. A crash during final publication can leave a finished but unacknowledged output; recovery preserves it and uses a collision suffix when redoing the job, rather than risk deleting someone else's file.

## CPU budget and parallelism

Defaults are **20% CPU** and **one active job**. These settings affect queued jobs only.

```bash
shrink setting --cpu 20% --jobs 2
shrink que clip.mov --cpu 70%
shrink que start --cpu 40% --jobs 2
```

`setting` saves defaults. A job's explicit `--cpu` overrides the default or worker-run value for that job. `que start` overrides settings only for that worker's lifetime. Jobs without an explicit override use the saved default when dispatched. Changes to defaults affect later jobs; they do not interrupt running encodes.

The budget is shared across active jobs, not granted in full to each job. Jobs with different CPU budgets wait for the current group to finish before changing the worker's budget. The scheduler reduces concurrency when the approximate thread budget cannot give each job at least one thread. The same input cannot be processed by two jobs simultaneously.

100% means the available machine CPU capacity, not one CPU core. Logical CPUs are used for thread calculations. On Linux, inherited cgroup quotas also constrain the capacity calculation.

- Linux uses a native cgroup v2 cap if the process has a writable delegated CPU controller. No privilege escalation or system service is installed.
- Windows uses a CPU-rate-controlled Job Object if the environment permits it.
- Otherwise, including on macOS, the fallback limits decoder, encoder and filter thread pools and lowers ordinary background encoder priority. **This is approximate, not a hard percentage cap.** A minimum of one thread can exceed 20% on a small machine, and encoder support varies.

`que status` reports the active CPU-control mode and thread budget. CPU limits do not cap GPU activity when `-f` selects a hardware encoder.

## Priority and boost

```bash
shrink que important.mov 1080p --next --boost
```

`--next` promotes a job ahead of ordinary waiting jobs without interrupting current work. Priority jobs preserve their enqueue order relative to one another.

`--boost` gives that job a 100% CPU allowance, runs it alone, and avoids the normal background priority reduction. Existing jobs finish first; the scheduler holds back other waiting jobs while the boosted job waits for them to drain. When the boosted job completes or fails, normal CPU settings and parallelism return. `--boost` cannot be combined with `--cpu`.

## Stats and privacy

```bash
shrink stats
shrink info
shrink stats -m
shrink que status -m
shrink setting -m
```

Stats currently cover queue processing. Ordinary foreground commands do not write usage history. Counters include completed encodes, same-kind conversions/compressions, derived audio/frame outputs, no-op skips, failed attempts, cumulative input/output bytes, processing time, and per-input-extension totals. Net byte reduction counts only same-kind outputs; audio extraction and frame grabs have separate counts and do not inflate compression savings. Net reduction can be negative when outputs grow; it is not a measurement of disk space actually freed, since originals are usually retained. Parallel job times are summed, not reported as queue wall time.

Paths and target settings are stored only for unfinished jobs, including failed jobs awaiting attention. On completion or cancellation those fields are removed. The most recent 100 finished job records retain anonymous IDs, state, attempt count and timestamps. Aggregate totals remain across all completed work. Raw ffmpeg errors and filename-bearing worker logs are not persisted.

Queue state is stored in a private application-data directory:

| OS | Default directory |
|---|---|
| macOS | `~/Library/Application Support/shrink` |
| Linux | `$XDG_DATA_HOME/shrink`, or `~/.local/share/shrink` |
| Windows | `%LOCALAPPDATA%\shrink` |

`SHRINK_DATA_DIR` overrides the directory, primarily for isolated tests or separate queues. The directory contains a versioned `queue.json` with settings, jobs and aggregates, plus OS-managed lock files. Updates use a lock, an atomic replacement and file synchronization; idle worker polls do not rewrite unchanged data. A corrupt or unsupported state file is rejected without resetting it.

## Testing

Unit tests cover locks, atomic concurrent updates, scheduling, shared budgets, exclusive boosts, restored limits, recovery, cancellation and filename removal. Integration tests build the CLI and run detached workers with real ffmpeg media, including simultaneous encoders and a forcibly killed worker. They use temporary data directories and never touch the user's queue. Controlled POSIX process tests skip on Windows; native Windows and Linux CPU enforcement need platform-specific runtime validation. All six supported targets are cross-compiled separately before delivery.
