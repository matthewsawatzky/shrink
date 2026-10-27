package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// claimOutput reserves stem.ext in dir, bumping a counter when the name
// is taken (an existing file, or a claim made earlier this run).
func (a *application) claimOutput(dir, stem, ext string) string {
	out := filepath.Join(dir, stem+"."+ext)
	for i := 1; !a.reserveOut(out); i++ {
		out = filepath.Join(dir, fmt.Sprintf("%s-%d.%s", stem, i, ext))
	}
	return out
}

// candidateBase is the "<original>-<operations>" part of an output name,
// without extension or directory.
func candidateBase(input, operation, resolution, level, speed string) string {
	base := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))

	var parts []string
	for _, tag := range []string{resolution, speed, operation, level} {
		if tag != "" {
			parts = append(parts, tag)
		}
	}
	if len(parts) == 0 {
		// nothing else to name it by: a plain re-encode or conversion
		parts = append(parts, "compressed")
	}
	return base + "-" + strings.Join(parts, "-")
}

// setptsFilter returns the video timestamp filter for one speed target:
// setpts=PTS/5 packs frames five times closer (faster), PTS/0.1 spreads
// them out (slower).
func setptsFilter(speed string) string {
	return "setpts=PTS/" + strings.TrimPrefix(speed, "x")
}

// playbackStretch returns the factor a speed target stretches the output
// timeline by: x2 packs it to half the input's length, x0.1 makes it ten
// times longer. 1 without a speed target.
func playbackStretch(speed string) float64 {
	if speed == "" {
		return 1
	}
	v, _, _ := parseSpeed(speed)
	return 1 / v
}

// progressDuration maps the input's duration onto the output timeline
// for the progress bar: the bar tracks ffmpeg's output timestamps
// (out_time), which x-targets compress or stretch, so a bare input
// duration would report x2 runs as half done and x0.1 runs as 10x done.
// 0 passes through (unknown durations get the spinner either way).
func progressDuration(info mediaInfo, speed string) float64 {
	if info.duration <= 0 {
		return 0
	}
	return info.duration * playbackStretch(speed)
}

// atempoChain renders one speed factor as an atempo filter chain. A
// single atempo only accepts 0.5 through 100, so factors outside that
// range chain stages: x0.1 becomes atempo=0.5,atempo=0.5,atempo=0.5,atempo=0.8.
func atempoChain(speed float64) string {
	if speed <= 0 || math.IsNaN(speed) || math.IsInf(speed, 0) {
		return ""
	}
	f := speed
	var stages []string
	for f < 0.5 {
		stages = append(stages, "atempo=0.5")
		f *= 2
	}
	for f > 100 {
		stages = append(stages, "atempo=100")
		f /= 100
	}
	stages = append(stages, "atempo="+strconv.FormatFloat(f, 'g', -1, 64))
	return strings.Join(stages, ",")
}

// outExt maps a format target to the file extension it really produces
// (hevc and av1 ride in mp4 containers; audio "aac" lands in .m4a).
func outExt(kind, name string) string {
	if f, ok := formatsForKind(kind)[name]; ok {
		return f.ext
	}
	return name
}

// reservedOut tracks output paths handed out this run. Parallel workers
// can pick names before ffmpeg has created the file (two inputs like
// clip.mp4 + clip.mkv both map to clip-compressed.mp4), so a plain
// fileExists check would race; reserving is check-and-set.

// reserveOut claims path for this run. With -o it claims existing files
// too (ffmpeg -y replaces them); otherwise a prior claim or an existing
// file makes it unavailable.
func (a *application) reserveOut(path string) bool {
	a.reservedMu.Lock()
	defer a.reservedMu.Unlock()
	if a.reservedOut[path] {
		return false
	}
	if !a.overwrite && fileExists(path) {
		return false
	}
	a.reservedOut[path] = true
	return true
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// mediaInfo describes everything shrink needs to know about an input
// before encoding: whether ffmpeg can actually read it, its duration in
// seconds (0 when absent, e.g. images), its bitrate (drives -f's
// hardware rate), its dimensions, and the codecs inside it. One ffprobe
// call collects it all.
type mediaInfo struct {
	ok         bool
	formatName string
	frames     int64
	streams    map[string]int
	hasAudio   bool
	hasVideo   bool
	duration   float64
	bitRate    int64  // bits per second; 0 when unknown
	height     int    // tallest video/image stream; 0 when unknown
	videoCodec string // e.g. h264, hevc, mjpeg
	audioCodec string // e.g. aac, mp3
}

// probeFile gathers input metadata via a single ffprobe invocation
// (JSON) instead of one decode + two probe calls per file. A file that
// ffprobe cannot read at all - wrong extension or garbage contents -
// reports ok=false.
func (a *application) probeFile(path string) mediaInfo {
	args := []string{"-v", "error", "-show_entries", "format=format_name,duration,bit_rate:stream=codec_type,codec_name,height,nb_read_frames", "-of", "json"}
	if detectKind(path) == "image" {
		args = append(args, "-count_frames", "-read_intervals", "%+#2")
	}
	args = append(args, "-i", path)
	cmd := a.command("ffprobe", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return mediaInfo{}
	}
	var probe struct {
		Format struct {
			Name     string `json:"format_name"`
			Duration string `json:"duration"`
			BitRate  string `json:"bit_rate"`
		}
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			Height    int    `json:"height"`
			Frames    string `json:"nb_read_frames"`
		}
	}
	if json.Unmarshal(out, &probe) != nil {
		return mediaInfo{}
	}
	info := mediaInfo{formatName: probe.Format.Name, streams: map[string]int{}}
	for _, s := range probe.Streams {
		info.streams[s.CodecType]++
		if n, err := strconv.ParseInt(s.Frames, 10, 64); err == nil && n > info.frames {
			info.frames = n
		}
		switch s.CodecType {
		case "audio":
			info.hasAudio = true
			if info.audioCodec == "" {
				info.audioCodec = s.CodecName
			}
		case "video":
			info.hasVideo = true
			if info.videoCodec == "" {
				info.videoCodec = s.CodecName
			}
			if s.Height > info.height {
				info.height = s.Height
			}
		}
	}
	if d, err := strconv.ParseFloat(strings.TrimSpace(probe.Format.Duration), 64); err == nil && d >= 0 && !math.IsNaN(d) && !math.IsInf(d, 0) {
		info.duration = d
	}
	if br, err := strconv.ParseInt(strings.TrimSpace(probe.Format.BitRate), 10, 64); err == nil && br > 0 {
		info.bitRate = br
	}
	info.ok = info.hasAudio || info.hasVideo
	return info
}

// humanSize renders a byte count in the unit that keeps it readable:
// sub-megabyte files show as KB, everything else as MB. Verbose mode
// always uses KB so sizes are directly comparable.
func (a *application) humanSize(b int64) string {
	if a.verbose || b < 1024*1024 {
		return fmt.Sprintf("%.0fKB", float64(b)/1024)
	}
	return fmt.Sprintf("%.2fMB", float64(b)/1024/1024)
}

// savingsText renders the size delta as "↓ 62%" (or "↑ 4%" when the output
// grew), colored green for a win and yellow when it gained nothing.
// Plain arrows become -/+ in unstyled mode so piped logs stay ASCII.
func (a *application) savingsText(inBytes, outBytes int64) string {
	if inBytes <= 0 {
		return ""
	}
	pct := int((1 - float64(outBytes)/float64(inBytes)) * 100)
	arrow := "↓"
	if !a.ui.on {
		arrow = "-"
	}
	if pct >= 0 {
		return a.ui.green(fmt.Sprintf("%s %d%%", arrow, pct))
	}
	if !a.ui.on {
		arrow = "+"
	} else {
		arrow = "↑"
	}
	return a.ui.yellow(fmt.Sprintf("%s %d%%", arrow, -pct))
}

// parseMissingEncoder pulls the encoder name out of ffmpeg's
// "Unknown encoder 'xyz'" error so we can explain it nicely.
func parseMissingEncoder(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		if i := strings.Index(line, "Unknown encoder '"); i >= 0 {
			rest := line[i+len("Unknown encoder '"):]
			if j := strings.Index(rest, "'"); j >= 0 {
				return rest[:j]
			}
		}
	}
	return ""
}

// firstMeaningfulLine returns the first stderr line that isn't ffmpeg
// bookkeeping noise, for compact error reporting.
func firstMeaningfulLine(stderr string) string {
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Error opening output") {
			continue
		}
		return line
	}
	return "unknown error"
}

// The handle* functions run one compression job and return the output
// path. Errors are returned instead of exiting so batch runs can keep
// going. duration is the input's length in seconds from the earlier
// probe, passed down for the progress bar - 0 means unknown.

// ffmpegEncoders lists this build's encoders once per run, so -f can
// check for hardware encoders without spawning ffmpeg every file.

func (a *application) encoderAvailable(name string) bool { return a.ffmpegEncoders()[name] }

// bitrateForLevel scales a source bitrate down to what the compression
// level asks for, in kbps, with a floor so tiny sources don't turn to mud.
func bitrateForLevel(compress string, bitRate int64) int {
	factor := 0.5
	switch compress {
	case "transparent": // hardware has no CRF; a fat bitrate nears it
		factor = 0.6
	case "spacesaver":
		factor = 0.35
	case "neversee":
		factor = 0.2
	}
	kbps := int(float64(bitRate) * factor / 1000)
	if kbps < 250 {
		kbps = 250
	}
	return kbps
}

// hwEncodeFor maps a software codec to its hardware counterpart on
// macOS (VideoToolbox) when -f asked for speed. Hardware encoders have
// no CRF, so the args carry a bitrate derived from the source instead.
// ok=false means no hardware route and software proceeds unchanged.
func (a *application) hwEncodeFor(codec, compress string, info mediaInfo) (string, []string, bool) {
	if runtime.GOOS != "darwin" || info.bitRate <= 0 {
		return "", nil, false
	}
	var hw string
	switch codec {
	case "libx264":
		hw = "h264_videotoolbox"
	case "libx265":
		hw = "hevc_videotoolbox"
	default:
		return "", nil, false
	}
	if !a.encoderAvailable(hw) {
		return "", nil, false
	}
	return hw, []string{"-b:v", fmt.Sprintf("%dk", bitrateForLevel(compress, info.bitRate))}, true
}

// videoCRF picks the level's CRF for a codec family: each family has
// its own quality scale, so the level table keeps one value per family.
func videoCRF(codec string, lv levelSpec) string {
	switch codec {
	case "libx265":
		return lv.hevcCRF
	case "libsvtav1":
		return lv.av1CRF
	case "libvpx-vp9":
		if lv.vp9CRF != "" {
			return lv.vp9CRF
		}
	}
	return lv.videoCRF
}

// imageCodecArgs builds the encoder flags for one still-image output,
// shared by image-to-image and video frame grabs.
func imageCodecArgs(codec, quality, webpQ string) []string {
	switch codec {
	case "mjpeg":
		return []string{"-c:v", "mjpeg", "-q:v", quality}
	case "png":
		return []string{"-c:v", "png", "-compression_level", "9"}
	case "libwebp":
		return []string{"-c:v", "libwebp", "-quality", webpQ}
	default: // gif
		return []string{"-c:v", "gif"}
	}
}

// audioEncodeArgs adds the quality settings for one audio codec at one
// compression level; shared by audio conversion, track extraction and
// shrink make.
func audioEncodeArgs(codec string, levels levelSpec) []string {
	switch {
	case codec == "pcm_s16le", codec == "flac":
		return nil // lossless: the level does not apply
	case codec == "libvorbis":
		return []string{"-q:a", levels.vorbisQ}
	}
	return []string{"-b:a", levels.audioBitrate}
}

// fileReport is one finished job's outcome. Parallel batches hold
// reports back and print them in input order once the run completes.
type fileReport struct {
	app       *application
	input     string
	out       string
	kind      string
	operation string
	took      time.Duration
	inBytes   int64
	outBytes  int64
	statOK    bool // output file could be stat'ed
	warning   string
	noop      bool // nothing to do: the input already fits the request
	noopNote  string
	deleted   bool // -d removed the original
	delNote   string
}

// print reports a finished job in human mode: the done line, any size
// warning, and this file's verbose step list.
func (r *fileReport) print() {
	if !r.statOK {
		fmt.Fprintf(r.app.stderr, "%s %s %s\n", r.app.doneMark(), r.app.ui.bold(r.out), r.app.ui.dim("took "+r.app.humanDuration(r.took.Seconds())))
		r.app.printStepsFor(r.input)
		return
	}
	if r.inBytes > 0 {
		fmt.Fprintf(r.app.stderr, "%s %s %s  %s  %s\n",
			r.app.doneMark(), r.app.ui.bold(r.out),
			r.app.ui.dim(fmt.Sprintf("(%s → %s)", r.app.humanSize(r.inBytes), r.app.humanSize(r.outBytes))),
			r.app.savingsText(r.inBytes, r.outBytes),
			r.app.ui.dim("took "+r.app.humanDuration(r.took.Seconds())))
	} else {
		fmt.Fprintf(r.app.stderr, "%s %s %s\n", r.app.doneMark(), r.app.ui.bold(r.out), r.app.ui.dim("took "+r.app.humanDuration(r.took.Seconds())))
	}
	if r.warning != "" {
		fmt.Fprintf(r.app.stderr, "%s %s %s\n", r.app.ui.yellow("!"), r.app.ui.yellow("warning:"), r.warning)
	}
	if r.deleted {
		fmt.Fprintf(r.app.stderr, "  %s\n", r.app.ui.dim("deleted original "+filepath.Base(r.input)))
	}
	if r.delNote != "" {
		fmt.Fprintf(r.app.stderr, "%s %s\n", r.app.ui.yellow("!"), r.app.ui.yellow(r.delNote))
	}
	r.app.printStepsFor(r.input)
}

// finishJob builds a completed result and applies the verified deletion policy.
// The command coordinator owns rendering and aggregation.
func (a *application) finishJob(input, kind, out string, took time.Duration, operation string) *fileReport {
	statDone := a.trackStep(input, "stat output")
	inSize, _ := os.Stat(input)
	outSize, err := os.Stat(out)
	statDone()

	r := &fileReport{app: a,
		input: input, out: out, kind: kind, operation: operation,
		took: took, statOK: err == nil,
	}
	if inSize != nil {
		r.inBytes = inSize.Size()
	}
	if err == nil && outSize != nil {
		r.outBytes = outSize.Size()
	}
	if inSize != nil && r.statOK && r.outBytes >= r.inBytes {
		r.warning = fmt.Sprintf("output is not smaller than input (%s >= %s) - this format/settings combo gains nothing here", a.humanSize(r.outBytes), a.humanSize(r.inBytes))
	}

	// -d: remove the original once a smaller replacement exists. Derived
	// outputs (audio tracks, frame grabs) never qualify, generated files
	// have no original to remove, and neither does an "output" that
	// didn't actually shrink.
	if a.deleteAfter {
		switch {
		case operation == "make":
			// generated files have no original
		case operation != "compress" && operation != "rescale":
			r.delNote = "kept the original: this run made a derived file, not a compressed version"
		case !r.statOK:
			r.delNote = "kept the original: the output could not be verified"
		case !a.replacementSafe(input, out):
			r.delNote = "kept the original: the output does not preserve all required streams or frames"
		case r.outBytes >= r.inBytes:
			r.delNote = "kept the original: the output is not smaller"
		default:
			if err := os.Remove(input); err != nil {
				r.delNote = fmt.Sprintf("could not delete the original: %v", err)
			} else {
				r.deleted = true
			}
		}
	}

	return r
}

func (a *application) recordMachineFile(r *fileReport) {

	f := machineFile{
		Input: r.input, Output: r.out, Kind: r.kind, Operation: r.operation,
		InputBytes: r.inBytes, OutputBytes: r.outBytes,
		TookMS: r.took.Milliseconds(), Warning: r.warning, Steps: a.machineStepsFor(r.input),
		DeletedInput: r.deleted,
	}
	if r.operation == "make" {
		f.Input = "(generated)"
	}
	if r.delNote != "" {
		f.Warning = strings.TrimSpace(f.Warning + " " + r.delNote)
	}
	if r.inBytes > 0 {
		f.SavingsPercent = round2((1 - float64(r.outBytes)/float64(r.inBytes)) * 100)
	}
	a.machineFiles = append(a.machineFiles, f)
}
