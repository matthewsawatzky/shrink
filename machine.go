package main

import (
	"encoding/json"
	"math"
	"time"
)

type machineStep struct {
	Name string `json:"name"`
	MS   int64  `json:"ms"`
}

// machineFile is one completed compression job.
type machineFile struct {
	Input          string        `json:"input"`
	Output         string        `json:"output"`
	Kind           string        `json:"kind"`
	Operation      string        `json:"operation,omitempty"`
	InputBytes     int64         `json:"input_bytes"`
	OutputBytes    int64         `json:"output_bytes"`
	SavingsPercent float64       `json:"savings_percent"`
	TookMS         int64         `json:"took_ms"`
	Warning        string        `json:"warning,omitempty"`
	DeletedInput   bool          `json:"deleted_input,omitempty"`
	Steps          []machineStep `json:"steps,omitempty"`
}

type machineFailure struct {
	Input string `json:"input"`
	Error string `json:"error"`
}

type machineSummary struct {
	Processed      int     `json:"processed"`
	Failed         int     `json:"failed"`
	Skipped        int     `json:"skipped"`
	Deleted        int     `json:"deleted,omitempty"`
	InputBytes     int64   `json:"input_bytes"`
	OutputBytes    int64   `json:"output_bytes"`
	SavingsPercent float64 `json:"savings_percent"`
	TookMS         int64   `json:"took_ms"`
}

// machineOutput is the single document emitted for any run: one entry per
// compressed file, plus folder metadata when a folder was scanned.
type machineOutput struct {
	Version     string           `json:"version,omitempty"`
	DryRun      bool             `json:"dry_run,omitempty"`
	Note        string           `json:"note,omitempty"`
	Folder      string           `json:"folder,omitempty"`
	Recursive   bool             `json:"recursive,omitempty"`
	Files       []machineFile    `json:"files"`
	Failed      []machineFailure `json:"failed,omitempty"`
	Skipped     map[string]int   `json:"skipped,omitempty"`
	AlreadyDone int              `json:"already_done,omitempty"`
	Summary     machineSummary   `json:"summary"`
}

type machineError struct {
	Error        string   `json:"error"`
	Input        string   `json:"input,omitempty"`
	ValidTargets []string `json:"valid_targets,omitempty"`
	ValidInputs  []string `json:"valid_inputs,omitempty"`
	Suggestions  []string `json:"suggestions,omitempty"`
}

func (a *application) printJSON(v interface{}) {
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

// exitMachineError emits a structured error and exits 1. Errors stay out of
// stdout's normal output path only in the sense that a run produces either
// results or an error - never both.
func (a *application) exitMachineError(e machineError) {
	a.printJSON(e)
	exitCLI(1)
}

// emitMachineOutput prints the final document for a completed run.
func (a *application) emitMachineOutput(folder string, recursive bool, failures []machineFailure, elapsed time.Duration, processedOverride int) {
	files := a.machineFiles
	if files == nil {
		files = []machineFile{}
	}
	processed := len(files)
	if processedOverride >= 0 {
		processed = processedOverride
	}

	sum := machineSummary{
		Processed: processed,
		Failed:    len(failures),
		TookMS:    elapsed.Milliseconds(),
	}
	var inTotal, outTotal int64
	var deleted int
	for _, f := range files {
		inTotal += f.InputBytes
		outTotal += f.OutputBytes
		if f.DeletedInput {
			deleted++
		}
	}
	sum.InputBytes = inTotal
	sum.OutputBytes = outTotal
	sum.Deleted = deleted
	if inTotal > 0 {
		sum.SavingsPercent = round2((1 - float64(outTotal)/float64(inTotal)) * 100)
	}
	for _, n := range a.machineSkipped {
		sum.Skipped += n
	}
	sum.Skipped += a.machineAlreadyDone

	out := machineOutput{
		Version:     version,
		DryRun:      a.machineDryRun,
		Note:        a.machineNote,
		Folder:      folder,
		Recursive:   recursive,
		Files:       files,
		Failed:      failures,
		AlreadyDone: a.machineAlreadyDone,
		Summary:     sum,
	}
	if len(a.machineSkipped) > 0 {
		out.Skipped = a.machineSkipped
	}
	a.printJSON(out)
}

// machineStepsFor converts an input's recorded step timings and clears
// them, so each file carries its own breakdown.
func (a *application) machineStepsFor(input string) []machineStep {
	a.stepsMu.Lock()
	steps := a.stepsByFile[input]
	delete(a.stepsByFile, input)
	a.stepsMu.Unlock()
	if len(steps) == 0 {
		return nil
	}
	out := make([]machineStep, 0, len(steps))
	for _, s := range steps {
		out = append(out, machineStep{Name: s.name, MS: s.dur.Milliseconds()})
	}
	return out
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
