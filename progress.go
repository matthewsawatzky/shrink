package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

// errCancelled is returned after the child has stopped and its temporary
// output has been removed. The application boundary selects exit code 130.
var errCancelled = errors.New("cancelled")

type progressSink interface {
	render(pct float64) // fraction complete, 0..1
	setSpeed(v float64) // ffmpeg speed, x realtime
	spin()              // duration unknown; keep the display alive
	markEnd()           // ffmpeg sent progress=end
	sawEnd() bool
	done() // the file finished successfully
}

// encodeJob owns the complete output lifecycle. ffmpeg only writes a private
// sibling file; a verified result is published after the child has stopped.
func (a *application) encodeJob(input, output, label string, total float64, args []string) (time.Duration, error) {
	if a.ctx.Err() != nil {
		return 0, errCancelled
	}
	start := time.Now()
	tmp, err := os.CreateTemp(filepath.Dir(output), ".shrink-*"+filepath.Ext(output))
	if err != nil {
		return 0, fmt.Errorf("create temporary output: %w", err)
	}
	tempPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		os.Remove(tempPath)
		return 0, err
	}
	defer os.Remove(tempPath)
	argv := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, args...)
	argv = append(argv, tempPath)
	var sink progressSink
	stopDisplay := func() {}
	interactive := !a.machine && writerIsTerminal(a.stderr)
	if interactive {
		if a.parallelTTY {
			active := a.liveBatch.start(filepath.Base(input), total > 0)
			sink = active
			stopDisplay = func() { a.liveBatch.stop(active) }
		} else {
			fmt.Fprint(a.stderr, "\033[?25l")
			sink = a.newBar(label, total)
			stopDisplay = func() { fmt.Fprint(a.stderr, "\033[?25h\n") }
		}
		argv = append([]string{"-nostats", "-stats_period", "0.2", "-progress", "pipe:1"}, argv...)
	} else if !a.machine {
		name := label
		if a.batchMode {
			name += " " + filepath.Base(input)
		}
		fmt.Fprintln(a.stderr, a.ui.cyan(name+"..."))
	}
	defer stopDisplay()
	cmd := a.command("ffmpeg", argv...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	var stdout io.ReadCloser
	if interactive {
		stdout, err = cmd.StdoutPipe()
		if err != nil {
			return 0, err
		}
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("could not start ffmpeg: %w", err)
	}
	// Read the pipe to EOF before Wait closes it. This avoids losing the final
	// progress packet or spuriously reporting a closed-pipe error.
	var readErr error
	if stdout != nil {
		readErr = trackProgress(sink, stdout, total)
		stdout.Close()
	}
	waitErr := cmd.Wait()
	if a.ctx.Err() != nil {
		return 0, errCancelled
	}
	if _, err := a.finishRun(stderr.String(), waitErr, input, output, start, label); err != nil {
		return 0, err
	}
	if readErr != nil {
		return 0, fmt.Errorf("progress read error: %w", readErr)
	}
	if err := a.verifyEncoded(tempPath, total, args); err != nil {
		return 0, err
	}
	if a.ctx.Err() != nil {
		return 0, errCancelled
	}
	if a.overwrite {
		err = os.Rename(tempPath, output)
	} else {
		err = os.Link(tempPath, output)
	}
	if err != nil {
		return 0, fmt.Errorf("could not publish %s (existing files preserved): %w", output, err)
	}
	if sink != nil {
		sink.done()
	}
	return time.Since(start), nil
}

// verifyEncoded rejects empty, unreadable, truncated, or wrong-codec output
// even when a child exits zero. It runs before publication or original deletion.
func (a *application) verifyEncoded(path string, total float64, args []string) error {
	stat, err := os.Stat(path)
	if err != nil || stat.Size() == 0 {
		return fmt.Errorf("ffmpeg produced no usable output")
	}
	info := a.probeFile(path)
	if !info.ok {
		return fmt.Errorf("ffmpeg output could not be verified")
	}
	for i := 0; i+1 < len(args); i++ {
		codec := args[i+1]
		if codec == "copy" {
			continue
		}
		if args[i] == "-c:v" && info.videoCodec != probeCodec(codec) {
			return fmt.Errorf("output video codec does not match requested encoder")
		}
		if args[i] == "-c:a" && info.hasAudio && info.audioCodec != probeCodec(codec) {
			return fmt.Errorf("output audio codec does not match requested encoder")
		}
	}
	if total > 0 && math.Abs(info.duration-total) > math.Max(0.5, total*0.05) {
		return fmt.Errorf("output duration does not match the planned timeline")
	}
	return nil
}

// finishRun turns a finished ffmpeg process into the return value of
// runFFmpegWithDuration: mapped error messages, recorded step time.
func (a *application) finishRun(stderr string, waitErr error, input, output string, start time.Time, label string) (time.Duration, error) {
	if a.ctx.Err() != nil {
		return 0, errCancelled
	}
	if waitErr != nil {
		msg := stderr
		enc := parseMissingEncoder(msg)
		if enc != "" {
			return 0, fmt.Errorf("your ffmpeg build is missing the %q encoder\ntry: brew install ffmpeg (full build), or pick a different output format", enc)
		}
		if strings.Contains(msg, "Output file does not contain any stream") {
			return 0, fmt.Errorf("the input has no audio track, so there is nothing to extract - use a video target instead (e.g. shrink %s mp4), or grab a frame (e.g. png)", input)
		}
		if strings.Contains(msg, "Error opening output file") {
			return 0, fmt.Errorf("ffmpeg could not write %q: %s", filepath.Ext(output), firstMeaningfulLine(msg))
		}
		if msg != "" {
			return 0, fmt.Errorf("ffmpeg failed: %s", strings.TrimRight(msg, "\n"))
		}
		return 0, fmt.Errorf("ffmpeg failed: %v", waitErr)
	}
	a.recordStep(input, "encode ("+label+")", time.Since(start))
	return time.Since(start), nil
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
	app      *application
	mu       sync.Mutex
	total    int
	done     int
	failed   int
	actives  []*activeFile
	lastDraw time.Time
}

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
	sb.WriteString(b.app.ui.bold(fmt.Sprintf("%d/%d", b.done+b.failed, b.total)))
	sb.WriteString(b.app.ui.dim(" done"))
	if b.failed > 0 {
		sb.WriteString(b.app.ui.yellow(fmt.Sprintf(" %d failed", b.failed)))
	}
	shown := b.actives
	if len(shown) > 3 {
		shown = shown[:3]
	}
	for _, a := range shown {
		sb.WriteString(b.app.ui.dim(" · "))
		sb.WriteString(b.app.ui.cyan(a.name))
		if a.known {
			sb.WriteString(fmt.Sprintf(" %3d%%", int(a.pct*100+0.5)))
		}
		if a.speed > 0 {
			sb.WriteString(b.app.ui.dim(fmt.Sprintf(" %4.1fx", a.speed)))
		}
	}
	if n := len(b.actives) - len(shown); n > 0 {
		sb.WriteString(b.app.ui.dim(fmt.Sprintf(" · +%d", n)))
	}
	fmt.Fprintf(b.app.stderr, "\r\033[K%s", sb.String())
	b.mu.Unlock()
}

// clear wipes the shared line so per-file results can print cleanly.
func (b *batchBar) clear() {
	fmt.Fprint(b.app.stderr, "\r\033[K")
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
		}
	}
	return scanner.Err()
}

// atof parses a plain decimal string (ffprobe/ffmpeg -progress values).
func atof(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
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
	app        *application
	label      string  // e.g. "compressing", "compressing + scaling"
	total      float64 // seconds; 0 = unknown duration → spinner mode
	remain     float64 // seconds left, derived from latest pct
	speed      float64 // latest ffmpeg speed (x realtime)
	spinIdx    int     // spinner frame
	lastFilled int     // last printed fill count (redraw throttle)
	lastPos    float64 // latest known fraction complete
	endSeen    bool    // ffmpeg sent "progress=end" for this run
	start      time.Time
	lastRedraw time.Time
}

func (a *application) newBar(label string, total float64) *bar {
	return &bar{app: a, label: label, total: total, start: time.Now()}
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
	if filled == b.lastFilled {
		return
	}
	b.lastFilled = filled
	b.lastRedraw = time.Now()
	b.draw()
}

func (b *bar) draw() {
	filled := strings.Repeat("━", b.lastFilled)
	rest := strings.Repeat("─", barWidth-b.lastFilled)
	eta := ""
	if b.remain > 0.5 && b.speed > 0 {
		eta = b.app.ui.dim("  " + b.app.humanDuration(b.etaSeconds()) + " left")
	}
	speed := ""
	if b.speed > 0 {
		speed = b.app.ui.dim(fmt.Sprintf("  %4.1fx", b.speed))
	}
	fmt.Fprintf(b.app.stderr, "%s%s  %s %s %s  %s%s%s",
		cr, eraseEOL,
		b.app.ui.cyan(b.label+"..."),
		b.app.ui.cyan(filled)+b.app.ui.dim(rest),
		b.app.ui.bold(fmt.Sprintf("%3d%%", int(b.lastPos*100+0.5))),
		b.app.ui.dim(b.app.humanDuration(b.elapsed().Seconds())), speed, eta)
}

func (b *bar) etaSeconds() float64 {
	if b.speed <= 0 {
		return 0
	}
	return b.remain / b.speed
}

func (b *bar) done() {
	if b.total > 0 {
		fmt.Fprintf(b.app.stderr, "%s%s  %s %s  %s%s",
			cr, eraseEOL,
			b.app.ui.green("✓ "+b.label+"..."),
			b.app.ui.green(strings.Repeat("━", barWidth)),
			b.app.ui.bold("100%"), b.app.ui.dim("  "+b.app.humanDuration(b.elapsed().Seconds())))
		return
	}
	fmt.Fprintf(b.app.stderr, "%s%s  %s", cr, eraseEOL, b.app.ui.green("✓ "+b.label+"... done"))
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
		speed = b.app.ui.dim(fmt.Sprintf("  %4.1fx", b.speed))
	}
	fmt.Fprintf(b.app.stderr, "%s%s  %s %s  %s%s",
		cr, eraseEOL,
		b.app.ui.cyan(b.label+"..."),
		b.app.ui.cyan(string(spinner[b.spinIdx])),
		b.app.ui.dim(b.app.humanDuration(b.elapsed().Seconds())), speed)
}

// humanDuration formats seconds as 420ms / 42s / 3m12s / 1h04m02s.
// Sub-5s times - and everything in verbose mode - keep millisecond
// resolution so quick runs don't all read "0s".
func (a *application) humanDuration(seconds float64) string {
	if a.verbose || seconds < 5 {
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
