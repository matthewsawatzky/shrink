package main

import (
	"fmt"
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

// fast enables -f/--fast: prefer hardware encoders (VideoToolbox on
// macOS) for video when available, falling back to software otherwise.
var fast bool

// dryRun enables -n/--dry-run: show what a run would do without
// encoding anything.
var dryRun bool

// overwrite enables -o/--overwrite: claim output paths that already
// exist (replacing them) instead of skipping or bumping a counter.
var overwrite bool

// deleteAfter enables -d/--delete: remove the original once a smaller
// replacement of the same kind exists. Derived files (audio tracks,
// frame grabs) never trigger deletion.
var deleteAfter bool

// levelSpec holds the encoder settings one compression level maps to.
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

// videoFormat is one output format shrink can encode video to.
type videoFormat struct {
	codec string
	ext   string // container extension; empty means the format key itself
	args  []string
}

// Supported video formats. VP9 gets speed flags: its defaults are
// brutally slow (it would otherwise spend minutes per encode to save a
// few percent at these bitrates), and single-pass with -b:v 0 keeps
// quality driven by the CRF alone. hevc/av1 ride in mp4 containers;
// the hvc1 tag lets Apple devices play the hevc result.
var videoFormats = map[string]videoFormat{
	"mp4":  {codec: "libx264", args: []string{"-preset", "medium", "-movflags", "+faststart"}},
	"mkv":  {codec: "libx264", args: []string{"-preset", "medium"}},
	"webm": {codec: "libvpx-vp9", args: []string{"-b:v", "0", "-row-mt", "1", "-deadline", "good", "-cpu-used", "4"}},
	"mov":  {codec: "libx264", args: []string{"-preset", "medium", "-movflags", "+faststart"}},
	"avi":  {codec: "libx264", args: []string{"-preset", "medium"}},
	"hevc": {codec: "libx265", ext: "mp4", args: []string{"-preset", "medium", "-tag:v", "hvc1"}},
	"av1":  {codec: "libsvtav1", ext: "mp4", args: []string{"-preset", "8"}},
}

// imageFormat is one output format shrink can encode images to.
type imageFormat struct {
	codec string
	ext   string
}

// Supported image formats
var imageFormats = map[string]imageFormat{
	"jpeg": {codec: "mjpeg", ext: "jpg"},
	"jpg":  {codec: "mjpeg", ext: "jpg"},
	"png":  {codec: "png", ext: "png"},
	"webp": {codec: "libwebp", ext: "webp"},
	"gif":  {codec: "gif", ext: "gif"},
}

// audioFormat is one output format shrink can encode audio to.
type audioFormat struct {
	codec string
	ext   string
}

// Supported audio formats
var audioFormats = map[string]audioFormat{
	"mp3":  {codec: "libmp3lame", ext: "mp3"},
	"aac":  {codec: "aac", ext: "m4a"},
	"m4a":  {codec: "aac", ext: "m4a"},
	"opus": {codec: "libopus", ext: "opus"},
	"ogg":  {codec: "libvorbis", ext: "ogg"},
	"wav":  {codec: "pcm_s16le", ext: "wav"},
	"flac": {codec: "flac", ext: "flac"},
}

func usage() {
	out := os.Stderr
	fmt.Fprintf(out, "%s %s\n\n", ui.bold("shrink"), ui.dim("- compress images, video and audio with ffmpeg"))
	fmt.Fprintf(out, "  %s\n", ui.bold("usage: shrink <input> [target...]"))
	fmt.Fprintf(out, "  %s\n", ui.bold("       shrink folder <folder> [format...] [level]"))
	fmt.Fprintf(out, "  %s\n", ui.bold("       shrink info <file>...   # inspect codecs, duration, fps, bitrate"))
	fmt.Fprintf(out, "  %s\n", ui.bold("       shrink doctor   # check ffmpeg and encoder availability"))
	fmt.Fprintf(out, "  %s\n", ui.bold("       shrink make [targets...]   # generate test media"))
	fmt.Fprintf(out, "  %s\n\n", ui.bold("       shrink update   # self-update to the latest release"))
	fmt.Fprintf(out, "%s\n", ui.dim("  folder mode converts every matching file in place. Give one format per"))
	fmt.Fprintf(out, "%s\n\n", ui.dim("  media kind (e.g. \"jpeg mp4 mp3\"); levels apply to all media. Add -r for subfolders."))
	fmt.Fprintf(out, "%s\n", ui.bold("targets:"))
	for _, t := range []struct{ name, desc string }{
		{"<resolution>", "340p, 480p, 720p, 1080p, 1440p, 4k"},
		{"<format>", "jpeg, png, webp, gif | mp4, mkv, webm, mov, avi, hevc, av1"},
		{"<audio>", "mp3, aac, m4a, opus, ogg, wav, flac"},
		{"<speed>", "x2, x5, x0.1 ... change video/audio playback speed"},
		{"<level>", "compress, spacesaver, neversee"},
	} {
		fmt.Fprintf(out, "  %s  %s\n", ui.cyan(fmt.Sprintf("%-12s", t.name)), t.desc)
	}
	fmt.Fprintf(out, "\n%s\n", ui.bold("flags:"))
	for _, f := range []struct{ name, desc string }{
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
		fmt.Fprintf(out, "  %s  %s\n", ui.cyan(fmt.Sprintf("%-12s", f.name)), f.desc)
	}
	fmt.Fprintf(out, "\n%s\n", ui.dim("  speed factors over 1 speed up, under 1 slow down: x2 plays twice as fast, x0.1 ten times slower."))
	fmt.Fprintf(out, "\n%s\n", ui.bold("examples:"))
	for _, ex := range []string{
		"shrink vid.mkv webm spacesaver",
		"shrink pic.heic 1080p jpeg neversee",
		"shrink clip.mp4 mp3",
		"shrink clip.mp4 x5",
		"shrink clip.mp4 x0.1 spacesaver",
		"shrink folder ~/Media jpeg mp4 mp3 spacesaver",
		"shrink make mp4 red 1080p 60fps 30s",
	} {
		fmt.Fprintf(out, "  %s\n", ui.dim(ex))
	}
	fmt.Fprintf(out, "\n%s\n", ui.bold("compression levels:"))
	for _, l := range []struct{ name, desc string }{
		{"compress", "decent quality, solid savings"},
		{"spacesaver", "noticeably compressed"},
		{"neversee", "very compressed"},
	} {
		fmt.Fprintf(out, "  %s  %s\n", ui.cyan(fmt.Sprintf("%-12s", l.name)), l.desc)
	}
	fmt.Fprintf(out, "\n%s\n", ui.dim("  A bare shrink <file> run compresses (the compress level). Any specific operation -"))
	fmt.Fprintf(out, "%s\n", ui.dim("  scale, speed, format, audio extraction - runs alone at near-lossless quality: pass"))
	fmt.Fprintf(out, "%s\n", ui.dim("  a level to compress on top of it."))
	fmt.Fprintf(out, "\n%s\n", ui.bold("make targets (shrink make [targets...]):"))
	for _, t := range []struct{ name, desc string }{
		{"<format>", "mp4, jpeg, wav ... - the format picks the kind of thing made"},
		{"<content>", "a color name (red, blue, gray...), bars, test, noise; sine for audio"},
		{"<fps>", "24fps, 25fps, 30fps, 50fps, 60fps (default), 120fps"},
		{"<duration>", "Ns or Nm, e.g. 10s, 2m (default 10s)"},
		{"<hz>", "audio tone frequency, e.g. 440hz (default)"},
	} {
		fmt.Fprintf(out, "  %s  %s\n", ui.cyan(fmt.Sprintf("%-12s", t.name)), t.desc)
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
		if n != "jpg" && n != "aac" {
			kept = append(kept, n)
		}
	}
	return kept
}

// unknownTargetError explains a rejected target. Input-only extensions
// (aiff, wma, heic...) get their own message, so seeing them in the input
// list never reads as "why not this?"; anything else lists valid targets.
func unknownTargetError(arg string) {
	ext := "." + strings.TrimPrefix(strings.ToLower(arg), ".")
	if kind := kindForExt(ext); kind != "" {
		if machine {
			exitMachineError(machineError{
				Error:        fmt.Sprintf("%s is a valid %s input, but shrink cannot write %s", arg, kind, arg),
				ValidTargets: outputFormats(kind),
			})
		}
		fmt.Fprintf(os.Stderr, "%s %s\n", errMark(),
			ui.red(fmt.Sprintf("%s is a valid %s input, but shrink cannot write %s", arg, kind, arg)))
		fmt.Fprintf(os.Stderr, "  %s %s\n", ui.dim(kind+" outputs:"), strings.Join(outputFormats(kind), ", "))
		os.Exit(1)
	}
	if machine {
		var valid []string
		valid = append(valid, "340p", "480p", "720p", "1080p", "1440p", "4k")
		valid = append(valid, "compress", "spacesaver", "neversee")
		for _, kind := range mediaKinds {
			valid = append(valid, outputFormats(kind)...)
		}
		exitMachineError(machineError{Error: "unknown target: " + arg, ValidTargets: valid})
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", errMark(), ui.red("unknown target: "+arg))
	fmt.Fprintf(os.Stderr, "  %s\n", ui.dim("valid targets:"))
	fmt.Fprintf(os.Stderr, "    %s  %s\n", ui.cyan(fmt.Sprintf("%-10s", "resolution")), "340p, 480p, 720p, 1080p, 1440p, 4k")
	fmt.Fprintf(os.Stderr, "    %s  %s\n", ui.cyan(fmt.Sprintf("%-10s", "levels")), "compress, spacesaver, neversee")
	for _, kind := range mediaKinds {
		fmt.Fprintf(os.Stderr, "    %s  %s\n", ui.cyan(fmt.Sprintf("%-10s", kind)), strings.Join(outputFormats(kind), ", "))
	}
	os.Exit(1)
}

// unsupportedTypeError names the offending file type and lists every type
// shrink can read, so the gap is obvious without consulting --help.
func unsupportedTypeError(input string) {
	what := strings.ToLower(filepath.Ext(input))
	if what == "" {
		what = "no extension"
	}
	if machine {
		var valid []string
		for _, kind := range mediaKinds {
			valid = append(valid, mediaExtensions[kind]...)
		}
		exitMachineError(machineError{
			Error:       "unsupported file type: " + what,
			Input:       input,
			ValidInputs: valid,
		})
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", errMark(), ui.red("unsupported file type: "+what))
	fmt.Fprintf(os.Stderr, "  %s\n", ui.dim("shrink reads:"))
	for _, kind := range mediaKinds {
		var exts []string
		for _, e := range mediaExtensions[kind] {
			exts = append(exts, strings.TrimPrefix(e, "."))
		}
		fmt.Fprintf(os.Stderr, "    %s  %s\n", ui.cyan(fmt.Sprintf("%-5s", kind)), strings.Join(exts, ", "))
	}
	os.Exit(1)
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
	if err != nil || v <= 0 {
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
func speedTargetError(arg string) {
	if v, _, ok := parseSpeed(arg); ok && v == 1 {
		exitError("%s leaves the speed unchanged - drop it, or pick another factor", arg)
	}
	exitError("bad speed factor: %s - use x<number> greater than 0, e.g. x5 (faster) or x0.1 (slower)", arg)
}

// setFormat stores one format target per kind, so folder mode can take
// e.g. "jpeg mp4 mp3" (each applies to its own kind). A second format of
// the same kind is rejected instead of silently overwriting.
func setFormat(o *options, name string) {
	switch {
	case isTargetVideo(name):
		if o.videoFmt != "" && o.videoFmt != name {
			exitError("two video formats given: %s and %s - pick one", o.videoFmt, name)
		}
		o.videoFmt = name
	case isTargetImage(name):
		if o.imageFmt != "" && o.imageFmt != name {
			exitError("two image formats given: %s and %s - pick one", o.imageFmt, name)
		}
		o.imageFmt = name
	case isTargetAudio(name):
		if o.audioFmt != "" && o.audioFmt != name {
			exitError("two audio formats given: %s and %s - pick one", o.audioFmt, name)
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
func targetVideoCodec(videoFmt string) string {
	switch videoFmt {
	case "hevc":
		return "hevc"
	case "av1":
		return "av1"
	case "webm":
		return "vp9"
	}
	return "h264" // mp4, mkv, mov, avi
}

// noopReport explains that an input already satisfies the request, so
// no encoding is needed.
func noopReport(input string) *fileReport {
	return &fileReport{
		input:    input,
		noop:     true,
		noopNote: filepath.Base(input) + " is already a compressed file that fits this request - nothing to do (pass a level to squeeze further, or -o to redo)",
	}
}

// processOne runs a single compression job and returns its report.
// All failures come back as errors so batch runs can continue.
func processOne(input string, o options) (*fileReport, error) {
	kind := detectKind(input)
	probeDone := trackStep(input, "inspect input")
	info := probeFile(input)
	if kind != "unknown" && !info.ok {
		return nil, fmt.Errorf("%s does not look like a valid %s file", input, kind)
	}

	// for audio extraction, make sure there is an audio track to extract
	if kind == "video" && o.audioFmt != "" && !info.hasAudio {
		return nil, fmt.Errorf("%s has no audio track - nothing to extract as %s (grab a frame instead, e.g. shrink %s png)", input, o.audioFmt, input)
	}
	probeDone()

	// Route by input kind. Cross-format targets are supported where ffmpeg
	// makes sense of them (video -> audio extracts the track, video -> image
	// grabs a frame); checkKindTargets already rejected anything else.
	if err := checkKindTargets(kind, o); err != nil {
		return nil, err
	}

	// One of shrink's own outputs fed back in (detected by name) needs
	// no second compression pass: without an explicit level or speed, a
	// request the file already satisfies is a no-op, and a resolution
	// target that still downscales re-encodes at a near-lossless grade.
	if looksConverted(input) && o.compress == "" && o.speed == "" && !overwrite {
		switch kind {
		case "video":
			if o.audioFmt == "" && o.imageFmt == "" &&
				targetVideoCodec(coalesce(o.videoFmt, "mp4")) == info.videoCodec {
				if o.resolution == "" || info.height <= resHeight(o.resolution) {
					return noopReport(input), nil
				}
				return handleRescaleVideo(input, o.resolution, coalesce(o.videoFmt, "mp4"), info)
			}
		case "image":
			if img, ok := imageFormats[coalesce(o.imageFmt, "jpeg")]; ok && img.codec == info.videoCodec {
				if o.resolution == "" || info.height <= resHeight(o.resolution) {
					return noopReport(input), nil
				}
				return handleRescaleImage(input, o.resolution, coalesce(o.imageFmt, "jpeg"))
			}
		case "audio":
			if af := audioFormats[coalesce(o.audioFmt, "mp3")]; af.codec == info.audioCodec {
				return noopReport(input), nil
			}
		}
	}

	// Grade: a bare "shrink file" run defaults to the compress level.
	// Anything more specific (scale, speed, format, extraction) performs
	// that operation alone at a near-lossless grade - compression requires
	// an explicit level.
	grade := o.compress
	if grade == "" && o.plainRun() {
		grade = "compress"
	}

	switch kind {
	case "video":
		switch {
		case o.audioFmt != "":
			return handleAudioFromVideo(input, o.audioFmt, grade, o.speed, info)
		case o.imageFmt != "":
			return handleVideoToImage(input, o.resolution, o.imageFmt, grade, info)
		default:
			return handleVideo(input, o.resolution, coalesce(o.videoFmt, "mp4"), grade, o.speed, info)
		}
	case "image":
		return handleImage(input, o.resolution, coalesce(o.imageFmt, "jpeg"), grade)
	case "audio":
		return handleAudio(input, coalesce(o.audioFmt, "mp3"), grade, o.speed, info)
	}
	return nil, fmt.Errorf("no handler for %s", input)
}

func main() {
	args := os.Args[1:]

	// -v/--verbose, -r, -m, -f, -n and -o are plain flags and may appear
	// anywhere; -j and -c take a value; the input is the first thing left
	// after flags are pulled out.
	var positional []string
	recursive := false
	jobs := 2
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-v", "--verbose":
			verbose = true
		case "-m", "--machine":
			machine = true
		case "-r", "--recursive":
			recursive = true
		case "-f", "--fast":
			fast = true
		case "-n", "--dry-run":
			dryRun = true
		case "-o", "--overwrite":
			overwrite = true
		case "-d", "--delete":
			deleteAfter = true
		case "-V", "--version":
			fmt.Println("shrink " + version)
			os.Exit(0)
		case "-c", "--completions":
			i++
			if i >= len(args) {
				exitError("completions needs a shell: zsh or bash")
			}
			printCompletions(args[i])
		case "-j", "--jobs":
			i++
			if i >= len(args) {
				exitError("jobs needs a number, e.g. shrink folder ~/Media -j 4 jpeg")
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				exitError("jobs needs a number of 1 or more, got %q", args[i])
			}
			jobs = n
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) == 0 {
		usage()
		os.Exit(1)
	}

	// "info" prints a concise probe of one or more files.
	if len(positional) > 0 && positional[0] == "info" {
		runInfo(positional[1:])
		return
	}

	// "doctor" checks the install: ffmpeg, ffprobe and every encoder
	if len(positional) > 0 && positional[0] == "doctor" {
		if len(positional) > 1 {
			exitError("doctor takes no arguments - just: shrink doctor")
		}
		runDoctor()
	}

	// "make" generates test media; the rest of the line is targets.
	if len(positional) > 0 && positional[0] == "make" {
		runMake(positional[1:])
		return
	}

	// "update" replaces the running binary with the newest release.
	if len(positional) > 0 && positional[0] == "update" {
		if len(positional) > 1 {
			exitError("update takes no arguments - just: shrink update")
		}
		runUpdate()
	}

	// "folder" opens explicit folder mode: the next argument is the folder,
	// and separate format targets per media kind are allowed.
	folderKeyword := false
	if len(positional) > 0 && positional[0] == "folder" {
		folderKeyword = true
		positional = positional[1:]
		if len(positional) == 0 {
			exitError("folder needs a path, e.g. shrink folder ~/Pictures jpeg")
		}
	}

	input := positional[0]

	info, err := os.Stat(input)
	if err != nil {
		exitError("input file not found: %s", input)
	}

	// Remaining args are targets; keep it flexible (resolution, format, speed, compression in any order)
	var o options
	for _, a := range positional[1:] {
		switch {
		case isTargetResolution(a):
			o.resolution = a
		case isTargetVideo(a), isTargetImage(a), isTargetAudio(a):
			setFormat(&o, a)
		case isTargetCompression(a):
			o.compress = a
		case isSpeedFactor(a):
			if o.speed != "" {
				exitError("two speed factors given: %s and %s - pick one", o.speed, strings.ToLower(a))
			}
			o.speed = strings.ToLower(a)
		default:
			// anything shaped like x<number> (x0, x-2) reads as a speed
			// mistake: explain the right shape instead of "unknown target"
			if t := strings.ToLower(a); strings.HasPrefix(t, "x") && len(t) > 1 {
				if _, perr := strconv.ParseFloat(t[1:], 64); perr == nil {
					speedTargetError(a)
				}
			}
			unknownTargetError(a)
		}
	}

	// every remaining path probes or encodes, so the tools must exist
	requireDeps()

	if folderKeyword && !info.IsDir() {
		exitError("folder mode needs a folder, but %s is a file", input)
	}

	if !info.IsDir() {
		kind := detectKind(input)
		if kind == "unknown" {
			unsupportedTypeError(input)
		}
		// one file can only become one thing; guide toward the two runs
		if formats := requestedFormats(o); len(formats) > 1 {
			targetConflictError(input, formats)
		}
		if err := checkKindTargets(kind, o); err != nil {
			exitError("%v", err)
		}
		if dryRun {
			out := wouldProduce(input, kind, o)
			if machine {
				machineFiles = append(machineFiles, machineFile{Input: input, Output: out, Kind: kind})
				machineDryRun = true
				emitMachineOutput("", false, nil, 0, 1)
				return
			}
			fmt.Fprintf(os.Stderr, "%s dry run: would compress %s -> %s\n", doneMark(), ui.bold(input), ui.bold(out))
			return
		}
	}

	if recursive && !info.IsDir() {
		exitError("-r only applies to folders")
	}

	if info.IsDir() {
		batchProcess(input, o, recursive, jobs)
		return
	}

	start := time.Now()
	rep, err := processOne(input, o)
	if err != nil {
		if machine {
			exitMachineError(machineError{Error: err.Error(), Input: input})
		}
		fmt.Fprintf(os.Stderr, "%s %s\n", errMark(), ui.red(err.Error()))
		printStepsFor(input)
		os.Exit(1)
	}
	if rep.noop {
		if machine {
			machineNote = rep.noopNote
			emitMachineOutput("", false, nil, time.Since(start), -1)
			return
		}
		fmt.Fprintf(os.Stderr, "%s %s\n", doneMark(), ui.dim(rep.noopNote))
		return
	}
	if machine {
		emitMachineOutput("", false, nil, time.Since(start), -1)
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
func targetConflictError(input string, formats []string) {
	var descs, suggestions []string
	for _, f := range formats {
		descs = append(descs, fmt.Sprintf("%s %s", f, formatKind(f)))
		suggestions = append(suggestions, fmt.Sprintf("shrink %s %s", input, f))
	}
	msg := fmt.Sprintf("cannot produce %s from one file", joinAnd(descs))
	if machine {
		exitMachineError(machineError{Error: msg, Input: input, Suggestions: suggestions})
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", errMark(), ui.red(msg))
	fmt.Fprintf(os.Stderr, "  %s\n", ui.dim("to get both, run it once per format:"))
	for _, s := range suggestions {
		fmt.Fprintf(os.Stderr, "    %s\n", ui.cyan(s))
	}
	os.Exit(1)
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

func exitError(f string, a ...interface{}) {
	msg := fmt.Sprintf(f, a...)
	if machine {
		exitMachineError(machineError{Error: msg})
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", errMark(), ui.red(msg))
	os.Exit(1)
}
