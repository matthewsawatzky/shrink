package main

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// findMediaFiles collects media files under dir in sorted order. Recursion
// is opt-in; hidden entries are skipped. Unreadable entries are noted and
// skipped rather than aborting the scan.
func (a *application) findMediaFiles(dir string, recursive bool) []string {
	var files []string
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			fmt.Fprintf(a.stderr, "%s %s\n", a.ui.yellow("!"), a.ui.dim("cannot read "+path))
			return nil
		}
		if path != dir && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != dir && !recursive {
				return filepath.SkipDir
			}
			return nil
		}
		if detectKind(path) != "unknown" {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files
}

// countMediaFiles counts media files under dir (always recursive) without
// building the list, for the "did you mean -r" hint. Mirrors findMediaFiles'
// skip rules exactly so the count can never disagree with a real scan.
func countMediaFiles(dir string) int {
	n := 0
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path != dir && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if detectKind(path) != "unknown" {
			n++
		}
		return nil
	})
	return n
}

// kindAllowed reports whether a file kind has a matching format target.
// With no format targets at all, every kind is in scope - except images
// cannot be sped, so they fall out when a speed target is set.
func kindAllowed(kind string, o options) bool {
	if o.speed != "" && kind == "image" {
		return false
	}
	if o.videoFmt == "" && o.imageFmt == "" && o.audioFmt == "" {
		return true
	}
	switch kind {
	case "video":
		return o.videoFmt != ""
	case "image":
		return o.imageFmt != ""
	case "audio":
		return o.audioFmt != ""
	}
	return false
}

// batchWork is one eligible file: its path, the output it would produce,
// and the per-kind format targets that apply to it.
type batchWork struct {
	path string
	out  string
	plan jobPlan
	err  error
}

// perKindOpts narrows the parsed targets to the one format that applies
// to a file's kind, so the same-kind rule holds in batch runs (a video
// file ignores the "mp3" target that only applies to audio files).
func perKindOpts(kind string, o options) options {
	fileOpts := o
	fileOpts.videoFmt, fileOpts.imageFmt, fileOpts.audioFmt = "", "", ""
	switch kind {
	case "video":
		fileOpts.videoFmt = o.videoFmt
	case "image":
		fileOpts.imageFmt = o.imageFmt
	case "audio":
		fileOpts.audioFmt = o.audioFmt
	}
	return fileOpts
}

// looksConverted reports whether a file's name matches the outputs
// shrink itself produces (-compressed, level, resolution, "frame" or
// "audio" tags), so folder re-runs don't chew their own results.
// Camera-style names (IMG_1234) never match: the tags use dashes.
func looksConverted(path string) bool {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if i := strings.LastIndexByte(base, '-'); i >= 0 {
		if n, err := strconv.Atoi(base[i+1:]); err == nil && n > 0 {
			base = base[:i]
		}
	}
	for _, suffix := range []string{
		"-compressed", "-compress", "-spacesaver", "-neversee", "-frame", "-audio",
	} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	for res := range resolutions {
		if strings.HasSuffix(base, "-"+res) {
			return true
		}
	}
	// a speed tag trailing the stem: "-x5", "-x0.1", "-x2.5"
	if i := strings.LastIndexByte(base, '-'); i >= 0 {
		if _, _, ok := parseSpeed(base[i+1:]); ok {
			return true
		}
	}
	return false
}

// batchProcess compresses every eligible media file in dir and prints a
// one-line summary (or a JSON document in machine mode). Format targets
// only apply to their own kind, so videos are untouched by "mp3". With
// more than one worker, per-file results are held back until the run
// completes so output stays in input order, and a single shared
// progress line covers every in-flight encode. Files whose output
// already exists are skipped unless -o is given.
func (a *application) batchProcess(dir string, o options, recursive bool, jobs int) {
	a.batchMode = true
	start := time.Now()

	files := a.findMediaFiles(dir, recursive)
	if len(files) == 0 {
		if a.machine {
			a.exitMachineError(machineError{Error: "no media files found in " + dir})
		}
		fmt.Fprintf(a.stderr, "%s %s\n", a.errMark(), a.ui.red("no media files found in "+dir))
		if !recursive {
			if n := countMediaFiles(dir); n > 0 {
				fmt.Fprintf(a.stderr, "  %s\n", a.ui.dim(fmt.Sprintf("%d found in subfolders - re-run with -r", n)))
			}
		}
		exitCLI(1)
	}

	// collect the worklist up front: skips are counted before anything
	// runs, and each item carries only its own kind's format target
	var work []batchWork
	skipped := map[string]int{}
	alreadyDone := 0
	// Inspect each eligible source once, reusing metadata in workers. An
	// output belonging to another source in this scan is never a fresh input.
	plans := map[string]jobPlan{}
	errors := map[string]error{}
	completed := map[string]bool{}
	generated := map[string]bool{}
	for _, f := range files {
		if !kindAllowed(detectKind(f), o) {
			continue
		}
		p, err := a.planJob(f, perKindOpts(detectKind(f), o))
		plans[f], errors[f] = p, err
		completed[f] = err == nil && !a.overwrite && p.existingResult()
		if completed[f] {
			generated[p.output] = true
		}
	}
	for _, f := range files {
		kind := detectKind(f)
		if !kindAllowed(kind, o) {
			skipped[kind]++
			continue
		}
		p, err := plans[f], errors[f]
		if !a.overwrite && (generated[f] || (err == nil && (p.noop || completed[f]))) {
			alreadyDone++
			continue
		}
		if err == nil {
			p.reserve()
		}
		work = append(work, batchWork{path: f, out: p.output, plan: p, err: err})
	}

	if a.dryRun {
		a.reportDryRun(dir, recursive, work, skipped, alreadyDone)
		return
	}
	if alreadyDone > 0 {
		a.updateForegroundStats(func(stats *queueStats) { stats.Skipped += int64(alreadyDone) })
	}

	workers := jobs
	if workers > len(work) {
		workers = len(work)
	}
	if workers < 1 {
		workers = 1
	}
	a.parallel = workers > 1
	a.parallelTTY = a.parallel && !a.machine && writerIsTerminal(a.stderr)

	type batchResult struct {
		rep *fileReport
		err error
	}
	results := make([]batchResult, len(work))

	if a.parallel {
		// worker pool: each goroutine takes files from idx until it
		// closes; results land in their slot, so accounting below
		// stays in input order however jobs finish
		if a.parallelTTY {
			a.liveBatch.begin(len(work))
			fmt.Fprint(a.stderr, "\033[?25l") // hide cursor for the shared line
		}
		idx := make(chan int)
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range idx {
					if a.ctx.Err() != nil {
						results[i].err = errCancelled
						continue
					}
					rep, err := executeWork(work[i])
					results[i].rep, results[i].err = rep, err
					if a.parallelTTY {
						a.liveBatch.complete(err == nil)
					}
				}
			}()
		}
		for i := range work {
			idx <- i
		}
		close(idx)
		wg.Wait()
	} else {
		for i := range work {
			rep, err := executeWork(work[i])
			results[i].rep, results[i].err = rep, err
			if err != nil && !a.machine && a.ctx.Err() == nil {
				fmt.Fprintf(a.stderr, "%s %s\n", a.errMark(), a.ui.red(failMsg(work[i].path, err)))
			}
		}
	}
	if a.ctx.Err() != nil {
		return // every active child has stopped and cleaned its temporary output
	}
	if a.parallelTTY {
		a.liveBatch.clear()
		fmt.Fprint(a.stderr, "\033[?25h")
	}

	var processed, failed, deleted int
	var inTotal, outTotal int64
	var failures []machineFailure
	for i := range work {
		r := results[i]
		if r.rep != nil && r.rep.noop {
			// defensive: the worklist normally filters these out
			alreadyDone++
			continue
		}
		if r.err != nil {
			failed++
			msg := failMsg(work[i].path, r.err)
			failures = append(failures, machineFailure{Input: work[i].path, Error: msg})
			if !a.machine && a.parallel {
				fmt.Fprintf(a.stderr, "%s %s\n", a.errMark(), a.ui.red(msg))
			}
			continue
		}
		processed++
		if a.machine {
			a.recordMachineFile(r.rep)
		}
		if r.rep.statOK {
			inTotal += r.rep.inBytes
			outTotal += r.rep.outBytes
		}
		if r.rep.deleted {
			deleted++
		}
		if !a.machine {
			r.rep.print() // held back during the run; report in input order
		}
	}

	if a.machine {

		a.machineSkipped = skipped
		a.machineAlreadyDone = alreadyDone
		a.emitMachineOutput(dir, recursive, failures, time.Since(start), processed)
		if failed > 0 {
			exitCLI(1)
		}
		return
	}

	a.printBatchSummary(processed, failed, skipped, inTotal, outTotal, time.Since(start))
	if deleted > 0 {
		fmt.Fprintf(a.stderr, "  %s\n", a.ui.dim(fmt.Sprintf("%d originals deleted", deleted)))
	}
	if alreadyDone > 0 {
		fmt.Fprintf(a.stderr, "  %s\n", a.ui.dim(fmt.Sprintf("%d %s already converted (or already look converted) - re-run with -o to redo them",
			alreadyDone, filesWord(alreadyDone))))
	}
	if failed > 0 {
		exitCLI(1)
	}
}

// reportDryRun prints what a batch would do without encoding anything.
func (a *application) reportDryRun(dir string, recursive bool, work []batchWork, skipped map[string]int, alreadyDone int) {
	for _, w := range work {
		if w.err != nil {
			a.exitError("%v", w.err)
		}
	}
	if a.machine {
		for _, w := range work {
			a.machineFiles = append(a.machineFiles, machineFile{Input: w.path, Output: w.out, Kind: detectKind(w.path)})
		}
		a.machineDryRun = true
		a.machineSkipped = skipped
		a.machineAlreadyDone = alreadyDone
		a.emitMachineOutput(dir, recursive, nil, 0, len(work))
		return
	}
	for _, w := range work {
		fmt.Fprintf(a.stderr, "would compress %s -> %s\n", a.ui.cyan(w.path), a.ui.bold(w.out))
	}
	fmt.Fprintf(a.stderr, "%s dry run: %d %s, nothing written\n", a.doneMark(), len(work), filesWord(len(work)))
	if a.deleteAfter {
		fmt.Fprintf(a.stderr, "  %s\n", a.ui.dim("originals will be deleted after each successful compression"))
	}
	if alreadyDone > 0 {
		fmt.Fprintf(a.stderr, "  %s\n", a.ui.dim(fmt.Sprintf("%d %s already converted (or already look converted) - re-run with -o to redo them",
			alreadyDone, filesWord(alreadyDone))))
	}
	var skippedTotal int
	var breakdown []string
	for _, kind := range mediaKinds {
		if n := skipped[kind]; n > 0 {
			skippedTotal += n
			breakdown = append(breakdown, fmt.Sprintf("%d %s", n, kind))
		}
	}
	if skippedTotal > 0 {
		fmt.Fprintf(a.stderr, "  %s\n", a.ui.dim(fmt.Sprintf("would skip %d %s (%s) - targets apply to matching files only",
			skippedTotal, filesWord(skippedTotal), strings.Join(breakdown, ", "))))
	}
}

// failMsg renders a batch failure, naming the file unless the error
// already does.
func failMsg(path string, err error) string {
	msg := err.Error()
	if !strings.Contains(msg, filepath.Base(path)) {
		msg = filepath.Base(path) + ": " + msg
	}
	return msg
}

func (a *application) printBatchSummary(processed, failed int, skipped map[string]int, inTotal, outTotal int64, elapsed time.Duration) {
	mark := a.doneMark()
	if failed > 0 {
		mark = a.errMark()
	}
	line := fmt.Sprintf("%d %s", processed, filesWord(processed))
	if failed > 0 {
		line = fmt.Sprintf("%d ok, %d failed", processed, failed)
	}
	if inTotal > 0 {
		line += fmt.Sprintf("  (%s → %s)  %s",
			a.humanSize(inTotal), a.humanSize(outTotal), a.savingsText(inTotal, outTotal))
	}
	fmt.Fprintf(a.stderr, "%s %s  %s\n", mark, line, a.ui.dim("took "+a.humanDuration(elapsed.Seconds())))

	var skippedTotal int
	var breakdown []string
	for _, kind := range mediaKinds {
		if n := skipped[kind]; n > 0 {
			skippedTotal += n
			breakdown = append(breakdown, fmt.Sprintf("%d %s", n, kind))
		}
	}
	if skippedTotal > 0 {
		fmt.Fprintf(a.stderr, "  %s\n", a.ui.dim(fmt.Sprintf("skipped %d %s (%s) - targets apply to matching files only",
			skippedTotal, filesWord(skippedTotal), strings.Join(breakdown, ", "))))
	}
}

func filesWord(n int) string {
	if n == 1 {
		return "file"
	}
	return "files"
}

func executeWork(w batchWork) (*fileReport, error) {
	if w.err != nil {
		w.plan.app.recordForegroundOutcome(w.path, nil, w.err)
		return nil, w.err
	}
	return w.plan.execute()
}
