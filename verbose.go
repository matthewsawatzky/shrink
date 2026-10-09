package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// verbose enables -v/--verbose mode: every duration is reported in ms, every
// size in KB, and each stage of a run logs how long it took.
var verbose bool

// progStart anchors the "total" line in the verbose step list.
var progStart = time.Now()

// step is one timed stage of a run, logged together at the end.
type step struct {
	name string
	dur  time.Duration
}

// Steps are recorded per input file, so parallel batch runs can never
// mix one file's timings into another's report.
var (
	stepsMu     sync.Mutex
	stepsByFile = map[string][]step{}
)

// trackStep starts timing a named stage of one input. The returned
// function stops the clock; outside verbose/machine mode it is a no-op.
func trackStep(input, name string) func() {
	if !verbose && !machine {
		return func() {}
	}
	start := time.Now()
	return func() { recordStep(input, name, time.Since(start)) }
}

// recordStep appends a completed stage to its input's step list.
func recordStep(input, name string, d time.Duration) {
	if !verbose && !machine {
		return
	}
	stepsMu.Lock()
	stepsByFile[input] = append(stepsByFile[input], step{name, d})
	stepsMu.Unlock()
}

// formatMS renders a duration in whole milliseconds, verbose's only unit.
func formatMS(d time.Duration) string {
	return fmt.Sprintf("%dms", (d + 500*time.Microsecond).Milliseconds())
}

// printStepsFor lists an input's recorded stages under the done line,
// plus the total wall time, and clears them. Batch runs skip the total
// label because their summary line already reports whole-run time.
// Machine mode carries the steps in its JSON instead.
func printStepsFor(input string) {
	if machine || !verbose {
		return
	}
	stepsMu.Lock()
	steps := stepsByFile[input]
	delete(stepsByFile, input)
	stepsMu.Unlock()
	if len(steps) == 0 {
		return
	}
	width := 0
	for _, s := range steps {
		if len(s.name) > width {
			width = len(s.name)
		}
	}
	for _, s := range steps {
		fmt.Fprintf(os.Stderr, "  %s %s  %s\n",
			ui.dim("·"), ui.dim(fmt.Sprintf("%-*s", width, s.name)), formatMS(s.dur))
	}
	if !batchMode {
		fmt.Fprintf(os.Stderr, "  %s %s  %s\n",
			ui.dim("·"), ui.dim(fmt.Sprintf("%-*s", width, "total")), ui.bold(formatMS(time.Since(progStart))))
	}
}
