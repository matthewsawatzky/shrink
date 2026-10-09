package main

import (
	"encoding/json"
	"math"
	"os"
	"time"
)

// machine enables -m/--machine: one JSON document on stdout instead of the
// human-facing output, for scripts and LLMs. Progress and status text are
// suppressed; failures come back as {"error": ...} with exit code 1.
var machine bool

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
	InputBytes     int64   `json:"input_bytes"`
	OutputBytes    int64   `json:"output_bytes"`
	SavingsPercent float64 `json:"savings_percent"`
	TookMS         int64   `json:"took_ms"`
}

// machineOutput is the single document emitted for any run: one entry per
// compressed file, plus folder metadata when a folder was scanned.
type machineOutput struct {
	Folder    string           `json:"folder,omitempty"`
	Recursive bool             `json:"recursive,omitempty"`
	Files     []machineFile    `json:"files"`
	Failed    []machineFailure `json:"failed,omitempty"`
	Skipped   map[string]int   `json:"skipped,omitempty"`
	Summary   machineSummary   `json:"summary"`
}

type machineError struct {
	Error        string   `json:"error"`
	Input        string   `json:"input,omitempty"`
	ValidTargets []string `json:"valid_targets,omitempty"`
	ValidInputs  []string `json:"valid_inputs,omitempty"`
	Suggestions  []string `json:"suggestions,omitempty"`
}

// machineFiles collects per-file results for the final document.
var machineFiles []machineFile

// machineSkipped counts files passed over because their kind had no target.
var machineSkipped = map[string]int{}

func printJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

// exitMachineError emits a structured error and exits 1. Errors stay out of
// stdout's normal output path only in the sense that a run produces either
// results or an error - never both.
func exitMachineError(e machineError) {
	printJSON(e)
	os.Exit(1)
}

// emitMachineOutput prints the final document for a completed run.
func emitMachineOutput(folder string, recursive bool, failures []machineFailure, elapsed time.Duration, processedOverride int) {
	files := machineFiles
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
	for _, f := range files {
		inTotal += f.InputBytes
		outTotal += f.OutputBytes
	}
	sum.InputBytes = inTotal
	sum.OutputBytes = outTotal
	if inTotal > 0 {
		sum.SavingsPercent = round2((1 - float64(outTotal)/float64(inTotal)) * 100)
	}
	for _, n := range machineSkipped {
		sum.Skipped += n
	}

	out := machineOutput{
		Folder:    folder,
		Recursive: recursive,
		Files:     files,
		Failed:    failures,
		Summary:   sum,
	}
	if len(machineSkipped) > 0 {
		out.Skipped = machineSkipped
	}
	printJSON(out)
}

// machineSteps converts the recorded step timings and clears them, so each
// file carries its own breakdown.
func machineSteps() []machineStep {
	if len(steps) == 0 {
		return nil
	}
	out := make([]machineStep, 0, len(steps))
	for _, s := range steps {
		out = append(out, machineStep{Name: s.name, MS: s.dur.Milliseconds()})
	}
	steps = nil
	return out
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
