package main

import (
	"fmt"
	"time"
)

type step struct {
	name string
	dur  time.Duration
}

func (a *application) trackStep(input, name string) func() {
	if !a.verbose && !a.machine {
		return func() {}
	}
	start := time.Now()
	return func() { a.recordStep(input, name, time.Since(start)) }
}

// recordStep appends a completed stage to its input's step list.
func (a *application) recordStep(input, name string, d time.Duration) {
	if !a.verbose && !a.machine {
		return
	}
	a.stepsMu.Lock()
	a.stepsByFile[input] = append(a.stepsByFile[input], step{name, d})
	a.stepsMu.Unlock()
}

// formatMS renders a duration in whole milliseconds, verbose's only unit.
func formatMS(d time.Duration) string {
	return fmt.Sprintf("%dms", (d + 500*time.Microsecond).Milliseconds())
}

// printStepsFor lists an input's recorded stages under the done line,
// plus the total wall time, and clears them. Batch runs skip the total
// label because their summary line already reports whole-run time.
// Machine mode carries the steps in its JSON instead.
func (a *application) printStepsFor(input string) {
	if a.machine || !a.verbose {
		return
	}
	a.stepsMu.Lock()
	steps := a.stepsByFile[input]
	delete(a.stepsByFile, input)
	a.stepsMu.Unlock()
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
		fmt.Fprintf(a.stderr, "  %s %s  %s\n",
			a.ui.dim("·"), a.ui.dim(fmt.Sprintf("%-*s", width, s.name)), formatMS(s.dur))
	}
	if !a.batchMode {
		fmt.Fprintf(a.stderr, "  %s %s  %s\n",
			a.ui.dim("·"), a.ui.dim(fmt.Sprintf("%-*s", width, "total")), a.ui.bold(formatMS(time.Since(a.progStart))))
	}
}
