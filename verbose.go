package main

import (
	"fmt"
	"os"
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

var steps []step

// trackStep starts timing a named stage. The returned function stops the
// clock; outside verbose/machine mode it is a no-op.
func trackStep(name string) func() {
	if !verbose && !machine {
		return func() {}
	}
	start := time.Now()
	return func() { recordStep(name, time.Since(start)) }
}

// recordStep appends a completed stage to the step list.
func recordStep(name string, d time.Duration) {
	if !verbose && !machine {
		return
	}
	steps = append(steps, step{name, d})
}

// formatMS renders a duration in whole milliseconds, verbose's only unit.
func formatMS(d time.Duration) string {
	return fmt.Sprintf("%dms", (d + 500*time.Microsecond).Milliseconds())
}

// printSteps lists every recorded stage under the done line, plus the total
// wall time, and clears the list for a next run. Batch runs skip the total
// label because their summary line already reports whole-run time. Machine
// mode carries the steps in its JSON instead.
func printSteps() {
	if machine || !verbose || len(steps) == 0 {
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
	steps = nil
}
