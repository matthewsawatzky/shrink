package main

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// makeColors are the solid colors shrink make can generate, by name.
var makeColors = map[string]string{
	"red": "0xFF0000", "green": "0x008000", "blue": "0x0000FF",
	"yellow": "0xFFFF00", "cyan": "0x00FFFF", "magenta": "0xFF00FF",
	"white": "0xFFFFFF", "black": "0x000000", "gray": "0x808080",
	"grey": "0x808080", "orange": "0xFFA500", "purple": "0x800080",
	"pink": "0xFFC0CB", "brown": "0xA52A2A", "teal": "0x008080",
	"gold": "0xFFD700", "lime": "0x00FF00", "silver": "0xC0C0C0",
	"navy": "0x000080", "maroon": "0x800000", "olive": "0x808000",
}

// makeFPS are the frame rates shrink make can generate.
var makeFPS = map[string]int{
	"24fps": 24, "25fps": 25, "30fps": 30,
	"50fps": 50, "60fps": 60, "120fps": 120,
}

// Generation defaults: 60fps per request, plus the rest of the sane
// test-media starting points.
const (
	makeDefaultRes  = "1080p"
	makeDefaultFPS  = "60fps"
	makeDefaultDur  = "10s"
	makeDefaultHz   = "440hz"
	makeDefaultTone = "sine"
	makeDefaultFill = "red"
)

// makeSpec is one generation request. Only explicitly-given targets are
// filled in by the parser; resolve() applies the per-kind defaults.
type makeSpec struct {
	kind    string // video, image or audio
	format  string // the format target chosen (mp4, jpeg, wav...)
	content string // a color name, bars, test, noise, or sine
	res     string
	fps     string
	hz      string
	dur     string
	level   string // "" means the compress default
}

// resolve fills in the per-kind defaults.
func (s makeSpec) resolve() makeSpec {
	r := s
	switch r.kind {
	case "image":
		if r.content == "" {
			r.content = makeDefaultFill
		}
		if r.res == "" {
			r.res = makeDefaultRes
		}
	case "audio":
		if r.content == "" {
			r.content = makeDefaultTone
		}
		if r.hz == "" {
			r.hz = makeDefaultHz
		}
		if r.dur == "" {
			r.dur = makeDefaultDur
		}
	default: // video
		if r.content == "" {
			r.content = makeDefaultFill
		}
		if r.res == "" {
			r.res = makeDefaultRes
		}
		if r.fps == "" {
			r.fps = makeDefaultFPS
		}
		if r.dur == "" {
			r.dur = makeDefaultDur
		}
		if r.hz == "" {
			r.hz = makeDefaultHz
		}
	}
	return r
}

// name builds the output file's base name from what is being made, so
// a test file always describes itself.
func (s makeSpec) name() string {
	parts := []string{s.content}
	switch s.kind {
	case "video":
		parts = append(parts, s.res, s.fps, s.dur)
	case "image":
		parts = append(parts, s.res)
	case "audio":
		parts = append(parts, s.hz, s.dur)
	}
	if s.level != "" {
		parts = append(parts, s.level)
	}
	return strings.Join(parts, "-")
}

// parseMakeDuration reads "10s" / "2m" style durations, in seconds.
func parseMakeDuration(s string) (float64, bool) {
	if len(s) < 2 {
		return 0, false
	}
	num, err := strconv.ParseFloat(s[:len(s)-1], 64)
	if err != nil || num <= 0 || math.IsNaN(num) || math.IsInf(num, 0) {
		return 0, false
	}
	switch s[len(s)-1] {
	case 's':
		return num, true
	case 'm':
		return num * 60, !math.IsInf(num*60, 0)
	}
	return 0, false
}

// parseMakeHz reads "440hz" style tone frequencies.
func parseMakeHz(s string) (int, bool) {
	if !strings.HasSuffix(s, "hz") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, "hz"))
	if err != nil || n < 20 || n > 20000 {
		return 0, false
	}
	return n, true
}

func isMakeFPS(s string) bool      { _, ok := makeFPS[s]; return ok }
func isMakeHz(s string) bool       { _, ok := parseMakeHz(s); return ok }
func isMakeDuration(s string) bool { _, ok := parseMakeDuration(s); return ok }

// isMakeContent reports whether s names something shrink make can
// generate: a color, a test pattern, or an audio source.
func isMakeContent(s string) bool {
	if _, ok := makeColors[s]; ok {
		return true
	}
	switch s {
	case "bars", "test", "noise", "sine":
		return true
	}
	return false
}

// parseMakeTargets classifies everything after "make". It mirrors the
// shared target vocabulary (formats, resolutions, levels) and adds the
// generation-specific targets, validating which target applies to which
// kind.
func parseMakeTargets(args []string) (makeSpec, error) {
	var spec makeSpec
	var formats []string
	contents := 0
	for _, a := range args {
		switch {
		case isTargetResolution(a):
			spec.res = a
		case isTargetVideo(a), isTargetImage(a), isTargetAudio(a):
			formats = append(formats, a)
		case isTargetCompression(a):
			spec.level = a
		case isMakeFPS(a):
			spec.fps = a
		case isMakeHz(a):
			spec.hz = a
		case isMakeDuration(a):
			spec.dur = a
		case isMakeContent(a):
			spec.content = a
			contents++
		default:
			return spec, fmt.Errorf("unknown make target: %s - expected a format, a resolution, a level, a color, bars, test, noise, sine, an fps like 60fps, a duration like 10s, or a frequency like 440hz", a)
		}
	}
	if len(formats) > 1 {
		return spec, fmt.Errorf("make takes one format target - got %s and %s", formats[0], formats[1])
	}
	if contents > 1 {
		return spec, fmt.Errorf("make takes one content target - a color, bars, test, noise or sine")
	}
	if len(formats) == 1 {
		f := formats[0]
		switch {
		case isTargetVideo(f):
			spec.kind, spec.format = "video", f
		case isTargetImage(f):
			spec.kind, spec.format = "image", f
		default:
			spec.kind, spec.format = "audio", f
		}
	} else {
		spec.kind, spec.format = "video", "mp4"
	}

	switch spec.kind {
	case "audio":
		if spec.res != "" {
			return spec, fmt.Errorf("resolution targets apply to video and images, not audio")
		}
		if spec.fps != "" {
			return spec, fmt.Errorf("fps targets apply to video only")
		}
		if spec.content != "" && spec.content != "sine" && spec.content != "noise" {
			return spec, fmt.Errorf("%s content is for video and images - audio takes sine or noise", spec.content)
		}
	case "image":
		if spec.fps != "" {
			return spec, fmt.Errorf("fps targets apply to video only")
		}
		if spec.dur != "" {
			return spec, fmt.Errorf("duration targets apply to video and audio - an image is a single frame")
		}
		if spec.content == "sine" {
			return spec, fmt.Errorf("sine is an audio content target")
		}
	default: // video
		if spec.content == "sine" {
			return spec, fmt.Errorf("sine is an audio content target")
		}
	}
	if spec.dur != "" {
		if secs, ok := parseMakeDuration(spec.dur); !ok || secs > 3600 {
			return spec, fmt.Errorf("duration must look like 10s or 2m, at most 60m - got %s", spec.dur)
		}
	}
	return spec, nil
}

// makeSize turns a resolution target into a 16:9 generation size; test
// media is generated at exactly this size, never scaled.
func makeSize(res string) (w, h int) {
	h = resHeight(res)
	if h <= 0 {
		h = 1080
	}
	w = h * 16 / 9
	w -= w % 2
	return w, h
}

// durString renders seconds the way ffmpeg wants them.
func durString(secs float64) string {
	if secs == float64(int(secs)) {
		return strconv.Itoa(int(secs))
	}
	return strconv.FormatFloat(secs, 'f', -1, 64)
}

// makePictureSource builds the lavfi source for a video or image, plus
// any extra output filters the content needs.
func makePictureSource(content string, w, h, fps int) (source string, filters []string) {
	size := fmt.Sprintf("s=%dx%d", w, h)
	rate := ""
	if fps > 0 {
		rate = fmt.Sprintf(":r=%d", fps)
	}
	switch content {
	case "bars":
		return "smptebars=" + size + rate, nil
	case "test":
		return "testsrc2=" + size + rate, nil
	case "noise":
		return "nullsrc=" + size + rate, []string{"-vf", "noise=alls=100:allf=t+u"}
	}
	return fmt.Sprintf("color=%s:%s%s", makeColors[content], size, rate), nil
}

// makeOutput claims a descriptive name in the current directory.
func (a *application) makeOutput(s makeSpec) string {
	s = s.resolve()
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	return a.claimOutput(dir, s.name(), outExt(s.kind, s.format))
}

// makeVideo generates a test video with the same codec and level
// machinery normal encodes use, plus a sine soundtrack so audio paths
// get exercised too.
func (a *application) makeVideo(s makeSpec) (*fileReport, error) {
	s = s.resolve()
	levels := compressionLevels[coalesce(s.level, "compress")]
	vf := videoFormats[s.format]
	w, h := makeSize(s.res)
	fps := makeFPS[s.fps]
	hz, _ := parseMakeHz(s.hz)
	dur, _ := parseMakeDuration(s.dur)

	picture, filters := makePictureSource(s.content, w, h, fps)
	tone := fmt.Sprintf("sine=frequency=%d:sample_rate=48000", hz)

	args := []string{"-f", "lavfi", "-i", picture, "-f", "lavfi", "-i", tone}
	args = append(args, "-t", durString(dur))
	args = append(args, filters...)
	args = append(args, "-pix_fmt", "yuv420p", "-c:v", vf.codec, "-crf", videoCRF(vf.codec, levels))
	args = append(args, vf.args...)
	if s.format != "webm" {
		args = append(args, "-c:a", "aac", "-b:a", levels.audioBitrate)
	} else {
		args = append(args, "-c:a", "libopus", "-b:a", levels.audioBitrate)
	}

	out := a.makeOutput(s)

	took, err := a.encodeJob(picture, out, "making", dur, args)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %v", err)
	}
	return a.finishJob(picture, "video", out, took, "make"), nil
}

// makeImage generates a single-frame test image.
func (a *application) makeImage(s makeSpec) (*fileReport, error) {
	s = s.resolve()
	levels := compressionLevels[coalesce(s.level, "compress")]
	img := imageFormats[s.format]
	w, h := makeSize(s.res)

	picture, filters := makePictureSource(s.content, w, h, 60)
	args := []string{"-f", "lavfi", "-i", picture}
	args = append(args, filters...)
	args = append(args, imageCodecArgs(img.codec, levels.imageQuality, levels.webpQuality)...)

	out := a.makeOutput(s)
	args = append(args, "-frames:v", "1")

	took, err := a.encodeJob(picture, out, "making", 0, args)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %v", err)
	}
	return a.finishJob(picture, "image", out, took, "make"), nil
}

// makeAudio generates a test tone or noise track.
func (a *application) makeAudio(s makeSpec) (*fileReport, error) {
	s = s.resolve()
	levels := compressionLevels[coalesce(s.level, "compress")]
	af := audioFormats[s.format]
	hz, _ := parseMakeHz(s.hz)
	dur, _ := parseMakeDuration(s.dur)

	var source string
	if s.content == "noise" {
		source = "anoisesrc=color=white:sample_rate=48000"
	} else {
		source = fmt.Sprintf("sine=frequency=%d:sample_rate=48000", hz)
	}

	args := []string{"-f", "lavfi", "-i", source, "-t", durString(dur), "-c:a", af.codec}
	args = append(args, audioEncodeArgs(af.codec, levels)...)

	out := a.makeOutput(s)

	took, err := a.encodeJob(source, out, "making", dur, args)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %v", err)
	}
	return a.finishJob(source, "audio", out, took, "make"), nil
}

// makeTargetWords lists the make-specific completion words.
func makeTargetWords() []string {
	words := sortedKeys(makeColors)
	words = append(words, "bars", "test", "noise", "sine")
	words = append(words, sortedKeys(makeFPS)...)
	sort.Strings(words)
	return words
}

// runMake generates test media in the current directory.
func (a *application) runMake(args []string) {
	a.requireDeps()
	spec, err := parseMakeTargets(args)
	if err != nil {
		a.exitError("%v", err)
	}

	if a.dryRun {
		s := spec.resolve()
		out := a.makeOutput(s)
		if a.machine {
			a.machineFiles = append(a.machineFiles, machineFile{Input: "(generated)", Output: out, Kind: s.kind})
			a.machineDryRun = true
			a.emitMachineOutput("", false, nil, 0, 1)
			return
		}
		fmt.Fprintf(a.stderr, "%s dry run: would generate %s\n", a.doneMark(), a.ui.bold(out))
		return
	}

	start := time.Now()
	var rep *fileReport
	switch spec.kind {
	case "image":
		rep, err = a.makeImage(spec)
	case "audio":
		rep, err = a.makeAudio(spec)
	default:
		rep, err = a.makeVideo(spec)
	}
	if err != nil {
		a.exitError("%v", err)
	}
	if a.machine {
		a.recordMachineFile(rep)
		a.emitMachineOutput("", false, nil, time.Since(start), -1)
	} else {
		rep.print()
	}
}
