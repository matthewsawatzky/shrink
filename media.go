package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// outputFileName builds the output name from what was actually done:
//
//	<original>-<operations>.<ext>
//
// Tags appear only for work that actually happened: an explicit resolution
// (scaling), a cross-kind operation ("audio", "frame"), or an explicit
// compression level. The format change itself isn't tagged - the extension
// already shows it - so a plain re-encode or conversion falls back to
// "-compressed". The extension always reflects the true output format.
func outputFileName(input, outKind, fmtName, operation, resolution, level string) string {
	dir := filepath.Dir(input)
	stem := candidateBase(input, outKind, fmtName, operation, resolution, level)
	ext := outExt(outKind, fmtName)
	out := filepath.Join(dir, stem+"."+ext)
	// avoid overwriting existing files: bump a counter (unless -o)
	for i := 1; !reserveOut(out); i++ {
		out = filepath.Join(dir, fmt.Sprintf("%s-%d.%s", stem, i, ext))
	}
	return out
}

// candidateBase is the "<original>-<operations>" part of an output name,
// without extension or directory.
func candidateBase(input, outKind, fmtName, operation, resolution, level string) string {
	base := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))

	var parts []string
	for _, tag := range []string{resolution, operation, level} {
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

// outExt maps a format target to the file extension it really produces
// (hevc and av1 ride in mp4 containers; audio "aac" lands in .m4a).
func outExt(outKind, fmtName string) string {
	switch outKind {
	case "image":
		if f, ok := imageFormats[fmtName]; ok {
			return f.ext
		}
	case "audio":
		if f, ok := audioFormats[fmtName]; ok {
			return f.ext
		}
	case "video":
		if f, ok := videoFormats[fmtName]; ok && f.ext != "" {
			return f.ext
		}
	}
	return fmtName
}

// reservedOut tracks output paths handed out this run. Parallel workers
// can pick names before ffmpeg has created the file (two inputs like
// clip.mp4 + clip.mkv both map to clip-compressed.mp4), so a plain
// fileExists check would race; reserving is check-and-set.
var (
	reservedMu  sync.Mutex
	reservedOut = map[string]bool{}
)

// reserveOut claims path for this run. With -o it claims existing files
// too (ffmpeg -y replaces them); otherwise a prior claim or an existing
// file makes it unavailable.
func reserveOut(path string) bool {
	reservedMu.Lock()
	defer reservedMu.Unlock()
	if reservedOut[path] {
		return false
	}
	if !overwrite && fileExists(path) {
		return false
	}
	reservedOut[path] = true
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
func probeFile(path string) mediaInfo {
	cmd := exec.Command("ffprobe", "-v", "error", "-show_entries",
		"format=duration,bit_rate:stream=codec_type,codec_name,height",
		"-of", "json", path)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return mediaInfo{}
	}
	var probe struct {
		Format struct {
			Duration string `json:"duration"`
			BitRate  string `json:"bit_rate"`
		}
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			Height    int    `json:"height"`
		}
	}
	if json.Unmarshal(out, &probe) != nil {
		return mediaInfo{}
	}
	info := mediaInfo{ok: true}
	for _, s := range probe.Streams {
		switch s.CodecType {
		case "audio":
			info.hasAudio = true
			info.audioCodec = s.CodecName
		case "video":
			info.hasVideo = true
			info.videoCodec = s.CodecName
			if s.Height > info.height {
				info.height = s.Height
			}
		}
	}
	if d, err := strconv.ParseFloat(strings.TrimSpace(probe.Format.Duration), 64); err == nil {
		info.duration = d
	}
	if br, err := strconv.ParseInt(strings.TrimSpace(probe.Format.BitRate), 10, 64); err == nil && br > 0 {
		info.bitRate = br
	}
	return info
}

// probeDuration returns the input's duration in seconds via a single
// ffprobe call; 0 if unknown (or the probe fails). Used by the progress
// bar, which only needs the number.
func probeDuration(path string) float64 {
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

// humanSize renders a byte count in the unit that keeps it readable:
// sub-megabyte files show as KB, everything else as MB. Verbose mode
// always uses KB so sizes are directly comparable.
func humanSize(b int64) string {
	if verbose || b < 1024*1024 {
		return fmt.Sprintf("%.0fKB", float64(b)/1024)
	}
	return fmt.Sprintf("%.2fMB", float64(b)/1024/1024)
}

// sizeWarning reports when the "compressed" output ended up bigger than
// the input, since that defeats the point of the tool.
func sizeWarning(inPath, outPath string) string {
	inSize, _ := os.Stat(inPath)
	outSize, _ := os.Stat(outPath)
	if inSize == nil || outSize == nil {
		return ""
	}
	if outSize.Size() >= inSize.Size() {
		return fmt.Sprintf("output is not smaller than input (%s > %s) - this format/settings combo gains nothing here",
			humanSize(outSize.Size()), humanSize(inSize.Size()))
	}
	return ""
}

// savingsText renders the size delta as "↓ 62%" (or "↑ 4%" when the output
// grew), colored green for a win and yellow when it gained nothing.
// Plain arrows become -/+ in unstyled mode so piped logs stay ASCII.
func savingsText(inBytes, outBytes int64) string {
	if inBytes <= 0 {
		return ""
	}
	pct := int((1 - float64(outBytes)/float64(inBytes)) * 100)
	arrow := "↓"
	if !ui.on {
		arrow = "-"
	}
	if pct >= 0 {
		return ui.green(fmt.Sprintf("%s %d%%", arrow, pct))
	}
	if !ui.on {
		arrow = "+"
	} else {
		arrow = "↑"
	}
	return ui.yellow(fmt.Sprintf("%s %d%%", arrow, -pct))
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
var ffmpegEncoders = sync.OnceValue(func() map[string]bool {
	out, err := exec.Command("ffmpeg", "-hide_banner", "-encoders").Output()
	if err != nil {
		return map[string]bool{}
	}
	avail := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) >= 2 {
			avail[f[1]] = true
		}
	}
	return avail
})

func encoderAvailable(name string) bool { return ffmpegEncoders()[name] }

// bitrateForLevel scales a source bitrate down to what the compression
// level asks for, in kbps, with a floor so tiny sources don't turn to mud.
func bitrateForLevel(compress string, bitRate int64) int {
	factor := 0.5
	switch compress {
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
func hwEncodeFor(codec, compress string, info mediaInfo) (string, []string, bool) {
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
	if !encoderAvailable(hw) {
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
	}
	return lv.videoCRF
}

func handleVideo(input string, resolution string, videoFmt string, compress string, info mediaInfo) (*fileReport, error) {
	if videoFmt == "" {
		videoFmt = "mp4"
	}
	level := compress
	if compress == "" {
		compress = "compress"
	}
	levels := compressionLevels[compress]
	vf := videoFormats[videoFmt]

	// -f: swap in the hardware encoder for x264/x265 targets when one
	// exists, trading some efficiency for a much faster encode
	codec, encArgs := vf.codec, vf.args
	crf := videoCRF(codec, levels)
	if fast {
		if hw, hwArgs, ok := hwEncodeFor(codec, compress, info); ok {
			codec = hw
			if videoFmt == "mp4" || videoFmt == "mov" {
				hwArgs = append(hwArgs, "-movflags", "+faststart")
			}
			encArgs = hwArgs
			crf = ""
		}
	}

	args := []string{"-i", input, "-c:v", codec}
	if crf != "" {
		args = append(args, "-crf", crf)
	}
	args = append(args, encArgs...)

	if videoFmt != "webm" {
		args = append(args, "-c:a", "aac", "-b:a", levels.audioBitrate)
	} else {
		args = append(args, "-c:a", "libopus", "-b:a", levels.audioBitrate)
	}

	if resolution != "" {
		args = append(args, "-vf", resolutions[resolution])
	}

	out := outputFileName(input, "video", videoFmt, "", resolution, level)
	args = append(args, out)

	label := "compressing"
	if resolution != "" {
		label = "compressing + scaling"
	}
	took, err := runFFmpegWithDuration(label, info.duration, args...)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %v", err)
	}
	return finishJob(input, out, took, "compress"), nil
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

func handleImage(input string, resolution string, imageFmt string, compress string) (*fileReport, error) {
	if imageFmt == "" {
		imageFmt = "jpeg"
	}
	level := compress
	if compress == "" {
		compress = "compress"
	}
	levels := compressionLevels[compress]
	img := imageFormats[imageFmt]

	args := []string{"-i", input}
	if resolution != "" {
		args = append(args, "-vf", resolutions[resolution])
	}
	args = append(args, imageCodecArgs(img.codec, levels.imageQuality, levels.webpQuality)...)

	out := outputFileName(input, "image", imageFmt, "", resolution, level)
	args = append(args, "-frames:v", "1", out)

	label := "compressing"
	if resolution != "" {
		label = "compressing + scaling"
	}
	took, err := runFFmpeg(label, args...)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %v", err)
	}
	return finishJob(input, out, took, "compress"), nil
}

func handleAudio(input string, audioFmt string, compress string, info mediaInfo) (*fileReport, error) {
	if audioFmt == "" {
		audioFmt = "mp3"
	}
	level := compress
	if compress == "" {
		compress = "compress"
	}
	levels := compressionLevels[compress]
	af := audioFormats[audioFmt]

	args := []string{"-i", input, "-c:a", af.codec}
	if af.codec == "pcm_s16le" || af.codec == "flac" {
		// lossless: ignore compression level
	} else if af.codec == "libvorbis" {
		args = append(args, "-q:a", levels.vorbisQ)
	} else {
		args = append(args, "-b:a", levels.audioBitrate)
	}

	out := outputFileName(input, "audio", audioFmt, "", "", level)
	args = append(args, out)

	took, err := runFFmpegWithDuration("compressing", info.duration, args...)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %v", err)
	}
	return finishJob(input, out, took, "compress"), nil
}

// fileReport is one finished job's outcome. Parallel batches hold
// reports back and print them in input order once the run completes.
type fileReport struct {
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
		fmt.Fprintf(os.Stderr, "%s %s %s\n", doneMark(), ui.bold(r.out), ui.dim("took "+humanDuration(r.took.Seconds())))
		printStepsFor(r.input)
		return
	}
	if r.inBytes > 0 {
		fmt.Fprintf(os.Stderr, "%s %s %s  %s  %s\n",
			doneMark(), ui.bold(r.out),
			ui.dim(fmt.Sprintf("(%s → %s)", humanSize(r.inBytes), humanSize(r.outBytes))),
			savingsText(r.inBytes, r.outBytes),
			ui.dim("took "+humanDuration(r.took.Seconds())))
	} else {
		fmt.Fprintf(os.Stderr, "%s %s %s\n", doneMark(), ui.bold(r.out), ui.dim("took "+humanDuration(r.took.Seconds())))
	}
	if r.warning != "" {
		fmt.Fprintf(os.Stderr, "%s %s %s\n", ui.yellow("!"), ui.yellow("warning:"), r.warning)
	}
	if r.deleted {
		fmt.Fprintf(os.Stderr, "  %s\n", ui.dim("deleted original "+filepath.Base(r.input)))
	} else if r.delNote != "" {
		fmt.Fprintf(os.Stderr, "%s %s\n", ui.yellow("!"), ui.yellow(r.delNote))
	}
	printStepsFor(r.input)
}

// finishJob records a completed job. Human mode prints the summary line
// and verbose steps unless a parallel batch is collecting reports;
// machine mode appends a JSON result for the final document.
func finishJob(input, out string, took time.Duration, operation string) *fileReport {
	statDone := trackStep(input, "stat output")
	inSize, _ := os.Stat(input)
	outSize, err := os.Stat(out)
	statDone()

	r := &fileReport{
		input: input, out: out, kind: detectKind(input), operation: operation,
		took: took, statOK: err == nil,
	}
	if inSize != nil {
		r.inBytes = inSize.Size()
	}
	if err == nil && outSize != nil {
		r.outBytes = outSize.Size()
	}
	r.warning = sizeWarning(input, out)

	// -d: remove the original once a smaller replacement exists. Derived
	// outputs (audio tracks, frame grabs) never qualify, and neither does
	// an "output" that didn't actually shrink.
	if deleteAfter {
		switch {
		case operation != "compress" && operation != "rescale":
			r.delNote = "kept the original: this run made a derived file, not a compressed version"
		case !r.statOK:
			r.delNote = "kept the original: the output could not be verified"
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

	if machine {
		f := machineFile{
			Input: r.input, Output: r.out, Kind: r.kind, Operation: r.operation,
			InputBytes: r.inBytes, OutputBytes: r.outBytes,
			TookMS: r.took.Milliseconds(), Warning: r.warning, Steps: machineStepsFor(r.input),
			DeletedInput: r.deleted,
		}
		if r.inBytes > 0 {
			f.SavingsPercent = round2((1 - float64(r.outBytes)/float64(r.inBytes)) * 100)
		}
		machineFilesMu.Lock()
		machineFiles = append(machineFiles, f)
		machineFilesMu.Unlock()
		return r
	}

	if !parallel {
		r.print()
	}
	return r
}

// handleAudioFromVideo extracts the audio track of a video into its own
// file, e.g. "shrink clip.mp4 mp3".
func handleAudioFromVideo(input string, audioFmt string, compress string, info mediaInfo) (*fileReport, error) {
	level := compress
	if compress == "" {
		compress = "compress"
	}
	levels := compressionLevels[compress]
	af := audioFormats[audioFmt]

	args := []string{"-i", input, "-vn", "-c:a", af.codec}
	switch af.codec {
	case "pcm_s16le", "flac":
		// lossless: ignore compression level
	case "libvorbis":
		args = append(args, "-q:a", levels.vorbisQ)
	default:
		args = append(args, "-b:a", levels.audioBitrate)
	}

	out := outputFileName(input, "audio", audioFmt, "audio", "", level)
	args = append(args, out)

	took, err := runFFmpegWithDuration("extracting audio", info.duration, args...)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %v", err)
	}
	return finishJob(input, out, took, "extract_audio"), nil
}

// handleVideoToImage grabs a single frame from a video, e.g.
// "shrink clip.mp4 png". Resolution target scales the grabbed frame.
func handleVideoToImage(input string, resolution string, imageFmt string, compress string, info mediaInfo) (*fileReport, error) {
	level := compress
	if compress == "" {
		compress = "compress"
	}
	levels := compressionLevels[compress]
	img := imageFormats[imageFmt]

	args := []string{"-i", input}
	if resolution != "" {
		args = append(args, "-vf", resolutions[resolution])
	}
	args = append(args, imageCodecArgs(img.codec, levels.imageQuality, levels.webpQuality)...)

	out := outputFileName(input, "image", imageFmt, "frame", resolution, level)
	args = append(args, "-frames:v", "1", "-update", "1", out)

	took, err := runFFmpegWithDuration("grabbing frame", info.duration, args...)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %v", err)
	}
	return finishJob(input, out, took, "grab_frame"), nil
}

// transparentCRF is the near-lossless grade used when the only real work
// is rescaling one of shrink's own outputs: compressing an already
// compressed file again would stack losses for no gain.
func transparentCRF(codec string) string {
	switch codec {
	case "libx265":
		return "22"
	case "libsvtav1":
		return "30"
	case "libvpx-vp9":
		return "28"
	}
	return "18" // libx264 and anything unknown
}

// handleRescaleVideo downscales one of shrink's own outputs without
// stacking another compression pass on it: the rescale encodes at a
// near-lossless grade, and an AAC track is simply copied.
func handleRescaleVideo(input string, resolution string, videoFmt string, info mediaInfo) (*fileReport, error) {
	vf := videoFormats[videoFmt]

	args := []string{"-i", input, "-c:v", vf.codec, "-crf", transparentCRF(vf.codec)}
	args = append(args, vf.args...)
	args = append(args, "-vf", resolutions[resolution])

	switch {
	case info.audioCodec == "aac" && videoFmt != "webm":
		args = append(args, "-c:a", "copy")
	case videoFmt != "webm":
		args = append(args, "-c:a", "aac", "-b:a", compressionLevels["compress"].audioBitrate)
	default:
		args = append(args, "-c:a", "libopus", "-b:a", compressionLevels["compress"].audioBitrate)
	}

	out := outputFileName(input, "video", videoFmt, "", resolution, "")
	args = append(args, out)

	took, err := runFFmpegWithDuration("downscaling", info.duration, args...)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %v", err)
	}
	return finishJob(input, out, took, "rescale"), nil
}

// handleRescaleImage downscales an already-compressed image at a
// near-lossless quality instead of compressing it twice.
func handleRescaleImage(input string, resolution string, imageFmt string) (*fileReport, error) {
	img := imageFormats[imageFmt]

	args := []string{"-i", input}
	args = append(args, "-vf", resolutions[resolution])
	args = append(args, imageCodecArgs(img.codec, "2", "95")...)

	out := outputFileName(input, "image", imageFmt, "", resolution, "")
	args = append(args, "-frames:v", "1", out)

	took, err := runFFmpeg("downscaling", args...)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %v", err)
	}
	return finishJob(input, out, took, "rescale"), nil
}
