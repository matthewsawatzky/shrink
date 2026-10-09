package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// kindAllowed reports whether a file kind has a matching format target.
// With no format targets at all, every kind is in scope.
func kindAllowed(kind string, o options) bool {
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

// batchProcess compresses every eligible media file in dir and prints a
// one-line summary (or a JSON document in machine mode). Format targets
// only apply to their own kind, so videos are untouched by "mp3".
func batchProcess(dir string, o options, recursive bool) {
	batchMode = true
	start := time.Now()

	files := findMediaFiles(dir, recursive)
	if len(files) == 0 {
		if machine {
			exitMachineError(machineError{Error: "no media files found in " + dir})
		}
		fmt.Fprintf(os.Stderr, "%s %s\n", errMark(), ui.red("no media files found in "+dir))
		if !recursive {
			if n := len(findMediaFiles(dir, true)); n > 0 {
				fmt.Fprintf(os.Stderr, "  %s\n", ui.dim(fmt.Sprintf("%d found in subfolders - re-run with -r", n)))
			}
		}
		printSteps()
		os.Exit(1)
	}

	var processed, failed int
	var inTotal, outTotal int64
	var failures []machineFailure
	skipped := map[string]int{}

	for _, f := range files {
		kind := detectKind(f)
		if !kindAllowed(kind, o) {
			skipped[kind]++
			continue
		}

		// pass only this kind's format target, so the same-kind rule holds
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

		outPath, err := processOne(f, fileOpts)
		if err != nil {
			failed++
			msg := err.Error()
			// most errors already name the file; don't say it twice
			if !strings.Contains(msg, filepath.Base(f)) {
				msg = filepath.Base(f) + ": " + msg
			}
			failures = append(failures, machineFailure{Input: f, Error: msg})
			if !machine {
				fmt.Fprintf(os.Stderr, "%s %s\n", errMark(), ui.red(msg))
			}
			continue
		}
		processed++
		if inSize, err := os.Stat(f); err == nil {
			if outSize, err := os.Stat(outPath); err == nil {
				inTotal += inSize.Size()
				outTotal += outSize.Size()
			}
		}
	}

	if machine {
		machineSkipped = skipped
		emitMachineOutput(dir, recursive, failures, time.Since(start), processed)
		if failed > 0 {
			os.Exit(1)
		}
		return
	}

	printBatchSummary(processed, failed, skipped, inTotal, outTotal, time.Since(start))
	if failed > 0 {
		os.Exit(1)
	}
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
	printSteps()
}

func filesWord(n int) string {
	if n == 1 {
		return "file"
	}
	return "files"
}
