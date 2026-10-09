package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	ext := filepath.Ext(fmtName)
	switch outKind {
	case "image":
		if f, ok := imageFormats[fmtName]; ok {
			ext = f.ext
		}
	case "audio":
		if f, ok := audioFormats[fmtName]; ok {
			ext = f.ext
		}
	case "video":
		ext = fmtName
	}
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

	// write the output next to the original file, not in the cwd
	dir := filepath.Dir(input)
	name := base + "-" + strings.Join(parts, "-")
	out := filepath.Join(dir, name+"."+ext)
	// avoid overwriting existing files: bump a counter
	for i := 1; !reserveOut(out); i++ {
		out = filepath.Join(dir, fmt.Sprintf("%s-%d.%s", name, i, ext))
	}
	return out
}

// reservedOut tracks output paths handed out this run. Parallel workers
// can pick names before ffmpeg has created the file (two inputs like
// clip.mp4 + clip.mkv both map to clip-compressed.mp4), so a plain
// fileExists check would race; reserving is check-and-set.
var (
	reservedMu  sync.Mutex
	reservedOut = map[string]bool{}
)

// reserveOut claims path for this run if no existing file or prior
// claim holds it.
func reserveOut(path string) bool {
	reservedMu.Lock()
	defer reservedMu.Unlock()
	if reservedOut[path] || fileExists(path) {
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
// seconds (0 when absent, e.g. images), and whether it has an audio
// track. One ffprobe call collects it all.
type mediaInfo struct {
	ok       bool
	hasAudio bool
	hasVideo bool
	duration float64
}

// probeFile gathers input metadata via a single ffprobe invocation
// (JSON) instead of one decode + two probe calls per file. A file that
// ffprobe cannot read at all - wrong extension or garbage contents -
// reports ok=false.
func probeFile(path string) mediaInfo {
	cmd := exec.Command("ffprobe", "-v", "error", "-show_entries",
		"format=duration:stream=codec_type",
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
		}
		Streams []struct {
			CodecType string `json:"codec_type"`
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
		case "video":
			info.hasVideo = true
		}
	}
	if d, err := strconv.ParseFloat(strings.TrimSpace(probe.Format.Duration), 64); err == nil {
		info.duration = d
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

func handleVideo(input string, resolution string, videoFmt string, compress string, duration float64) (*fileReport, error) {
	if videoFmt == "" {
		videoFmt = "mp4"
	}
	level := compress
	if compress == "" {
		compress = "compress"
	}
	levels := compressionLevels[compress]
	vf := videoFormats[videoFmt]

	args := []string{"-i", input, "-c:v", vf.codec, "-crf", levels.videoCRF}
	args = append(args, vf.args...)

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
	took, err := runFFmpegWithDuration(label, duration, args...)
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

func handleAudio(input string, audioFmt string, compress string, duration float64) (*fileReport, error) {
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

	took, err := runFFmpegWithDuration("compressing", duration, args...)
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

	if machine {
		f := machineFile{
			Input: r.input, Output: r.out, Kind: r.kind, Operation: r.operation,
			InputBytes: r.inBytes, OutputBytes: r.outBytes,
			TookMS: r.took.Milliseconds(), Warning: r.warning, Steps: machineStepsFor(r.input),
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
func handleAudioFromVideo(input string, audioFmt string, compress string, duration float64) (*fileReport, error) {
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

	took, err := runFFmpegWithDuration("extracting audio", duration, args...)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %v", err)
	}
	return finishJob(input, out, took, "extract_audio"), nil
}

// handleVideoToImage grabs a single frame from a video, e.g.
// "shrink clip.mp4 png". Resolution target scales the grabbed frame.
func handleVideoToImage(input string, resolution string, imageFmt string, compress string, duration float64) (*fileReport, error) {
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

	took, err := runFFmpegWithDuration("grabbing frame", duration, args...)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %v", err)
	}
	return finishJob(input, out, took, "grab_frame"), nil
}
