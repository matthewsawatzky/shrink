package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// isTerminal reports whether f is an interactive terminal.
// We only draw the live bar on a TTY so redirected output stays clean.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// ---- interrupt handling ----
//
// One process-wide handler covers however many ffmpegs are in flight:
// the first Ctrl+C interrupts them all (graceful SIGINT first, force
// kill 2s later if one stalls), deletes their partial output files -
// ffmpeg cannot resume, so leftovers are useless - and exits 130. Each
// run registers its ffmpeg so the handler knows what to stop.

var (
	interrupted atomic.Bool
	activeMu    sync.Mutex
	activeRuns  = map[*exec.Cmd]string{} // running ffmpeg -> its partial output
	runWG       sync.WaitGroup
)

// errCancelled marks a run ended by Ctrl+C. The interrupt handler owns
// cleanup and the exit; callers just unwind with this error.
var errCancelled = errors.New("cancelled")

func init() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		interrupted.Store(true)
		var partials []string
		activeMu.Lock()
		for cmd, out := range activeRuns {
			if cmd.Process != nil {
				cmd.Process.Signal(os.Interrupt)
				go func(c *exec.Cmd) {
					time.Sleep(2 * time.Second)
					if c.Process != nil {
						c.Process.Kill()
					}
				}(cmd)
			}
			if out != "" && fileExists(out) {
				os.Remove(out)
				partials = append(partials, out)
			}
		}
		activeMu.Unlock()
		runWG.Wait() // let every interrupted ffmpeg finish dying
		prefix := "shrink: "
		if m := cancelMark(); m != "" {
			prefix = m + " "
		}
		msg := "cancelled"
		if len(partials) == 1 {
			msg = fmt.Sprintf("cancelled - deleted partial %s", partials[0])
		} else if len(partials) > 1 {
			msg = fmt.Sprintf("cancelled - deleted %d partial files", len(partials))
		}
		fmt.Fprintf(os.Stderr, "\r\033[K\033[?25h%s%s\n", prefix, msg)
		os.Exit(130)
	}()
}

// registerRun ties a started ffmpeg and its partial output to the
// interrupt handler. unregisterRun must be called when it exits.
func registerRun(cmd *exec.Cmd, outPath string) {
	runWG.Add(1)
	activeMu.Lock()
	activeRuns[cmd] = outPath
	activeMu.Unlock()
}

func unregisterRun(cmd *exec.Cmd) {
	activeMu.Lock()
	delete(activeRuns, cmd)
	activeMu.Unlock()
	runWG.Done()
}

// parallel is set when a batch runs more than one worker at once:
// per-file reporting is held back until the run completes, so results
// can print in input order.
var parallel bool

// parallelTTY is set when that parallel batch is on an interactive
// terminal and draws the one shared progress line.
var parallelTTY bool

// progressSink is where an ffmpeg run's -progress stream goes: the live
// per-file bar, or one shared line when several files encode at once.
type progressSink interface {
	render(pct float64) // fraction complete, 0..1
	setSpeed(v float64) // ffmpeg speed, x realtime
	spin()              // duration unknown; keep the display alive
	markEnd()           // ffmpeg sent progress=end
	sawEnd() bool
	done() // the file finished successfully
}

// runFFmpeg returns the run's elapsed wall time along with any error,
// so the done message can include "took 12s". Used when no duration is
// known yet (still images); video/audio paths call
// runFFmpegWithDuration with the value from their earlier probe.
func runFFmpeg(label string, args ...string) (time.Duration, error) {
	input := inputPathFromArgs(args)
	probeDone := trackStep(input, "probe duration")
	total := probeDuration(input)
	probeDone()
	return runFFmpegWithDuration(label, total, args...)
}

// runFFmpegWithDuration is runFFmpeg with a known input duration (0 =
// unknown), skipping the per-run probe when the caller already has one.
// Interactive runs draw the live bar - or, in a parallel batch, the one
// shared line covering every in-flight file.
func runFFmpegWithDuration(label string, total float64, args ...string) (time.Duration, error) {
	if interrupted.Load() {
		return 0, errCancelled
	}
	start := time.Now()
	inputPath := inputPathFromArgs(args)
	outPath := outputArgFromArgs(args)
	if machine || !isTerminal(os.Stderr) {
		if err := runFFmpegQuiet(label, args, outPath); err != nil {
			return 0, err
		}
		recordStep(inputPath, "encode ("+label+")", time.Since(start))
		return time.Since(start), nil
	}

	cmd := exec.Command("ffmpeg", append([]string{
		"-hide_banner", "-loglevel", "error", "-nostats", "-nostdin", "-y",
		"-stats_period", "0.2", "-progress", "pipe:1",
	}, args...)...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("could not create progress pipe: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr

	var sink progressSink
	var stopDisplay func()
	if parallelTTY {
		active := liveBatch.start(filepath.Base(inputPath), total > 0)
		sink = active
		stopDisplay = func() { liveBatch.stop(active) }
	} else {
		fmt.Fprint(os.Stderr, "\033[?25l") // hide cursor
		sink = newBar(label, total)
		stopDisplay = func() { fmt.Fprint(os.Stderr, "\033[?25h\n") } // show cursor, newline after bar
	}
	defer stopDisplay()

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("could not start ffmpeg: %v", err)
	}
	registerRun(cmd, outPath)
	defer unregisterRun(cmd)

	// short files finish before a bar is even worth drawing
	if !parallelTTY && total > 0 && total < 2 {
		readErr := make(chan error, 1)
		go func() {
			defer stdout.Close()
			readErr <- drainProgress(stdout)
		}()
		waitErr := cmd.Wait()
		<-readErr
		return finishRun(stderr.String(), waitErr, args, start, label)
	}

	readErr := make(chan error, 1)
	go func() {
		defer stdout.Close()
		readErr <- trackProgress(sink, stdout, total)
	}()

	waitErr := cmd.Wait()
	trackErr := <-readErr

	if waitErr == nil && trackErr == nil && !sink.sawEnd() {
		// some builds/muxers never send progress=end; close out the
		// bar only on a genuinely successful run
		sink.done()
	}

	// on failure, wipe the bar line so the error isn't preceded by a 100%
	if waitErr != nil && !parallelTTY {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}

	if trackErr != nil {
		return 0, fmt.Errorf("progress read error: %v", trackErr)
	}
	return finishRun(stderr.String(), waitErr, args, start, label)
}

// finishRun turns a finished ffmpeg process into the return value of
// runFFmpegWithDuration: mapped error messages, recorded step time.
func finishRun(stderr string, waitErr error, args []string, start time.Time, label string) (time.Duration, error) {
	if interrupted.Load() {
		return 0, errCancelled
	}
	if waitErr != nil {
		msg := stderr
		enc := parseMissingEncoder(msg)
		if enc != "" {
			return 0, fmt.Errorf("your ffmpeg build is missing the %q encoder\ntry: brew install ffmpeg (full build), or pick a different output format", enc)
		}
		if strings.Contains(msg, "Output file does not contain any stream") {
			return 0, fmt.Errorf("the input has no audio track, so there is nothing to extract - use a video target instead (e.g. shrink %s mp4), or grab a frame (e.g. png)", inputPathFromArgs(args))
		}
		if strings.Contains(msg, "Error opening output file") {
			return 0, fmt.Errorf("ffmpeg could not write %q: %s", filepath.Ext(args[len(args)-1]), firstMeaningfulLine(msg))
		}
		if msg != "" {
			return 0, fmt.Errorf("ffmpeg failed: %s", strings.TrimRight(msg, "\n"))
		}
		return 0, fmt.Errorf("ffmpeg failed: %v", waitErr)
	}
	recordStep(inputPathFromArgs(args), "encode ("+label+")", time.Since(start))
	return time.Since(start), nil
}

// drainProgress consumes ffmpeg's -progress output without rendering a
// bar, so the pipe never fills and blocks the encode.
func drainProgress(stdout io.Reader) error {
	_, err := io.Copy(io.Discard, stdout)
	return err
}

// ---- shared batch line ----
//
// When several files encode at once, one line reports the whole run:
// how many files are done plus each in-flight file's progress.

// activeFile is one in-flight encode's slice of the shared line.
type activeFile struct {
	b     *batchBar
	name  string
	known bool // duration known → show a percentage

	pct   float64
	speed float64
}

func (a *activeFile) render(pct float64) {
	a.b.mu.Lock()
	a.pct = pct
	a.b.mu.Unlock()
	a.b.redraw()
}

func (a *activeFile) setSpeed(v float64) {
	a.b.mu.Lock()
	a.speed = v
	a.b.mu.Unlock()
}

func (a *activeFile) spin() { a.b.redraw() }

func (a *activeFile) markEnd()     {}
func (a *activeFile) sawEnd() bool { return true } // the shared line never draws a per-file final state
func (a *activeFile) done()        {}

// batchBar is the one shared progress line for a parallel folder run.
type batchBar struct {
	mu       sync.Mutex
	total    int
	done     int
	failed   int
	actives  []*activeFile
	lastDraw time.Time
}

var liveBatch = &batchBar{}

func (b *batchBar) begin(total int) {
	b.mu.Lock()
	b.total, b.done, b.failed = total, 0, 0
	b.actives = nil
	b.lastDraw = time.Time{}
	b.mu.Unlock()
}

func (b *batchBar) start(name string, known bool) *activeFile {
	a := &activeFile{b: b, name: name, known: known}
	b.mu.Lock()
	b.actives = append(b.actives, a)
	b.mu.Unlock()
	return a
}

func (b *batchBar) stop(a *activeFile) {
	b.mu.Lock()
	for i, x := range b.actives {
		if x == a {
			b.actives = append(b.actives[:i], b.actives[i+1:]...)
			break
		}
	}
	b.mu.Unlock()
}

// complete records a finished file and redraws.
func (b *batchBar) complete(ok bool) {
	b.mu.Lock()
	if ok {
		b.done++
	} else {
		b.failed++
	}
	b.mu.Unlock()
	b.redraw()
}

func (b *batchBar) redraw() {
	b.mu.Lock()
	if time.Since(b.lastDraw) < 100*time.Millisecond {
		b.mu.Unlock()
		return
	}
	b.lastDraw = time.Now()
	var sb strings.Builder
	sb.WriteString(ui.bold(fmt.Sprintf("%d/%d", b.done+b.failed, b.total)))
	sb.WriteString(ui.dim(" done"))
	if b.failed > 0 {
		sb.WriteString(ui.yellow(fmt.Sprintf(" %d failed", b.failed)))
	}
	shown := b.actives
	if len(shown) > 3 {
		shown = shown[:3]
	}
	for _, a := range shown {
		sb.WriteString(ui.dim(" · "))
		sb.WriteString(ui.cyan(a.name))
		if a.known {
			sb.WriteString(fmt.Sprintf(" %3d%%", int(a.pct*100+0.5)))
		}
		if a.speed > 0 {
			sb.WriteString(ui.dim(fmt.Sprintf(" %4.1fx", a.speed)))
		}
	}
	if n := len(b.actives) - len(shown); n > 0 {
		sb.WriteString(ui.dim(fmt.Sprintf(" · +%d", n)))
	}
	fmt.Fprintf(os.Stderr, "\r\033[K%s", sb.String())
	b.mu.Unlock()
}

// clear wipes the shared line so per-file results can print cleanly.
func (b *batchBar) clear() {
	fmt.Fprint(os.Stderr, "\r\033[K")
}

// outputArgFromArgs returns the last positional (non-flag) argument,
// which for our generated command lines is always the output file.
func outputArgFromArgs(args []string) string {
	for i := len(args) - 1; i >= 0; i-- {
		if !strings.HasPrefix(args[i], "-") {
			return args[i]
		}
	}
	return ""
}

// runFFmpegQuiet runs ffmpeg without any live display (machine mode or
// non-tty stderr) and maps failures to friendly messages. In machine
// mode ffmpeg's stderr is captured instead of passed through, so the
// JSON document stays the only thing on stdout.
func runFFmpegQuiet(label string, args []string, outPath string) error {
	if interrupted.Load() {
		return errCancelled
	}
	cmd := exec.Command("ffmpeg", append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, args...)...)
	var stderr strings.Builder
	if machine {
		cmd.Stderr = &stderr
	} else {
		cmd.Stderr = os.Stderr
	}
	if !machine {
		name := label
		if batchMode {
			// in folder mode several files can encode at once, so say
			// which file this line is for
			name = label + " " + filepath.Base(inputPathFromArgs(args))
		}
		fmt.Fprintf(os.Stderr, "%s\n", ui.cyan(name+"..."))
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	registerRun(cmd, outPath)
	defer unregisterRun(cmd)
	err := cmd.Wait()
	if interrupted.Load() {
		return errCancelled
	}
	if err == nil {
		return nil
	}
	if !machine {
		return err
	}
	msg := stderr.String()
	// keep stderr out of the JSON stream; hand back the mapped message
	if enc := parseMissingEncoder(msg); enc != "" {
		return fmt.Errorf("your ffmpeg build is missing the %q encoder\ntry: brew install ffmpeg (full build), or pick a different output format", enc)
	}
	if strings.Contains(msg, "Output file does not contain any stream") {
		return fmt.Errorf("the input has no audio track, so there is nothing to extract - use a video target instead (e.g. shrink %s mp4), or grab a frame (e.g. png)", inputPathFromArgs(args))
	}
	if strings.Contains(msg, "Error opening output file") {
		return fmt.Errorf("ffmpeg could not write %q: %s", filepath.Ext(args[len(args)-1]), firstMeaningfulLine(msg))
	}
	if msg != "" {
		return fmt.Errorf("ffmpeg failed: %s", strings.TrimRight(msg, "\n"))
	}
	return err
}

// trackProgress scans ffmpeg's `-progress` key=value stream and feeds
// the sink. It never prints a final state on its own: the caller
// decides whether the run counts as finished (done) or failed (erase).
func trackProgress(sink progressSink, stdout io.Reader, total float64) error {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "out_time_us="):
			if total <= 0 {
				sink.spin()
				continue
			}
			if us, ok := atof(strings.TrimPrefix(line, "out_time_us=")); ok {
				pct := us / 1e6 / total
				if pct > 1 {
					pct = 1
				}
				if pct < 0 {
					pct = 0
				}
				sink.render(pct)
			}
		case strings.HasPrefix(line, "speed="):
			if v, ok := atof(strings.TrimPrefix(line, "speed=")); ok {
				sink.setSpeed(v)
			}
		case line == "progress=end":
			sink.markEnd()
			sink.done()
		}
	}
	return scanner.Err()
}

// inputPathFromArgs finds the value of the first -i flag (the input file).
func inputPathFromArgs(args []string) string {
	for i, a := range args {
		if a == "-i" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// atof parses a plain decimal string (ffprobe/ffmpeg -progress values).
func atof(s string) (float64, bool) {
	var f float64
	_, err := fmt.Sscan(s, &f)
	return f, err == nil
}

// ---- progress bar ----

const barWidth = 28

// ANSI helpers: carriage return + "erase line" so a shrinking ETA
// ("1m34s left" → "34s left") doesn't leave residue like "leftft".
const (
	cr       = "\r"
	eraseEOL = "\x1b[K"
)

type bar struct {
	label      string  // e.g. "compressing", "compressing + scaling"
	total      float64 // seconds; 0 = unknown duration → spinner mode
	remain     float64 // seconds left, derived from latest pct
	speed      float64 // latest ffmpeg speed (x realtime)
	spinIdx    int     // spinner frame
	lastFiled  int     // last printed fill count (redraw throttle)
	lastPos    float64 // latest known fraction complete
	endSeen    bool    // ffmpeg sent "progress=end" for this run
	start      time.Time
	lastRedraw time.Time
}

func newBar(label string, total float64) *bar {
	return &bar{label: label, total: total, start: time.Now()}
}

// elapsed returns how long the ffmpeg run took (wall clock).
func (b *bar) elapsed() time.Duration { return time.Since(b.start) }

func (b *bar) setSpeed(v float64) { b.speed = v }

func (b *bar) markEnd()     { b.endSeen = true }
func (b *bar) sawEnd() bool { return b.endSeen }

func (b *bar) render(pct float64) {
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	b.lastPos = pct
	b.remain = (1 - pct) * b.total

	if time.Since(b.lastRedraw) < 100*time.Millisecond {
		return
	}

	filled := int(pct*barWidth + 0.5)
	if filled > barWidth {
		filled = barWidth
	}
	if filled == b.lastFiled {
		return
	}
	b.lastFiled = filled
	b.lastRedraw = time.Now()
	b.draw()
}

func (b *bar) draw() {
	filled := strings.Repeat("━", b.lastFiled)
	rest := strings.Repeat("─", barWidth-b.lastFiled)
	eta := ""
	if b.remain > 0.5 && b.speed > 0 {
		eta = ui.dim("  " + humanDuration(b.etaSeconds()) + " left")
	}
	speed := ""
	if b.speed > 0 {
		speed = ui.dim(fmt.Sprintf("  %4.1fx", b.speed))
	}
	fmt.Fprintf(os.Stderr, "%s%s  %s %s %s  %s%s%s",
		cr, eraseEOL,
		ui.cyan(b.label+"..."),
		ui.cyan(filled)+ui.dim(rest),
		ui.bold(fmt.Sprintf("%3d%%", int(b.lastPos*100+0.5))),
		ui.dim(humanDuration(b.elapsed().Seconds())), speed, eta)
}

func (b *bar) etaSeconds() float64 {
	if b.speed <= 0 {
		return 0
	}
	return b.remain / b.speed
}

func (b *bar) done() {
	if b.total > 0 {
		fmt.Fprintf(os.Stderr, "%s%s  %s %s  %s%s",
			cr, eraseEOL,
			ui.green("✓ "+b.label+"..."),
			ui.green(strings.Repeat("━", barWidth)),
			ui.bold("100%"), ui.dim("  "+humanDuration(b.elapsed().Seconds())))
		return
	}
	fmt.Fprintf(os.Stderr, "%s%s  %s", cr, eraseEOL, ui.green("✓ "+b.label+"... done"))
}

// spin shows an indeterminate spinner when duration is unknown.
func (b *bar) spin() {
	if time.Since(b.lastRedraw) < 120*time.Millisecond {
		return
	}
	b.lastRedraw = time.Now()
	spinner := []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}
	b.spinIdx = (b.spinIdx + 1) % len(spinner)
	speed := ""
	if b.speed > 0 {
		speed = ui.dim(fmt.Sprintf("  %4.1fx", b.speed))
	}
	fmt.Fprintf(os.Stderr, "%s%s  %s %s  %s%s",
		cr, eraseEOL,
		ui.cyan(b.label+"..."),
		ui.cyan(string(spinner[b.spinIdx])),
		ui.dim(humanDuration(b.elapsed().Seconds())), speed)
}

// humanDuration formats seconds as 420ms / 42s / 3m12s / 1h04m02s.
// Sub-5s times - and everything in verbose mode - keep millisecond
// resolution so quick runs don't all read "0s".
func humanDuration(seconds float64) string {
	if verbose || seconds < 5 {
		return formatMS(time.Duration(seconds * float64(time.Second)))
	}
	s := int(seconds + 0.5)
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	s %= 60
	if m < 60 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	h := m / 60
	m %= 60
	return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
}
