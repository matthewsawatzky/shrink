# Project review — 27 October 2026

The main weaknesses are inconsistent job planning, unsafe output lifecycle handling, and process-wide state. These already cause observable bugs. Consolidating the job lifecycle will improve correctness and make new commands and media operations easier to add.

This records the original review. The seven numbered findings have since been addressed; implementation and extension details are in [architecture.md](architecture.md). At the time of this review, application source was not changed. Reproductions used generated media and temporary directories, never the user's media. P1 means fix first because of data loss or a hang; P2 means a functional correctness problem.

## Findings

### 1. P1: Compressing an animated image can delete the animation

Location: `media.go:509`, `media.go:643`.

`handleImage` always passes `-frames:v 1`, even when input and output are GIF. `finishJob` treats that as same-kind compression and permits deleting the original when the result is smaller.

Reproduced with `shrink animation.gif gif -d -m`: a five-frame GIF became a one-frame GIF; the 7,007-byte original was deleted after producing a 2,631-byte output. The command exited successfully.

Detect animation before planning. Preserve the frames for animation-capable outputs, or reject the operation until animation is supported. Treat an intentional first-frame conversion as a derived output that cannot authorize original deletion. Before `-d`, verify the output's required streams/frame properties, not just its size and existence.

### 2. P1: An infinite speed factor hangs before ffmpeg starts

Location: `main.go:348`, `media.go:102`.

`strconv.ParseFloat` accepts infinity and NaN. The `v <= 0` check rejects neither positive infinity nor NaN. With `xinf`, `atempoChain` repeatedly divides infinity by 100 and never terminates, continuously appending filter stages.

Reproduced with a short WAV: `shrink tone.wav xinf -m` required termination after the timeout. `xnan` was also accepted by a dry run. `parseMakeDuration` has the corresponding NaN validation gap.

Reject `math.IsNaN` and `math.IsInf` in numeric parsers. Validate speed once and store the numeric value rather than parsing the raw string repeatedly. Add regression cases for non-finite and extreme finite values.

### 3. P2: Default folder reruns re-encode originals and accumulate outputs

Location: `main.go:505`, `batch.go:164`, `batch.go:221`.

Execution resolves a bare request to the `compress` grade. Planning passes an empty grade into naming. Dry run predicts `tone-compressed.mp3`, execution writes `tone-compress.mp3`, and the next run looks for the wrong path.

Reproduced by running `shrink folder DIR -m` twice on a WAV: the second run encoded the original again into `tone-compress-1.mp3`. Counter-suffixed names also escape `looksConverted`, so later runs can start processing generated outputs too.

Resolve defaults once in a shared job plan. Use its final output decision for preview, skip detection, reservation, and execution. Test repeated runs through the command boundary.

### 4. P2: Failed encodes leave partial files that later count as success

Location: `progress.go:419`, `progress.go:225`, `batch.go:223`.

Normal error paths do not remove partial output. Batch mode considers any existing output completed without checking that it is a valid result.

Reproduced with a controlled ffmpeg substitute that wrote seven bytes and exited 1: the initial batch failed; repeating the same command with real ffmpeg exited 0, processed nothing, and reported the source and broken output as already done.

Encode to a temporary sibling file with the correct format/extension, validate it, then publish it. Remove temporary output on every failure and cancellation path. Existing output alone is insufficient evidence that it satisfies the requested operation.

The in-memory output reservation also protects only workers in one process. Both runners always use `-y`, so another process creating the chosen path between reservation and encoding can have its file overwritten. Publishing needs an explicit collision policy enforced at the filesystem boundary.

### 5. P2: Explicit format requests can be silently skipped

Location: `batch.go:151`, `batch.go:230`, `main.go:479`.

Folder mode skips converted-looking names without considering a requested format change. Single-file video handling compares codecs but ignores containers.

Reproduced both cases:

- `shrink folder DIR webm -n -m` skipped `clip-compressed.mp4`.
- `shrink clip-compressed.mp4 mkv -m` returned a successful no-op and created no MKV, because both formats use H.264.

Output existence checks have a similar limitation: `mp4`, `hevc`, and `av1` share `.mp4`, so one format's output can suppress a different requested codec.

Use a shared request-satisfaction predicate covering container, codec, resolution, speed, and quality intent. Filename suffixes can be hints, but cannot establish that an explicit request is satisfied. Encoder names also differ from probe codec names for several audio formats, making the existing audio comparison unreliable.

### 6. P2: Ctrl+C cleanup can leave partial output behind

Location: `progress.go:55`, `progress.go:89`, `progress.go:180`, `progress.go:416`.

The signal handler deletes outputs before waiting for children to exit. If a child creates its output after that check, the file survives. Processes are also started before registration, leaving a window where the handler cannot see them.

Reproduced using a controlled child that delayed creating its output until after SIGINT: shrink exited 130 saying cancelled, while the partial MP3 remained.

Use `signal.NotifyContext` at the application boundary. Make the runner own child termination and cleanup; wait for the child to stop before removing its temporary output. Registration, cancellation checks, and startup need a coordinated lifecycle. Apply cancellation to ffprobe and network requests too, which currently bypass active-run tracking.

### 7. P2: The special rescale path changes quality and ignores hardware preference

Location: `main.go:485`, `media.go:754`.

`handleRescaleVideo` duplicates `handleVideo` but uses the compression preset's 128k audio bitrate for non-AAC tracks. The ordinary no-level conversion uses 256k. It also bypasses `hwEncodeFor`, so `-f` behaves differently depending on the input filename.

This finding comes from code inspection rather than a hardware benchmark. Remove the separate encoding implementation: represent rescale as an ordinary plan with transparent quality and a rescale operation label.

## Main flow and a stronger foundation

There is no long-running application loop. The controlling flow is:

```text
main: collect global flags -> dispatch special command or parse media targets
      -> validate -> preview, single file, or folder
folder: scan -> infer output/skip -> worker pool -> processOne -> collect summary
processOne: probe -> validate -> filename-based no-op/rescale -> resolve grade
            -> media handler -> subprocess runner -> finishJob
```

The worker pool and ordered result slots are reasonable. The problems arise because these stages repeat decisions and also mutate global output/reporting state.

Recommended flow:

```text
parse -> select command -> validate request -> inspect input -> plan
                                                        -> preview
                                                        -> execute -> verify -> publish
                                                                           -> optional delete
                                                                           -> report result
```

1. **Introduce a testable application boundary.** Keep `main` as `os.Exit(run(...))`. Have `run` return an exit code and inject arguments, output writers, dependencies, and context. Lower layers return errors instead of exiting. This makes command tests possible without terminating the test process.
2. **Register commands and their capabilities.** A small static `CommandSpec` registry can contain name, help, allowed flags, dependency requirements, and handler. Generate dispatch, help, and command completion from it. New tools then need one registration rather than edits to several switches and lists. Keep bare-file conversion as the fallback command. A dynamic plugin loader is unnecessary at this size.
3. **Create a resolved `JobPlan`.** Include input metadata, operation, output kind/container/codec, effective quality, filters, expected duration, deletion eligibility, and output policy. Separate plan construction from filesystem reservation. Single-file mode, batch mode, and dry runs use the same planner. Reserve final paths deterministically in input order before parallel execution.
4. **Make one runner own subprocesses and outputs.** Pass input/output paths explicitly instead of recovering them from argv. The runner manages context cancellation, temporary files, stderr capture, progress parsing, verification, and publication. A reporter observes progress; it does not own execution. Preserve existing destinations until a replacement has succeeded.
5. **Return results instead of printing inside media handlers.** Workers return `JobResult` values. The caller aggregates results and gives them to a human or JSON renderer. Move options, reservations, timings, and result slices into per-run state. This removes the need for globals such as `parallel`, `batchMode`, and `machineFiles` to control lower-layer behavior.
6. **Centralize format metadata.** Keep canonical name, aliases, media kind, container, encoder, probe codec name, quality policy, and capabilities together. Generate validation, doctor encoder groups, completion, and help from this data. Adding a format currently still requires updating several unrelated switches despite the existing format maps.

Implement these incrementally. Fix data loss and numeric validation first; introduce the planner next; consolidate runner/output ownership next; then migrate command dispatch and reporting.

## Useful optimization and redundancy removal

- Images are probed in `processOne`, then probed again for duration by `runFFmpeg`. Supply the known duration or zero to the shared runner. `worthRevisit` can also probe a file that `processOne` probes again; retain metadata in the plan.
- Merge `handleRescaleVideo`/`handleRescaleImage` into the normal builders. Remove `transparentCRF` after routing all codec quality selection through `videoCRF` and `transparentLevel`.
- Consolidate error mapping duplicated by `finishRun` and `runFFmpegQuiet`. Currently piped human output returns generic process errors while machine/interactive modes receive richer explanations.
- `candidateBase` accepts unused `outKind` and `fmtName` parameters. Remove them. Repeated `len(positional) > 0` conditions after the empty-list exit are also redundant.
- `filepath.WalkDir` already gives deterministic traversal. The final sort in `findMediaFiles` is only needed if the contract requires globally sorted paths rather than traversal order; these differ for some directory/file names. Decide that contract before removing the sort.
- Avoid sorting resolution keys on every `looksConverted` call. More importantly, remove filename heuristics from correctness decisions; this micro-optimization matters less than eliminating extra probes and encodes.
- Bound work by actual resource use. Multiple ffmpegs each choose their own thread count, so `-j` alone does not bound CPU use. Benchmark explicit per-job thread budgets before changing defaults. Streaming discovery/results is useful only if very large libraries become a target; the current JSON format inherently retains results.
- Add `info` to completion commands. Add deliberate `--help`, `--` handling, unknown-flag validation, and command-specific flag checks. For example, parsing errors before encountering `-m` currently depend on flag order and can emit human output despite a machine flag later in the command.
- Release hygiene: the release workflow builds/publishes without running the test job, while `release.sh` also publishes after pushing a tag that triggers that workflow. Choose one publishing owner and gate it on verification. Windows installation has no checksum verification, and the shell installer silently continues if verification data/tools are missing. These are code-inspection findings; installers, releases, and self-update were not executed.

## Validation and missing tests

Passed locally: uncached unit tests, race-enabled unit tests, `go vet`, build, and formatting check. Statement coverage is **25.6%**. Initial cache-related tool failures were resolved using temporary Go caches; they were not project failures. Passing race tests only covers the executed test paths, which do not exercise the subprocess lifecycle.

Real ffmpeg fixtures reproduced animation loss, default-run filename drift/duplicate output, and ignored container conversion. Controlled ffmpeg substitutes reproduced failed-output poisoning and delayed-output cancellation. Speed-factor validation was exercised at the CLI boundary. No hardware performance or Windows runtime validation was performed.

Prioritize command-level regression tests for preview/execution parity, idempotent folder reruns, format changes, animation and stream preservation before deletion, failed-child cleanup, cancellation during startup, output collisions, and non-finite numbers. Fake runner/prober dependencies can keep most tests fast; a small optional real-ffmpeg integration suite should cover actual output semantics.
