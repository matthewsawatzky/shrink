package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"time"
)

// application owns mutable state for one invocation. Independent invocations
// have independent flags, output claims, encoder caches, timings and reporters.
type application struct {
	dataDir             string
	workerExecutable    string
	cpuThreads          int
	outputTemp          string
	queued, lowPriority bool
	cpuFactory          func() cpuController
	queueExecutor       func(context.Context, queueJob, int, bool) (*fileReport, error)
	ctx                 context.Context
	stdout, stderr      io.Writer
	ui                  styler
	fast                bool
	dryRun              bool
	overwrite           bool
	deleteAfter         bool
	verbose             bool
	machine             bool
	parallel            bool
	parallelTTY         bool
	batchMode           bool
	progStart           time.Time
	reservedMu          sync.Mutex
	reservedOut         map[string]bool
	stepsMu             sync.Mutex
	stepsByFile         map[string][]step
	machineFiles        []machineFile
	machineSkipped      map[string]int
	machineDryRun       bool
	machineNote         string
	machineAlreadyDone  int
	liveBatch           *batchBar
	ffmpegEncoders      func() map[string]bool
}

func newApplication(ctx context.Context, stdout, stderr io.Writer) *application {
	a := &application{ctx: ctx, stdout: stdout, stderr: stderr, progStart: time.Now(), reservedOut: map[string]bool{}, stepsByFile: map[string][]step{}, machineSkipped: map[string]int{}}
	if f, ok := stderr.(*os.File); ok {
		a.ui = newStyler(f)
	}
	a.liveBatch = &batchBar{app: a}
	a.ffmpegEncoders = sync.OnceValue(func() map[string]bool {
		out, err := a.command("ffmpeg", "-hide_banner", "-encoders").Output()
		available := map[string]bool{}
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if f := strings.Fields(line); len(f) >= 2 {
					available[f[1]] = true
				}
			}
		}
		return available
	})
	return a
}

type exitCode int

func exitCLI(code int) { panic(exitCode(code)) }

// run is the testable exit boundary. All defers, including output cleanup,
// run before a status is returned. Unknown panics are never swallowed.
func (a *application) run(args []string) (code int) {
	defer func() {
		if r := recover(); r != nil {
			if status, ok := r.(exitCode); ok {
				code = int(status)
			} else {
				panic(r)
			}
		}
		if a.ctx.Err() != nil {
			if writerIsTerminal(a.stderr) {
				fmt.Fprint(a.stderr, "\r\033[K\033[?25h")
			}
			fmt.Fprintln(a.stderr, "shrink: cancelled")
			code = 130
		}
	}()
	a.runArgs(args)
	return 0
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := 0
	if len(os.Args) > 1 && os.Args[1] == "_queue-ffmpeg" {
		code = queueFFmpegHelper(ctx, os.Args[2:])
	} else {
		code = newApplication(ctx, os.Stdout, os.Stderr).run(os.Args[1:])
	}
	stop()
	os.Exit(code)
}

// CommandContext closes the startup/cancellation race. Children get a graceful
// interrupt where supported, then WaitDelay forces termination after two seconds.
func (a *application) command(name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(a.ctx, name, args...)
	cmd.Cancel = func() error {
		if runtime.GOOS == "windows" {
			return cmd.Process.Kill()
		}
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

func writerIsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isTerminal(f)
}
