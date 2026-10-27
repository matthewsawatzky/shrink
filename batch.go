package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// batchMode suppresses the per-file "total" step line, since the batch
// summary reports wall time for the whole run.
var batchMode bool

// findMediaFiles collects media files under dir in sorted order. Recursion
// is opt-in; hidden entries are skipped. Unreadable entries are noted and
// skipped rather than aborting the scan.
func findMediaFiles(dir string, recursive bool) []string {
	var files []string
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s %s\n", ui.yellow("!"), ui.dim("cannot read "+path))
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
	opts options
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
	for _, suffix := range []string{
		"-compressed", "-compress", "-spacesaver", "-neversee", "-frame", "-audio",
	} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	for _, res := range sortedKeys(resolutions) {
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

// worthRevisit reports whether a converted-looking file deserves another
// pass: an explicit level squeezes it further, a speed target re-times
// it, or a resolution target would genuinely downscale it.
func worthRevisit(f string, o options) bool {
	if o.compress != "" || o.speed != "" {
		return true
	}
	if o.resolution == "" {
		return false
	}
	return probeFile(f).height > resHeight(o.resolution)
}

// wouldProduce names the output a run would make for f, mirroring
// processOne's routing, without encoding or bumping collision counters.
// o must already be narrowed to f's kind (see perKindOpts).
func wouldProduce(f, kind string, o options) string {
	dir := filepath.Dir(f)
	switch kind {
	case "video":
		switch {
		case o.audioFmt != "":
			return filepath.Join(dir, candidateBase(f, "audio", o.audioFmt, "audio", "", o.compress, o.speed)+"."+outExt("audio", o.audioFmt))
		case o.imageFmt != "":
			return filepath.Join(dir, candidateBase(f, "image", o.imageFmt, "frame", o.resolution, o.compress, "")+"."+outExt("image", o.imageFmt))
		default:
			return filepath.Join(dir, candidateBase(f, "video", coalesce(o.videoFmt, "mp4"), "", o.resolution, o.compress, o.speed)+"."+outExt("video", coalesce(o.videoFmt, "mp4")))
		}
	case "image":
		return filepath.Join(dir, candidateBase(f, "image", coalesce(o.imageFmt, "jpeg"), "", o.resolution, o.compress, "")+"."+outExt("image", coalesce(o.imageFmt, "jpeg")))
	case "audio":
		return filepath.Join(dir, candidateBase(f, "audio", coalesce(o.audioFmt, "mp3"), "", "", o.compress, o.speed)+"."+outExt("audio", coalesce(o.audioFmt, "mp3")))
	}
	return ""
}

// batchProcess compresses every eligible media file in dir and prints a
// one-line summary (or a JSON document in machine mode). Format targets
// only apply to their own kind, so videos are untouched by "mp3". With
// more than one worker, per-file results are held back until the run
// completes so output stays in input order, and a single shared
// progress line covers every in-flight encode. Files whose output
// already exists are skipped unless -o is given.
func batchProcess(dir string, o options, recursive bool, jobs int) {
	batchMode = true
	start := time.Now()

	files := findMediaFiles(dir, recursive)
	if len(files) == 0 {
		if machine {
			exitMachineError(machineError{Error: "no media files found in " + dir})
		}
		fmt.Fprintf(os.Stderr, "%s %s\n", errMark(), ui.red("no media files found in "+dir))
		if !recursive {
			if n := countMediaFiles(dir); n > 0 {
				fmt.Fprintf(os.Stderr, "  %s\n", ui.dim(fmt.Sprintf("%d found in subfolders - re-run with -r", n)))
			}
		}
		os.Exit(1)
	}

	// collect the worklist up front: skips are counted before anything
	// runs, and each item carries only its own kind's format target
	var work []batchWork
	skipped := map[string]int{}
	alreadyDone := 0
	for _, f := range files {
		kind := detectKind(f)
		if !kindAllowed(kind, o) {
			skipped[kind]++
			continue
		}
		fileOpts := perKindOpts(kind, o)
		out := wouldProduce(f, kind, fileOpts)
		// an output from an earlier run means there is nothing to do
		if !overwrite && fileExists(out) {
			alreadyDone++
			continue
		}
		// so does a file that already looks like one of our outputs -
		// unless this run would genuinely rescale it, or a level
		// explicitly squeezes it further
		if !overwrite && looksConverted(f) && !worthRevisit(f, o) {
			alreadyDone++
			continue
		}
		work = append(work, batchWork{path: f, out: out, opts: fileOpts})
	}

	if dryRun {
		reportDryRun(dir, recursive, work, skipped, alreadyDone)
		return
	}

	workers := jobs
	if workers > len(work) {
		workers = len(work)
	}
	if workers < 1 {
		workers = 1
	}
	parallel = workers > 1
	parallelTTY = parallel && !machine && isTerminal(os.Stderr)

	type batchResult struct {
		rep *fileReport
		err error
	}
	results := make([]batchResult, len(work))

	if parallel {
		// worker pool: each goroutine takes files from idx until it
		// closes; results land in their slot, so accounting below
		// stays in input order however jobs finish
		if parallelTTY {
			liveBatch.begin(len(work))
			fmt.Fprint(os.Stderr, "\033[?25l") // hide cursor for the shared line
		}
		idx := make(chan int)
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range idx {
					if interrupted.Load() {
						results[i].err = errCancelled
						continue
					}
					rep, err := processOne(work[i].path, work[i].opts)
					results[i].rep, results[i].err = rep, err
					if parallelTTY {
						liveBatch.complete(err == nil)
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
			rep, err := processOne(work[i].path, work[i].opts)
			results[i].rep, results[i].err = rep, err
			if err != nil && !machine && !interrupted.Load() {
				fmt.Fprintf(os.Stderr, "%s %s\n", errMark(), ui.red(failMsg(work[i].path, err)))
			}
		}
	}
	if interrupted.Load() {
		select {} // the interrupt handler is cleaning up and exiting
	}
	if parallelTTY {
		liveBatch.clear()
		fmt.Fprint(os.Stderr, "\033[?25h")
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
			if !machine && parallel {
				fmt.Fprintf(os.Stderr, "%s %s\n", errMark(), ui.red(msg))
			}
			continue
		}
		processed++
		if r.rep.statOK {
			inTotal += r.rep.inBytes
			outTotal += r.rep.outBytes
		}
		if r.rep.deleted {
			deleted++
		}
		if !machine && parallel {
			r.rep.print() // held back during the run; report in input order
		}
	}

	if machine {
		if parallel {
			// workers finish in arbitrary order; restore input order so
			// repeated runs of the same folder diff cleanly
			order := make(map[string]int, len(work))
			for i := range work {
				order[work[i].path] = i
			}
			machineFilesMu.Lock()
			sort.SliceStable(machineFiles, func(a, b int) bool {
				return order[machineFiles[a].Input] < order[machineFiles[b].Input]
			})
			machineFilesMu.Unlock()
		}
		machineSkipped = skipped
		machineAlreadyDone = alreadyDone
		emitMachineOutput(dir, recursive, failures, time.Since(start), processed)
		if failed > 0 {
			os.Exit(1)
		}
		return
	}

	printBatchSummary(processed, failed, skipped, inTotal, outTotal, time.Since(start))
	if deleted > 0 {
		fmt.Fprintf(os.Stderr, "  %s\n", ui.dim(fmt.Sprintf("%d originals deleted", deleted)))
	}
	if alreadyDone > 0 {
		fmt.Fprintf(os.Stderr, "  %s\n", ui.dim(fmt.Sprintf("%d %s already converted (or already look converted) - re-run with -o to redo them",
			alreadyDone, filesWord(alreadyDone))))
	}
	if failed > 0 {
		os.Exit(1)
	}
}

// reportDryRun prints what a batch would do without encoding anything.
func reportDryRun(dir string, recursive bool, work []batchWork, skipped map[string]int, alreadyDone int) {
	if machine {
		for _, w := range work {
			machineFiles = append(machineFiles, machineFile{Input: w.path, Output: w.out, Kind: detectKind(w.path)})
		}
		machineDryRun = true
		machineSkipped = skipped
		machineAlreadyDone = alreadyDone
		emitMachineOutput(dir, recursive, nil, 0, len(work))
		return
	}
	for _, w := range work {
		fmt.Fprintf(os.Stderr, "would compress %s -> %s\n", ui.cyan(w.path), ui.bold(w.out))
	}
	fmt.Fprintf(os.Stderr, "%s dry run: %d %s, nothing written\n", doneMark(), len(work), filesWord(len(work)))
	if deleteAfter {
		fmt.Fprintf(os.Stderr, "  %s\n", ui.dim("originals will be deleted after each successful compression"))
	}
	if alreadyDone > 0 {
		fmt.Fprintf(os.Stderr, "  %s\n", ui.dim(fmt.Sprintf("%d %s already converted (or already look converted) - re-run with -o to redo them",
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
		fmt.Fprintf(os.Stderr, "  %s\n", ui.dim(fmt.Sprintf("would skip %d %s (%s) - targets apply to matching files only",
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

func printBatchSummary(processed, failed int, skipped map[string]int, inTotal, outTotal int64, elapsed time.Duration) {
	mark := doneMark()
	if failed > 0 {
		mark = errMark()
	}
	line := fmt.Sprintf("%d %s", processed, filesWord(processed))
	if failed > 0 {
		line = fmt.Sprintf("%d ok, %d failed", processed, failed)
	}
	if inTotal > 0 {
		line += fmt.Sprintf("  (%s → %s)  %s",
			humanSize(inTotal), humanSize(outTotal), savingsText(inTotal, outTotal))
	}
	fmt.Fprintf(os.Stderr, "%s %s  %s\n", mark, line, ui.dim("took "+humanDuration(elapsed.Seconds())))

	var skippedTotal int
	var breakdown []string
	for _, kind := range mediaKinds {
		if n := skipped[kind]; n > 0 {
			skippedTotal += n
			breakdown = append(breakdown, fmt.Sprintf("%d %s", n, kind))
		}
	}
	if skippedTotal > 0 {
		fmt.Fprintf(os.Stderr, "  %s\n", ui.dim(fmt.Sprintf("skipped %d %s (%s) - targets apply to matching files only",
			skippedTotal, filesWord(skippedTotal), strings.Join(breakdown, ", "))))
	}
}

func filesWord(n int) string {
	if n == 1 {
		return "file"
	}
	return "files"
}
