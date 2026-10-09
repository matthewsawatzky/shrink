package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
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

// interrupt state, shared by every ffmpeg run. The first Ctrl+C
// interrupts the current ffmpeg (graceful first, force kill 2s later
// if it stalls); registering once avoids per-run signal plumbing.
var (
	interrupted atomic.Bool
	interruptCh = make(chan os.Signal, 1)
)

// watchInterrupt arms interrupt forwarding to cmd and reports whether a
// Ctrl+C arrived while it ran. The returned stop function disarms it.
func watchInterrupt(cmd *exec.Cmd) (fired func() bool, stop func()) {
	signal.Notify(interruptCh, os.Interrupt)
	go func() {
		select {
		case <-interruptCh:
			interrupted.Store(true)
			if cmd.Process != nil {
				cmd.Process.Signal(os.Interrupt)
				go func() {
					time.Sleep(2 * time.Second)
					cmd.Process.Kill()
				}()
			}
		case <-done:
		}
	}()
	return interrupted.Load, func() {
		signal.Stop(interruptCh)
		close(done)
	}
}

// runFFmpeg returns the run's elapsed wall time along with any error,
// so the done message can include "took 12s". Used when no duration is
// known yet (still images); video/audio paths call
// runFFmpegWithDuration with the value from their earlier probe.
// A Ctrl+C during the run kills ffmpeg and deletes the partial output
// file (ffmpeg cannot resume, so leftovers are useless).
func runFFmpeg(label string, args ...string) (time.Duration, error) {
	probeDone := trackStep("probe duration")
	total := probeDuration(inputPathFromArgs(args))
	probeDone()
	return runFFmpegWithDuration(label, total, args...)
}

// runFFmpegWithDuration is runFFmpeg with a known input duration (0 =
// unknown), skipping the per-run probe when the caller already has one.
func runFFmpegWithDuration(label string, total float64, args ...string) (time.Duration, error) {
	start := time.Now()
	outPath := outputArgFromArgs(args)
	if machine {
		// no bar in machine mode; ffmpeg's errors still reach stderr
		if err := runFFmpegQuiet(label, args, outPath); err != nil {
			return 0, err
		}
		recordStep("encode ("+label+")", time.Since(start))
		return time.Since(start), nil
	}
	if !isTerminal(os.Stderr) {
		if err := runFFmpegQuiet(label, args, outPath); err != nil {
			return 0, err
		}
		recordStep("encode ("+label+")", time.Since(start))
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

	fmt.Fprint(os.Stderr, "\033[?25l") // hide cursor
	defer func() {
		fmt.Fprint(os.Stderr, "\033[?25h\n") // show cursor, newline after bar
	}()

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("could not start ffmpeg: %v", err)
	}

	interruptedNow, stopWatch := watchInterrupt(cmd)
	defer stopWatch()

	// short files finish before a bar is even worth drawing
	if total > 0 && total < 2 {
		bar := newBar(label, total)
		bar.skip = true
		readErr := make(chan error, 1)
		go func() {
			defer stdout.Close()
			readErr <- drainProgress(stdout)
		}()
		waitErr := cmd.Wait()
		<-readErr
		return finishRun(stderr.String(), waitErr, outPath, args, interruptedNow, start, label)
	}

	bar := newBar(label, total)
	readErr := make(chan error, 1)
	go func() {
		defer stdout.Close()
		readErr <- trackProgress(bar, stdout, total)
	}()

	waitErr := cmd.Wait()
	trackErr := <-readErr

	// Ctrl+C takes priority, whatever ffmpeg's exit code was
	if interruptedNow() {
		fmt.Fprint(os.Stderr, "\033[?25h\n") // defers don't run on os.Exit
		deletePartial(outPath)
		os.Exit(130)
	}

	if waitErr == nil && trackErr == nil && !bar.sawEnd {
		// some builds/muxers never send progress=end; close out the
		// bar only on a genuinely successful run
		bar.done()
	}

	// on failure, wipe the bar line so the error isn't preceded by a 100%
	if waitErr != nil {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}

	if trackErr != nil {
		return 0, fmt.Errorf("progress read error: %v", trackErr)
	}
	return finishRun(stderr.String(), waitErr, outPath, args, interruptedNow, start, label)
}

// finishRun turns a finished ffmpeg process into the return value of
// runFFmpegWithDuration: mapped error messages, recorded step time.
func finishRun(stderr string, waitErr error, outPath string, args []string, interruptedNow func() bool, start time.Time, label string) (time.Duration, error) {
	// Ctrl+C takes priority, whatever ffmpeg's exit code was
	if interruptedNow() {
		deletePartial(outPath)
		fmt.Fprint(os.Stderr, "\033[?25h\n") // defers don't run on os.Exit
		os.Exit(130)
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
	recordStep("encode ("+label+")", time.Since(start))
	return time.Since(start), nil
}

// drainProgress consumes ffmpeg's -progress output without rendering a
// bar, so the pipe never fills and blocks the encode.
func drainProgress(stdout io.Reader) error {
	_, err := io.Copy(io.Discard, stdout)
	return err
}

// deletePartial removes the unfinished output file after a cancel.
// ffmpeg cannot resume, so a partial file is useless. The leading
// wipe replaces any half-drawn progress bar with the cancel message.
func deletePartial(outPath string) {
	prefix := "shrink: "
	if m := cancelMark(); m != "" {
		prefix = m + " "
	}
	msg := "cancelled"
	if outPath != "" && fileExists(outPath) {
		if err := os.Remove(outPath); err == nil {
			msg = fmt.Sprintf("cancelled - deleted partial %s", outPath)
		} else {
			msg = fmt.Sprintf("cancelled - could not delete partial %s: %v", outPath, err)
		}
	}
	fmt.Fprintf(os.Stderr, "\r\033[K%s%s\n", prefix, msg)
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

// runFFmpegQuiet runs ffmpeg without the bar (non-tty) and pipes ffmpeg's
// stderr directly to ours, like the original simple behavior. Ctrl+C still
// cleans up the partial output file.
func runFFmpegQuiet(label string, args []string, outPath string) error {
	cmd := exec.Command("ffmpeg", append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, args...)...)
	cmd.Stderr = os.Stderr
	if !machine {
		fmt.Fprintf(os.Stderr, "%s\n", ui.cyan(label+"..."))
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	w, stopWatch := watchCancel(cmd)
	defer stopWatch()
	err := cmd.Wait()
	if w.interrupted.Load() {
		deletePartial(outPath)
		os.Exit(130)
	}
	return err
}

// trackProgress scans ffmpeg's `-progress` key=value stream and renders
// the shared bar. It never prints a final state on its own: the caller
// decides whether the run counts as finished (bar.done) or failed (erase).
func trackProgress(bar *bar, stdout io.Reader, total float64) error {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "out_time_us="):
			if bar.total <= 0 {
				bar.spin()
				continue
			}
			if us, ok := atof(strings.TrimPrefix(line, "out_time_us=")); ok {
				pct := us / 1e6 / bar.total
				if pct > 1 {
					pct = 1
				}
				if pct < 0 {
					pct = 0
				}
				bar.render(pct)
			}
		case strings.HasPrefix(line, "speed="):
			if v, ok := atof(strings.TrimPrefix(line, "speed=")); ok {
				bar.setSpeed(v)
			}
		case line == "progress=end":
			bar.sawEnd = true
			bar.done()
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

// totalDuration returns the duration of path in seconds (0 if unknown). Uses ffprobe.
func totalDuration(path string) float64 {
	if path == "" {
		return 0
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		return 0
	}
	if v, ok := atof(strings.TrimSpace(string(out))); ok {
		return v
	}
	return 0
}

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
	sawEnd     bool    // ffmpeg sent "progress=end" for this run
	start      time.Time
	lastRedraw time.Time
}

func newBar(label string, total float64) *bar {
	return &bar{label: label, total: total, start: time.Now()}
}

// elapsed returns how long the ffmpeg run took (wall clock).
func (b *bar) elapsed() time.Duration { return time.Since(b.start) }

func (b *bar) setSpeed(v float64) { b.speed = v }

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
