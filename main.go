package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// levelSpec holds the encoder settings one compression level maps to.
type levelSpec struct {
	videoCRF     string
	audioBitrate string
	imageQuality string // quality scale for mjpeg (2-31, higher = smaller)
	webpQuality  string // libwebp -quality (0-100)
	vorbisQ      string // libvorbis -q:a (0-10)
}

// Preset compression levels shared by video, image and audio encoders.
// Higher CRF = heavier compression = smaller file + lower quality.
var compressionLevels = map[string]levelSpec{
	"compress":   {videoCRF: "23", audioBitrate: "128k", imageQuality: "4", webpQuality: "80", vorbisQ: "5"},
	"spacesaver": {videoCRF: "28", audioBitrate: "96k", imageQuality: "7", webpQuality: "65", vorbisQ: "3"},
	"neversee":   {videoCRF: "34", audioBitrate: "64k", imageQuality: "12", webpQuality: "40", vorbisQ: "1"},
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

// videoFormat is one output container shrink can encode video to.
type videoFormat struct {
	codec string
	args  []string
}

// Supported video formats. VP9 gets speed flags: its defaults are
// brutally slow (it would otherwise spend minutes per encode to save a
// few percent at these bitrates), and single-pass with -b:v 0 keeps
// quality driven by the CRF alone.
var videoFormats = map[string]videoFormat{
	"mp4":  {codec: "libx264", args: []string{"-preset", "medium", "-movflags", "+faststart"}},
	"mkv":  {codec: "libx264", args: []string{"-preset", "medium"}},
	"webm": {codec: "libvpx-vp9", args: []string{"-b:v", "0", "-row-mt", "1", "-deadline", "good", "-cpu-used", "4"}},
	"mov":  {codec: "libx264", args: []string{"-preset", "medium", "-movflags", "+faststart"}},
	"avi":  {codec: "libx264", args: []string{"-preset", "medium"}},
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
	fmt.Fprintf(out, "  %s\n\n", ui.bold("       shrink folder <folder> [format...] [level]"))
	fmt.Fprintf(out, "%s\n", ui.dim("  folder mode converts every matching file in place. Give one format per"))
	fmt.Fprintf(out, "%s\n\n", ui.dim("  media kind (e.g. \"jpeg mp4 mp3\"); levels apply to all media. Add -r for subfolders."))
	fmt.Fprintf(out, "%s\n", ui.bold("targets:"))
	for _, t := range []struct{ name, desc string }{
		{"<resolution>", "340p, 480p, 720p, 1080p, 1440p, 4k"},
		{"<format>", "jpeg, png, webp, gif | mp4, mkv, webm, mov, avi"},
		{"<audio>", "mp3, aac, m4a, opus, ogg, wav, flac"},
		{"<level>", "compress, spacesaver, neversee"},
	} {
		fmt.Fprintf(out, "  %s  %s\n", ui.cyan(fmt.Sprintf("%-12s", t.name)), t.desc)
	}
	fmt.Fprintf(out, "\n%s\n", ui.bold("flags:"))
	for _, f := range []struct{ name, desc string }{
		{"-v, --verbose", "show step timings, and use ms and KB everywhere"},
		{"-r, --recursive", "with a folder, also scan subfolders"},
		{"-m, --machine", "emit JSON instead of human output (for scripts/LLMs)"},
	} {
		fmt.Fprintf(out, "  %s  %s\n", ui.cyan(fmt.Sprintf("%-12s", f.name)), f.desc)
	}
	fmt.Fprintf(out, "\n%s\n", ui.bold("examples:"))
	for _, ex := range []string{
		"shrink vid.mkv webm spacesaver",
		"shrink pic.heic 1080p jpeg neversee",
		"shrink clip.mp4 mp3",
		"shrink folder ~/Media jpeg mp4 mp3 spacesaver",
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
		printSteps()
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
	printSteps()
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
	printSteps()
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
}

// processOne runs a single compression job and returns the output path.
// All failures come back as errors so batch runs can continue.
func processOne(input string, o options) (string, error) {
	kind := detectKind(input)
	probeDone := trackStep("inspect input")
	info := probeFile(input)
	if kind != "unknown" && !info.ok {
		return "", fmt.Errorf("%s does not look like a valid %s file", input, kind)
	}

	// for audio extraction, make sure there is an audio track to extract
	if kind == "video" && o.audioFmt != "" && !info.hasAudio {
		return "", fmt.Errorf("%s has no audio track - nothing to extract as %s (grab a frame instead, e.g. shrink %s png)", input, o.audioFmt, input)
	}
	probeDone()

	// Route by input kind. Cross-format targets are supported where ffmpeg
	// makes sense of them (video -> audio extracts the track, video -> image
	// grabs a frame); checkKindTargets already rejected anything else.
	if err := checkKindTargets(kind, o); err != nil {
		return "", err
	}
	switch kind {
	case "video":
		switch {
		case o.audioFmt != "":
			return handleAudioFromVideo(input, o.audioFmt, o.compress, info.duration)
		case o.imageFmt != "":
			return handleVideoToImage(input, o.resolution, o.imageFmt, o.compress, info.duration)
		default:
			return handleVideo(input, o.resolution, coalesce(o.videoFmt, "mp4"), o.compress, info.duration)
		}
	case "image":
		return handleImage(input, o.resolution, coalesce(o.imageFmt, "jpeg"), o.compress)
	case "audio":
		return handleAudio(input, coalesce(o.audioFmt, "mp3"), o.compress, info.duration)
	}
	return "", fmt.Errorf("no handler for %s", input)
}

func main() {
	args := os.Args[1:]

	// -v/--verbose, -r and -m are plain flags and may appear anywhere;
	// the input is the first thing left after flags are pulled out.
	var positional []string
	recursive := false
	for _, a := range args {
		switch a {
		case "-v", "--verbose":
			verbose = true
		case "-m", "--machine":
			machine = true
		case "-r", "--recursive":
			recursive = true
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) == 0 {
		usage()
		os.Exit(1)
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

	// Remaining args are targets; keep it flexible (resolution, format, compression in any order)
	var o options
	for _, a := range positional[1:] {
		switch {
		case isTargetResolution(a):
			o.resolution = a
		case isTargetVideo(a), isTargetImage(a), isTargetAudio(a):
			setFormat(&o, a)
		case isTargetCompression(a):
			o.compress = a
		default:
			unknownTargetError(a)
		}
	}

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
	}

	if recursive && !info.IsDir() {
		exitError("-r only applies to folders")
	}

	if info.IsDir() {
		batchProcess(input, o, recursive)
		return
	}

	start := time.Now()
	if _, err := processOne(input, o); err != nil {
		if machine {
			exitMachineError(machineError{Error: err.Error(), Input: input})
		}
		exitError("%v", err)
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
	printSteps()
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
	printSteps()
	os.Exit(1)
}
