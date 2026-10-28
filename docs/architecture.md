# Application and job lifecycle

Each invocation creates an `application` with its own context, flags, encoder cache, output claims, timings, progress display and result collection. Output writers are injected, so CLI tests can inspect exit codes and JSON without replacing process-wide stdout or stderr. Independent application instances can run concurrently. Create a fresh instance for each invocation.

Only `main` calls `os.Exit`. CLI errors unwind to the application boundary using a private exit-status type, allowing deferred cleanup to run. Planning and subprocess errors are ordinary returned errors. Unknown panics are rethrown.

## Adding a command

Register a command in `commands` in `cli.go` with its name, usage, permitted flags and handler. That registry supplies dispatch, command usage and completion names. Keep validation and dependencies in its handler; media-specific dependencies are not imposed on commands such as update or completion. For a handler that uses a child process, use `application.command` so cancellation and forced shutdown work consistently. Network requests use the same application context.

## Adding an output format

Add a `formatSpec` in `formats.go` with the encoder, extension, ffprobe codec, container, optional alias, codec arguments and any required video audio encoder. Those tables drive format parsing, output naming, help, completion, doctor groups, and satisfaction checks. An encoder with a new quality control mechanism also needs a policy in the codec argument builders.

## Media processing

```text
CLI request -> validate -> inspect -> jobPlan -> preview
                                         -> reserve output -> execute
                                            -> verify temporary file
                                            -> publish
                                            -> deletion policy
                                            -> return result -> render
```

`describeJob` resolves names and operations without I/O. `planJob` inspects and validates the input once, selects effective quality, and builds arguments. It rejects animated image inputs until animation-preserving encoding is implemented. Format matching checks the extension, actual container and probe codec; names only trigger an opportunity to check for a no-op.

A bare compression request defaults to the compression preset but retains the `-compressed` filename. An explicit compression target is included in the name. Previews and execution consume the same resolved plan and collision reservation. Scaling an earlier output uses the normal transparent-quality builder, including hardware preference; there is no separate rescale encoder implementation.

Folder mode builds plans and reserves outputs in input order before starting its bounded worker pool. A pre-existing destination counts as completed only if its metadata, duration and modification time satisfy the request. Invalid old outputs remain visible as failures rather than being treated as success. Workers return results; the coordinator reports them in input order. Global sorting is retained because lexicographic full-path order differs from directory traversal order.

## Output safety and cancellation

The runner creates a private sibling file with the destination's extension. It captures diagnostics consistently for terminal, piped and machine output, checks that ffprobe can read the result and that its codecs and duration fit the encode request, and then publishes it. Non-overwrite publication uses a filesystem hard link, which fails if another process claimed the destination. With overwrite, rename replaces the destination only after successful encoding and verification. Filesystems lacking the required operation fail without clobbering the destination.

Cancellation originates from `signal.NotifyContext`. Every child uses `CommandContext`, receives an interrupt where supported, and is force-killed after a two-second grace period. Cleanup removes temporary output after the child has stopped. Progress is consumed before `Wait` closes its pipe, and the successful final display appears only after verification and publication.

Original deletion additionally requires a smaller same-kind output with all source stream kinds/counts preserved. Video encoding maps all audio tracks. If unsupported auxiliary streams would be dropped, the original remains and the report explains why. Derived audio/frame outputs never permit deletion.

## Verification and deployment

Regression tests cover numeric validation, independent invocations, preview parity, idempotent reruns, format changes, animation protection, failed overwrite preservation, publication collisions, cancellation, transparent rescaling, and stream preservation. Real ffmpeg fixtures are skipped if dependencies are unavailable. Controlled child-process fixtures currently require a POSIX shell and skip on Windows.

Local release tooling checks formatting, runs vet and race-enabled unit/integration tests, and builds all six platforms using the existing Go cache. It pushes the commit/tag and uses the shared publish helper to validate checksums, upload a draft, and publish after all uploads succeed. `--resume` recovers interrupted releases without moving tags or overwriting published versions.

CI retains full verification for pull requests and manual runs. Tag pushes do not automatically rebuild releases. A manual Release fallback verifies an explicitly selected tag and uses the same build, changelog and publish helpers when local publishing is unavailable. Both installers require a valid matching SHA-256 checksum before extraction/install.

CPU-thread budgets and streaming discovery remain workload-dependent tuning opportunities. The worker count is bounded by `-j`; encoder thread defaults remain unchanged because no representative performance benchmark justifies a different default.


## Durable background work

The queue wraps the existing planner and transactional runner; it does not introduce a second encoder implementation. State/settings/anonymous aggregate statistics share one atomic snapshot protected by an OS file lock. A separate worker lock guarantees single ownership and automatically releases on process death. Inputs and output names are reserved before workers are dispatched; the same input never runs concurrently.

Each running job owns an application/context and a known temporary path. A pipe-watching helper cancels the encoder when its worker disappears. Unfinished jobs restart from scratch. Completion and statistics commit together, then optional original deletion runs; it can leave an original after a crash, but cannot leave a pending job whose original has already been deleted. Completed/cancelled records have their paths and options removed. See [queue.md](queue.md) for semantics and platform limits.
