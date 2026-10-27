package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// version is the shrink version, stamped in at release time via
// -ldflags "-X main.version=v1.2.3"; local builds report "dev".
var version = "dev"

type levelSpec struct {
	videoCRF     string // libx264 CRF
	hevcCRF      string // libx265 CRF (its quality scale differs from x264)
	vp9CRF       string // libvpx-vp9 CRF
	av1CRF       string // libsvtav1 CRF
	audioBitrate string
	imageQuality string // quality scale for mjpeg (2-31, higher = smaller)
	webpQuality  string // libwebp -quality (0-100)
	vorbisQ      string // libvorbis -q:a (0-10)
}

// Preset compression levels shared by video, image and audio encoders.
// Higher CRF = heavier compression = smaller file + lower quality.
var compressionLevels = map[string]levelSpec{
	"compress":   {videoCRF: "23", hevcCRF: "28", av1CRF: "32", audioBitrate: "128k", imageQuality: "4", webpQuality: "80", vorbisQ: "5"},
	"spacesaver": {videoCRF: "28", hevcCRF: "32", av1CRF: "40", audioBitrate: "96k", imageQuality: "7", webpQuality: "65", vorbisQ: "3"},
	"neversee":   {videoCRF: "34", hevcCRF: "38", av1CRF: "48", audioBitrate: "64k", imageQuality: "12", webpQuality: "40", vorbisQ: "1"},
}

// transparentLevel is the near-lossless grade used when a run performs
// only the operations it was given (a scale, a speed factor, a format
// conversion, an audio extraction) with no compression level. Compressing
// would stack quality losses on work the user didn't ask for; audio that
// can be copied is copied outright.
var transparentLevel = levelSpec{
	videoCRF: "18", hevcCRF: "22", vp9CRF: "28", av1CRF: "30",
	audioBitrate: "256k", imageQuality: "2", webpQuality: "95", vorbisQ: "10",
}

// Resolution map: height -> scale filter args. The min() cap never
// upscales - a smaller input just keeps its size (upscaling only makes
// a bigger file with no added detail) - and mod() keeps odd input
// heights even, which x264 and friends require. -2 keeps width even.
var resolutions = map[string]string{
	"340p":  `scale=-2:min(340\,ih-mod(ih\,2))`,
	"480p":  `scale=-2:min(480\,ih-mod(ih\,2))`,
	"720p":  `scale=-2:min(720\,ih-mod(ih\,2))`,
	"1080p": `scale=-2:min(1080\,ih-mod(ih\,2))`,
	"1440p": `scale=-2:min(1440\,ih-mod(ih\,2))`,
	"4k":    `scale=-2:min(2160\,ih-mod(ih\,2))`,
}

func (a *application) usage() {
	out := a.stderr
	fmt.Fprintf(out, "%s %s\n\n", a.ui.bold("shrink"), a.ui.dim("- compress images, video and audio with ffmpeg"))
	fmt.Fprintf(out, "  %s\n", a.ui.bold("usage: shrink <input> [target...]"))
	for _, spec := range a.commands() {
		fmt.Fprintf(out, "  %s\n", a.ui.bold("       shrink "+spec.usage))
	}
	fmt.Fprintf(out, "%s\n", a.ui.dim("  folder mode converts every matching file in place. Give one format per"))
	fmt.Fprintf(out, "%s\n\n", a.ui.dim("  media kind (e.g. \"jpeg mp4 mp3\"); levels apply to all media. Add -r for subfolders."))
	fmt.Fprintf(out, "%s\n", a.ui.bold("targets:"))
	for _, t := range []struct{ name, desc string }{
		{"<resolution>", "340p, 480p, 720p, 1080p, 1440p, 4k"},
		{"<format>", strings.Join(outputFormats("image"), ", ") + " | " + strings.Join(outputFormats("video"), ", ")},
		{"<audio>", strings.Join(outputFormats("audio"), ", ")},
		{"<speed>", "x2, x5, x0.1 ... change video/audio playback speed"},
		{"<level>", "compress, spacesaver, neversee"},
	} {
		fmt.Fprintf(out, "  %s  %s\n", a.ui.cyan(fmt.Sprintf("%-12s", t.name)), t.desc)
	}
	fmt.Fprintf(out, "\n%s\n", a.ui.bold("flags:"))
	for _, f := range []struct{ name, desc string }{
		{"-h, --help", "show usage and exit"},
		{"-v, --verbose", "show step timings, and use ms and KB everywhere"},
		{"-r, --recursive", "with a folder, also scan subfolders"},
		{"-j, --jobs N", "folder mode: encode N files at once (default 2)"},
		{"-f, --fast", "prefer hardware video encoders where available"},
		{"-n, --dry-run", "show what would happen without encoding anything"},
		{"-o, --overwrite", "redo outputs that already exist"},
		{"-d, --delete", "delete the original once a smaller output exists"},
		{"-m, --machine", "emit JSON instead of human output (for scripts/LLMs)"},
		{"-c, --completions", "print shell completions (zsh or bash) and exit"},
		{"-V, --version", "print the version and exit"},
	} {
		fmt.Fprintf(out, "  %s  %s\n", a.ui.cyan(fmt.Sprintf("%-12s", f.name)), f.desc)
	}
	fmt.Fprintf(out, "\n%s\n", a.ui.dim("  speed factors over 1 speed up, under 1 slow down: x2 plays twice as fast, x0.1 ten times slower."))
	fmt.Fprintf(out, "\n%s\n", a.ui.bold("examples:"))
	for _, ex := range []string{
		"shrink vid.mkv webm spacesaver",
		"shrink pic.heic 1080p jpeg neversee",
		"shrink clip.mp4 mp3",
		"shrink clip.mp4 x5",
		"shrink clip.mp4 x0.1 spacesaver",
		"shrink folder ~/Media jpeg mp4 mp3 spacesaver",
		"shrink make mp4 red 1080p 60fps 30s",
	} {
		fmt.Fprintf(out, "  %s\n", a.ui.dim(ex))
	}
	fmt.Fprintf(out, "\n%s\n", a.ui.bold("compression levels:"))
	for _, l := range []struct{ name, desc string }{
		{"compress", "decent quality, solid savings"},
		{"spacesaver", "noticeably compressed"},
		{"neversee", "very compressed"},
	} {
		fmt.Fprintf(out, "  %s  %s\n", a.ui.cyan(fmt.Sprintf("%-12s", l.name)), l.desc)
	}
	fmt.Fprintf(out, "\n%s\n", a.ui.dim("  A bare shrink <file> run compresses (the compress level). Any specific operation -"))
	fmt.Fprintf(out, "%s\n", a.ui.dim("  scale, speed, format, audio extraction - runs alone at near-lossless quality: pass"))
	fmt.Fprintf(out, "%s\n", a.ui.dim("  a level to compress on top of it."))
	for _, t := range []struct{ name, desc string }{
		{"<format>", "mp4, jpeg, wav ... - the format picks the kind of thing made"},
		{"<content>", "a color name (red, blue, gray...), bars, test, noise; sine for audio"},
		{"<fps>", "24fps, 25fps, 30fps, 50fps, 60fps (default), 120fps"},
		{"<duration>", "Ns or Nm, e.g. 10s, 2m (default 10s)"},
		{"<hz>", "audio tone frequency, e.g. 440hz (default)"},
	} {
		fmt.Fprintf(out, "  %s  %s\n", a.ui.cyan(fmt.Sprintf("%-12s", t.name)), t.desc)
	}
}

// Extensions recognized per media kind. Also drives the unsupported-type
// error, so what users are shown can't drift from what we handle.
var mediaExtensions = map[string][]string{
	"video": {".mp4", ".mkv", ".webm", ".mov", ".avi", ".m4v", ".mpg", ".mpeg", ".flv", ".wmv", ".ts"},
	"image": {".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp", ".tiff", ".heic", ".heif", ".avif"},
	"audio": {".mp3", ".aac", ".m4a", ".opus", ".ogg", ".wav", ".flac", ".wma", ".aiff"},
}

// stable order for display (map iteration is random)
var mediaKinds = []string{"video", "image", "audio"}

// extKind maps a lowercase ".ext" to its media kind, precomputed once
// from mediaExtensions so per-file lookups stay a single map access.
var extKind = buildExtKind()

func buildExtKind() map[string]string {
	m := make(map[string]string)
	for _, kind := range mediaKinds {
		for _, e := range mediaExtensions[kind] {
			m[e] = kind
		}
	}
	return m
}

// kindForExt maps a ".ext" (any case) to its media kind, or "" if unknown.
func kindForExt(ext string) string {
	return extKind[strings.ToLower(ext)]
}

func detectKind(path string) string {
	if kind := kindForExt(filepath.Ext(path)); kind != "" {
		return kind
	}
	return "unknown"
}

// sortedKeys returns a map's keys in a stable order for display.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// outputFormats lists the formats shrink can encode to, with pure aliases
// dropped ("jpg" from "jpeg", "aac" from "m4a") so the list reads cleanly.
func outputFormats(kind string) []string {
	var names []string
	switch kind {
	case "video":
		names = sortedKeys(videoFormats)
	case "image":
		names = sortedKeys(imageFormats)
	case "audio":
		names = sortedKeys(audioFormats)
	}
	kept := names[:0]
	for _, n := range names {
		if formatsForKind(kind)[n].aliasOf == "" {
			kept = append(kept, n)
		}
	}
	return kept
}

// unknownTargetError explains a rejected target. Input-only extensions
// (aiff, wma, heic...) get their own message, so seeing them in the input
// list never reads as "why not this?"; anything else lists valid targets.
func (a *application) unknownTargetError(arg string) {
	ext := "." + strings.TrimPrefix(strings.ToLower(arg), ".")
	if kind := kindForExt(ext); kind != "" {
		if a.machine {
			a.exitMachineError(machineError{
				Error:        fmt.Sprintf("%s is a valid %s input, but shrink cannot write %s", arg, kind, arg),
				ValidTargets: outputFormats(kind),
			})
		}
		fmt.Fprintf(a.stderr, "%s %s\n", a.errMark(),
			a.ui.red(fmt.Sprintf("%s is a valid %s input, but shrink cannot write %s", arg, kind, arg)))
		fmt.Fprintf(a.stderr, "  %s %s\n", a.ui.dim(kind+" outputs:"), strings.Join(outputFormats(kind), ", "))
		exitCLI(1)
	}
	if a.machine {
		var valid []string
		valid = append(valid, "340p", "480p", "720p", "1080p", "1440p", "4k")
		valid = append(valid, "compress", "spacesaver", "neversee")
		for _, kind := range mediaKinds {
			valid = append(valid, outputFormats(kind)...)
		}
		a.exitMachineError(machineError{Error: "unknown target: " + arg, ValidTargets: valid})
	}
	fmt.Fprintf(a.stderr, "%s %s\n", a.errMark(), a.ui.red("unknown target: "+arg))
	fmt.Fprintf(a.stderr, "  %s\n", a.ui.dim("valid targets:"))
	fmt.Fprintf(a.stderr, "    %s  %s\n", a.ui.cyan(fmt.Sprintf("%-10s", "resolution")), "340p, 480p, 720p, 1080p, 1440p, 4k")
	fmt.Fprintf(a.stderr, "    %s  %s\n", a.ui.cyan(fmt.Sprintf("%-10s", "levels")), "compress, spacesaver, neversee")
	for _, kind := range mediaKinds {
		fmt.Fprintf(a.stderr, "    %s  %s\n", a.ui.cyan(fmt.Sprintf("%-10s", kind)), strings.Join(outputFormats(kind), ", "))
	}
	exitCLI(1)
}

// unsupportedTypeError names the offending file type and lists every type
// shrink can read, so the gap is obvious without consulting --help.
func (a *application) unsupportedTypeError(input string) {
	what := strings.ToLower(filepath.Ext(input))
	if what == "" {
		what = "no extension"
	}
	if a.machine {
		var valid []string
		for _, kind := range mediaKinds {
			valid = append(valid, mediaExtensions[kind]...)
		}
		a.exitMachineError(machineError{
			Error:       "unsupported file type: " + what,
			Input:       input,
			ValidInputs: valid,
		})
	}
	fmt.Fprintf(a.stderr, "%s %s\n", a.errMark(), a.ui.red("unsupported file type: "+what))
	fmt.Fprintf(a.stderr, "  %s\n", a.ui.dim("shrink reads:"))
	for _, kind := range mediaKinds {
		var exts []string
		for _, e := range mediaExtensions[kind] {
			exts = append(exts, strings.TrimPrefix(e, "."))
		}
		fmt.Fprintf(a.stderr, "    %s  %s\n", a.ui.cyan(fmt.Sprintf("%-5s", kind)), strings.Join(exts, ", "))
	}
	exitCLI(1)
}

func isTargetResolution(s string) bool { _, ok := resolutions[s]; return ok }
func isTargetVideo(s string) bool      { _, ok := videoFormats[s]; return ok }
func isTargetImage(s string) bool      { _, ok := imageFormats[s]; return ok }
func isTargetAudio(s string) bool      { _, ok := audioFormats[s]; return ok }
func isTargetCompression(s string) bool {
	_, ok := compressionLevels[s]
	return ok
}

// parseSpeed parses a speed target "x<number>" (x5, x0.1, x2.5), returning
// the factor (>0). ok=false for anything x-shaped but malformed.
func parseSpeed(arg string) (speed float64, tag string, ok bool) {
	t := strings.ToLower(arg)
	if !strings.HasPrefix(t, "x") {
		return 0, "", false
	}
	v, err := strconv.ParseFloat(t[1:], 64)
	if err != nil || v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) || math.IsInf(1/v, 0) {
		return 0, "", false
	}
	return v, t, true
}

// isSpeedFactor reports whether arg is a well-formed speed target whose
// factor would actually change playback (x1 is rejected as a mistake).
func isSpeedFactor(arg string) bool {
	v, _, ok := parseSpeed(arg)
	return ok && v != 1
}

// speedTargetError explains why a speed target was rejected.
func (a *application) speedTargetError(arg string) {
	if v, _, ok := parseSpeed(arg); ok && v == 1 {
		a.exitError("%s leaves the speed unchanged - drop it, or pick another factor", arg)
	}
	a.exitError("bad speed factor: %s - use x<number> greater than 0, e.g. x5 (faster) or x0.1 (slower)", arg)
}

// setFormat stores one format target per kind, so folder mode can take
// e.g. "jpeg mp4 mp3" (each applies to its own kind). A second format of
// the same kind is rejected instead of silently overwriting.
func (a *application) setFormat(o *options, name string) {
	switch {
	case isTargetVideo(name):
		if o.videoFmt != "" && o.videoFmt != name {
			a.exitError("two video formats given: %s and %s - pick one", o.videoFmt, name)
		}
		o.videoFmt = name
	case isTargetImage(name):
		if o.imageFmt != "" && o.imageFmt != name {
			a.exitError("two image formats given: %s and %s - pick one", o.imageFmt, name)
		}
		o.imageFmt = name
	case isTargetAudio(name):
		if o.audioFmt != "" && o.audioFmt != name {
			a.exitError("two audio formats given: %s and %s - pick one", o.audioFmt, name)
		}
		o.audioFmt = name
	}
}

// options are the parsed targets shared by single-file and batch runs.
type options struct {
	resolution         string
	videoFmt, imageFmt string
	audioFmt, compress string
	speed              string // raw factor as typed, e.g. "x5" or "x0.1"
}

// plainRun reports whether this run is a bare compression request: an
// input with no targets at all. That is the only shape that defaults to
// the compress level; specific operations run compression-free instead.
func (o options) plainRun() bool {
	return o.resolution == "" && o.videoFmt == "" && o.imageFmt == "" && o.audioFmt == "" && o.speed == ""
}

// resHeight returns the pixel height a resolution target caps at.
func resHeight(name string) int {
	if name == "4k" {
		return 2160
	}
	n, err := strconv.Atoi(strings.TrimSuffix(name, "p"))
	if err != nil {
		return 0
	}
	return n
}

// targetVideoCodec names the codec a video format target produces, so
// already-compressed inputs can be matched by codec rather than by
// container guesswork.
func targetVideoCodec(videoFmt string) string { return videoFormats[videoFmt].probe }

// noopReport explains that an input already satisfies the request, so
// no encoding is needed.
func (a *application) noopReport(input string) *fileReport {
	return &fileReport{app: a,
		input:    input,
		noop:     true,
		noopNote: filepath.Base(input) + " is already a compressed file that fits this request - nothing to do (pass a level to squeeze further, or -o to redo)",
	}
}

func (a *application) runMedia(positional []string, recursive bool, jobs int, folderKeyword bool) {
	if len(positional) == 0 {
		a.exitError("folder needs a path, e.g. shrink folder ~/Pictures jpeg")
	}

	input := positional[0]

	info, err := os.Stat(input)
	if err != nil {
		a.exitError("input file not found: %s", input)
	}

	// Remaining args are targets; keep it flexible (resolution, format, speed, compression in any order)
	var o options
	for _, target := range positional[1:] {
		switch {
		case isTargetResolution(target):
			o.resolution = target
		case isTargetVideo(target), isTargetImage(target), isTargetAudio(target):
			a.setFormat(&o, target)
		case isTargetCompression(target):
			o.compress = target
		case isSpeedFactor(target):
			if o.speed != "" {
				a.exitError("two speed factors given: %s and %s - pick one", o.speed, strings.ToLower(target))
			}
			o.speed = strings.ToLower(target)
		default:
			// anything shaped like x<number> (x0, x-2) reads as target speed
			// mistake: explain the right shape instead of "unknown target"
			if t := strings.ToLower(target); strings.HasPrefix(t, "x") && len(t) > 1 {
				if _, perr := strconv.ParseFloat(t[1:], 64); perr == nil {
					a.speedTargetError(target)
				}
			}
			a.unknownTargetError(target)
		}
	}

	// every remaining path probes or encodes, so the tools must exist
	a.requireDeps()

	if folderKeyword && !info.IsDir() {
		a.exitError("folder mode needs a folder, but %s is a file", input)
	}

	if recursive && !info.IsDir() {
		a.exitError("-r only applies to folders")
	}
	if info.IsDir() {
		a.batchProcess(input, o, recursive, jobs)
		return
	}
	kind := detectKind(input)
	if kind == "unknown" {
		a.unsupportedTypeError(input)
	}
	if formats := requestedFormats(o); len(formats) > 1 {
		a.targetConflictError(input, formats)
	}
	p, err := a.planJob(input, o)
	if err != nil {
		a.exitError("%v", err)
	}
	if !p.noop {
		p.reserve()
	}
	if a.dryRun {
		if a.machine {
			a.machineDryRun = true
			if p.noop {
				a.machineNote = a.noopReport(input).noopNote
			} else {
				a.machineFiles = append(a.machineFiles, machineFile{Input: input, Output: p.output, Kind: p.kind})
			}
			a.emitMachineOutput("", false, nil, 0, -1)
		} else if p.noop {
			fmt.Fprintln(a.stderr, a.noopReport(input).noopNote)
		} else {
			fmt.Fprintf(a.stderr, "%s dry run: would compress %s -> %s\n", a.doneMark(), a.ui.bold(input), a.ui.bold(p.output))
		}
		return
	}

	start := time.Now()
	rep, err := p.execute()
	if err != nil {
		if a.machine {
			a.exitMachineError(machineError{Error: err.Error(), Input: input})
		}
		fmt.Fprintf(a.stderr, "%s %s\n", a.errMark(), a.ui.red(err.Error()))
		a.printStepsFor(input)
		exitCLI(1)
	}
	if rep.noop {
		if a.machine {
			a.machineNote = rep.noopNote
			a.emitMachineOutput("", false, nil, time.Since(start), -1)
			return
		}
		fmt.Fprintf(a.stderr, "%s %s\n", a.doneMark(), a.ui.dim(rep.noopNote))
		return
	}
	if a.machine {
		a.recordMachineFile(rep)
		a.emitMachineOutput("", false, nil, time.Since(start), -1)
	} else {
		rep.print()
	}
}

// requestedFormats returns the format targets in a stable order.
func requestedFormats(o options) []string {
	var out []string
	for _, f := range []string{o.videoFmt, o.imageFmt, o.audioFmt} {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// formatKind labels a format with the kind of thing it produces.
func formatKind(name string) string {
	switch {
	case isTargetVideo(name):
		return "video"
	case isTargetImage(name):
		return "image"
	case isTargetAudio(name):
		return "audio"
	}
	return "file"
}

// joinAnd renders a list as "a, b and c".
func joinAnd(parts []string) string {
	if len(parts) < 2 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// targetConflictError answers the likely intent - "I wanted a video and its
// audio" - by explaining one file only yields one output, then showing the
// per-format commands that actually get them both.
func (a *application) targetConflictError(input string, formats []string) {
	var descs, suggestions []string
	for _, f := range formats {
		descs = append(descs, fmt.Sprintf("%s %s", f, formatKind(f)))
		suggestions = append(suggestions, fmt.Sprintf("shrink %s %s", input, f))
	}
	msg := fmt.Sprintf("cannot produce %s from one file", joinAnd(descs))
	if a.machine {
		a.exitMachineError(machineError{Error: msg, Input: input, Suggestions: suggestions})
	}
	fmt.Fprintf(a.stderr, "%s %s\n", a.errMark(), a.ui.red(msg))
	fmt.Fprintf(a.stderr, "  %s\n", a.ui.dim("to get both, run it once per format:"))
	for _, s := range suggestions {
		fmt.Fprintf(a.stderr, "    %s\n", a.ui.cyan(s))
	}
	exitCLI(1)
}

// checkKindTargets rejects format targets that don't make sense for a
// single file. One file can only become one thing, so multiple formats are
// not allowed (that is what folder mode is for); beyond that, only
// same-kind conversions are possible.
func checkKindTargets(kind string, o options) error {
	if formats := requestedFormats(o); len(formats) > 1 {
		return fmt.Errorf("cannot apply %s to a single file - multiple formats only work in folder mode", strings.Join(formats, " and "))
	}
	switch kind {
	case "image":
		if o.speed != "" {
			return fmt.Errorf("cannot change the speed of an image - %s applies to video and audio only", o.speed)
		}
		if o.videoFmt != "" {
			return fmt.Errorf("cannot convert an image to %s - only image-to-image is supported", o.videoFmt)
		}
		if o.audioFmt != "" {
			return fmt.Errorf("cannot convert an image to %s", o.audioFmt)
		}
	case "audio":
		if o.videoFmt != "" {
			return fmt.Errorf("cannot convert audio to %s - only audio-to-audio is supported", o.videoFmt)
		}
		if o.imageFmt != "" {
			return fmt.Errorf("cannot convert audio to %s", o.imageFmt)
		}
	case "video":
		if o.imageFmt != "" && o.speed != "" {
			return fmt.Errorf("a frame grab has no speed - drop %s to grab frames, or drop %s to change speed", o.speed, o.imageFmt)
		}
	}
	return nil
}

func coalesce(checked, fallback string) string {
	if checked != "" {
		return checked
	}
	return fallback
}

func (a *application) exitError(f string, values ...interface{}) {
	msg := fmt.Sprintf(f, values...)
	if a.machine {
		a.exitMachineError(machineError{Error: msg})
	}
	fmt.Fprintf(a.stderr, "%s %s\n", a.errMark(), a.ui.red(msg))
	exitCLI(1)
}
