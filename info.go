package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// streamInfo is one audio/video stream's properties as reported by
// ffprobe. Width/height, fps and frames are video-only; sample_rate
// and channels are audio-only.
type streamInfo struct {
	Index      int     `json:"index"`
	Type       string  `json:"type"`
	Codec      string  `json:"codec"`
	Width      int     `json:"width,omitempty"`
	Height     int     `json:"height,omitempty"`
	FPS        float64 `json:"fps,omitempty"`
	Frames     int64   `json:"frames,omitempty"`
	Duration   float64 `json:"duration,omitempty"`
	SampleRate int     `json:"sample_rate,omitempty"`
	Channels   int     `json:"channels,omitempty"`
}

// formatInfo is the container-level metadata ffprobe returns.
type formatInfo struct {
	Name     string  `json:"name"`
	Duration float64 `json:"duration"`
	BitRate  int64   `json:"bit_rate,omitempty"`
	Size     int64   `json:"size"`
}

// fileInfo is everything shrink info reports for one input path.
type fileInfo struct {
	Path     string       `json:"path"`
	Kind     string       `json:"kind"`
	Format   formatInfo   `json:"format"`
	Streams  []streamInfo `json:"streams"`
	Warnings []string     `json:"warnings,omitempty"`
}

// durationMismatchThreshold is how far stream and container durations
// may diverge before we flag them as suspicious.
const durationMismatchThreshold = 0.01

// probeInfo runs a rich ffprobe on path and returns structured metadata.
// It is separate from probeFile because info needs width, fps, frame
// count, sample rate and channels, which the compression path does not.
func probeInfo(path string) (fileInfo, error) {
	cmd := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "format=format_name,duration,bit_rate,size:stream=index,codec_type,codec_name,width,height,avg_frame_rate,nb_frames,duration,sample_rate,channels",
		"-of", "json", path)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fileInfo{}, fmt.Errorf("ffprobe failed: %s", strings.TrimSpace(stderr.String()))
	}
	return parseProbeInfo(path, out)
}

// parseProbeInfo turns ffprobe JSON into a fileInfo. It is split out so
// tests can feed it fixed probe output without spawning ffmpeg.
func parseProbeInfo(path string, data []byte) (fileInfo, error) {
	var probe struct {
		Format struct {
			Name     string `json:"format_name"`
			Duration string `json:"duration"`
			BitRate  string `json:"bit_rate"`
			Size     string `json:"size"`
		} `json:"format"`
		Streams []struct {
			Index        int    `json:"index"`
			CodecType    string `json:"codec_type"`
			CodecName    string `json:"codec_name"`
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			AvgFrameRate string `json:"avg_frame_rate"`
			NbFrames     string `json:"nb_frames"`
			Duration     string `json:"duration"`
			SampleRate   string `json:"sample_rate"`
			Channels     int    `json:"channels"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return fileInfo{}, fmt.Errorf("could not parse ffprobe output: %v", err)
	}

	info := fileInfo{Path: path, Kind: detectKind(path)}
	info.Format.Name = strings.Split(probe.Format.Name, ",")[0]
	info.Format.Duration = parseDuration(probe.Format.Duration)
	info.Format.BitRate = parseInt64(probe.Format.BitRate)
	info.Format.Size = parseInt64(probe.Format.Size)

	var hasVideo, hasAudio bool
	for _, s := range probe.Streams {
		if s.CodecType != "video" && s.CodecType != "audio" {
			continue
		}
		si := streamInfo{
			Index:    s.Index,
			Type:     s.CodecType,
			Codec:    s.CodecName,
			Duration: parseDuration(s.Duration),
		}
		switch s.CodecType {
		case "video":
			hasVideo = true
			si.Width = s.Width
			si.Height = s.Height
			si.FPS = parseFrameRate(s.AvgFrameRate)
			si.Frames = parseInt64(s.NbFrames)
		case "audio":
			hasAudio = true
			si.SampleRate = int(parseInt64(s.SampleRate))
			si.Channels = s.Channels
		}
		info.Streams = append(info.Streams, si)
	}

	if !hasVideo && !hasAudio {
		info.Warnings = append(info.Warnings, "no video or audio streams found")
	}

	// Flag stream/container duration disagreements, which can confuse the
	// progress bar and make Finder-style panels report the wrong length.
	for _, s := range info.Streams {
		if s.Duration > 0 && info.Format.Duration > 0 {
			if diff := abs(s.Duration-info.Format.Duration) / info.Format.Duration; diff > durationMismatchThreshold {
				info.Warnings = append(info.Warnings, fmt.Sprintf(
					"%s stream duration (%s) differs from container duration (%s)",
					s.Type, humanDuration(s.Duration), humanDuration(info.Format.Duration)))
			}
		}
	}

	return info, nil
}

func parseDuration(s string) float64 {
	if v, ok := atof(strings.TrimSpace(s)); ok {
		return v
	}
	return 0
}

func parseInt64(s string) int64 {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseFrameRate converts ffprobe's "avg_frame_rate" fraction (e.g.
// "30000/1001") to a floating-point fps value.
func parseFrameRate(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "0/0" {
		return 0
	}
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return parseDuration(s)
	}
	num := parseInt64(parts[0])
	den := parseInt64(parts[1])
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// runInfo handles the "shrink info <file>..." subcommand.
func runInfo(paths []string) {
	if len(paths) == 0 {
		exitError("info needs a file, e.g. shrink info clip.mp4")
	}

	requireDeps()

	var infos []fileInfo
	for _, path := range paths {
		info, err := probeInfo(path)
		if err != nil {
			if machine {
				exitMachineError(machineError{Error: err.Error(), Input: path})
			}
			fmt.Fprintf(os.Stderr, "%s %s\n", errMark(), ui.red(fmt.Sprintf("%s: %v", path, err)))
			os.Exit(1)
		}
		infos = append(infos, info)
	}

	if machine {
		printJSON(infos)
		return
	}

	for i, info := range infos {
		if i > 0 {
			fmt.Fprintln(os.Stderr)
		}
		printFileInfo(info)
	}
}

// printFileInfo renders one fileInfo in the tool's compact style.
func printFileInfo(info fileInfo) {
	fmt.Fprintf(os.Stderr, "%s\n", ui.bold(filepath.Base(info.Path)))

	meta := []string{info.Kind}
	if info.Format.Name != "" {
		meta = append(meta, info.Format.Name)
	}
	if info.Format.Size > 0 {
		meta = append(meta, humanSize(info.Format.Size))
	}
	if info.Format.BitRate > 0 {
		meta = append(meta, humanBitRate(info.Format.BitRate))
	}
	fmt.Fprintf(os.Stderr, "  %s %s\n", ui.dim("container:"), strings.Join(meta, " · "))

	if info.Format.Duration > 0 {
		fmt.Fprintf(os.Stderr, "  %s %s\n", ui.dim("duration:"), humanDuration(info.Format.Duration))
	}

	for _, s := range info.Streams {
		fmt.Fprintf(os.Stderr, "  %s %s\n", ui.dim("stream"), formatStream(s))
	}

	for _, w := range info.Warnings {
		fmt.Fprintf(os.Stderr, "  %s %s\n", ui.yellow("!"), ui.yellow(w))
	}
}

// formatStream turns a streamInfo into a one-line human summary.
func formatStream(s streamInfo) string {
	parts := []string{ui.cyan(s.Type + ":"), ui.bold(s.Codec)}
	if s.Width > 0 && s.Height > 0 {
		parts = append(parts, fmt.Sprintf("%d×%d", s.Width, s.Height))
	}
	if s.FPS > 0 {
		parts = append(parts, fmt.Sprintf("%.2ffps", s.FPS))
	}
	if s.SampleRate > 0 {
		parts = append(parts, fmt.Sprintf("%dHz", s.SampleRate))
	}
	if s.Channels > 0 {
		parts = append(parts, channelName(s.Channels))
	}
	if s.Frames > 0 {
		parts = append(parts, fmt.Sprintf("%d frames", s.Frames))
	}
	if s.Duration > 0 {
		parts = append(parts, humanDuration(s.Duration))
	}
	return strings.Join(parts, "  ")
}

func channelName(n int) string {
	switch n {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	default:
		return fmt.Sprintf("%dch", n)
	}
}

// humanBitRate formats bits per second as kbps or Mbps.
func humanBitRate(bps int64) string {
	if bps < 1000 {
		return fmt.Sprintf("%dbps", bps)
	}
	if bps < 1000*1000 {
		return fmt.Sprintf("%.0fkbps", float64(bps)/1000)
	}
	return fmt.Sprintf("%.2fMbps", float64(bps)/1000/1000)
}
