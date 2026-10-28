package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	Name      string  `json:"name"`
	AllNames  string  `json:"all_names"`
	Extension string  `json:"extension"`
	MatchOK   bool    `json:"extension_matches_container"`
	Duration  float64 `json:"duration"`
	BitRate   int64   `json:"bit_rate,omitempty"`
	Size      int64   `json:"size"`
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
func (a *application) probeInfo(path string) (fileInfo, error) {
	cmd := a.command("ffprobe", "-v", "error",
		"-show_entries", "format=format_name,duration,bit_rate,size:stream=index,codec_type,codec_name,width,height,avg_frame_rate,nb_frames,duration,sample_rate,channels",
		"-of", "json", path)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fileInfo{}, fmt.Errorf("ffprobe failed: %s", strings.TrimSpace(stderr.String()))
	}
	return a.parseProbeInfo(path, out)
}

// parseProbeInfo turns ffprobe JSON into a fileInfo. It is split out so
// tests can feed it fixed probe output without spawning ffmpeg.
func (a *application) parseProbeInfo(path string, data []byte) (fileInfo, error) {
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
	names := strings.Split(probe.Format.Name, ",")
	ext := strings.ToLower(filepath.Ext(path))
	if ext != "" && ext[0] == '.' {
		ext = ext[1:]
	}
	info.Format.Extension = ext
	info.Format.AllNames = probe.Format.Name
	info.Format.Name = names[0]
	for _, n := range names {
		if n == ext {
			info.Format.MatchOK = true
			break
		}
	}
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

	// Flag when the file extension does not match any of the container
	// formats ffprobe reports. That can mean a mis-named file that some
	// players will refuse to open.
	if ext != "" && !info.Format.MatchOK {
		info.Warnings = append(info.Warnings, fmt.Sprintf(
			"file extension .%s does not match the container format (%s)",
			ext, info.Format.AllNames))
	}

	// Flag stream/container duration disagreements, which can confuse the
	// progress bar and make Finder-style panels report the wrong length.
	for _, s := range info.Streams {
		if s.Duration > 0 && info.Format.Duration > 0 {
			if diff := abs(s.Duration-info.Format.Duration) / info.Format.Duration; diff > durationMismatchThreshold {
				info.Warnings = append(info.Warnings, fmt.Sprintf(
					"%s stream duration (%s) differs from container duration (%s)",
					s.Type, a.humanDuration(s.Duration), a.humanDuration(info.Format.Duration)))
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

// runInfo reports the installation and recorded stats, or inspects media files.
func (a *application) runInfo(paths []string) {
	if len(paths) == 0 {
		a.runOverview()
		return
	}

	a.requireDeps()

	var infos []fileInfo
	for _, path := range paths {
		info, err := a.probeInfo(path)
		if err != nil {
			if a.machine {
				a.exitMachineError(machineError{Error: err.Error(), Input: path})
			}
			fmt.Fprintf(a.stderr, "%s %s\n", a.errMark(), a.ui.red(fmt.Sprintf("%s: %v", path, err)))
			exitCLI(1)
		}
		infos = append(infos, info)
	}

	if a.machine {
		a.printJSON(infos)
		return
	}

	for i, info := range infos {
		if i > 0 {
			fmt.Fprintln(a.stderr)
		}
		a.printFileInfo(info)
	}
}

type installationInfo struct {
	Version         string     `json:"version"`
	Platform        string     `json:"platform"`
	BinarySizeBytes int64      `json:"binary_size_bytes"`
	StatsScope      string     `json:"stats_scope"`
	Stats           queueStats `json:"stats"`
}

func (a *application) runOverview() {
	self, err := os.Executable()
	if err != nil {
		a.exitError("could not locate the running shrink: %v", err)
	}
	binary, err := os.Stat(self)
	if err != nil {
		a.exitError("could not read shrink binary size: %v", err)
	}
	store, err := a.queueStore()
	if err != nil {
		a.exitError("could not load compression stats: %v", err)
	}
	state, err := store.snapshot(a.ctx)
	if err != nil {
		a.exitError("could not load compression stats: %v", err)
	}
	info := installationInfo{Version: version, Platform: runtime.GOOS + "/" + runtime.GOARCH, BinarySizeBytes: binary.Size(), StatsScope: "foreground_and_queue", Stats: state.Stats}
	if a.machine {
		a.printJSON(info)
		return
	}
	fmt.Fprintf(a.stderr, "%s\n", a.ui.bold("shrink "+info.Version))
	fmt.Fprintf(a.stderr, "  Binary          %s\n  Platform        %s\n", a.humanSize(info.BinarySizeBytes), info.Platform)
	fmt.Fprintf(a.stderr, "\n%s\n", a.ui.bold("Compression stats (foreground + queue)"))
	stats := info.Stats
	if stats.Files == 0 {
		fmt.Fprintln(a.stderr, "  No completed encodes recorded yet.")
	}
	fmt.Fprintf(a.stderr, "  Completed       %d files (%d same-kind, %d derived)\n", stats.Files, stats.CompressedFiles, stats.DerivedFiles)
	fmt.Fprintf(a.stderr, "  Total size      %s -> %s\n", a.humanSize(stats.InputBytes), a.humanSize(stats.OutputBytes))
	fmt.Fprintf(a.stderr, "  Net reduction   %s", a.humanSize(stats.SavedBytes))
	if stats.CompressionInputBytes > 0 {
		fmt.Fprintf(a.stderr, "  %s", a.savingsText(stats.CompressionInputBytes, stats.CompressionOutputBytes))
	}
	fmt.Fprintf(a.stderr, "\n  Processing      %s\n  Skipped         %d\n  Failed attempts %d\n", a.humanDuration(float64(stats.ProcessingMS)/1000), stats.Skipped, stats.FailedAttempts)
	if len(stats.Types) > 0 {
		fmt.Fprintf(a.stderr, "\n  %s\n", a.ui.dim("File types"))
		for _, kind := range sortedKeys(stats.Types) {
			fmt.Fprintf(a.stderr, "    %-8s %d files\n", strings.TrimPrefix(kind, "."), stats.Types[kind].Files)
		}
	}
	fmt.Fprintf(a.stderr, "\n  %s\n", a.ui.dim("Reduction excludes derived outputs; originals may still be retained."))
}

// printFileInfo renders one fileInfo in the tool's compact style.
func (a *application) printFileInfo(info fileInfo) {
	fmt.Fprintf(a.stderr, "%s\n", a.ui.bold(filepath.Base(info.Path)))

	ext := info.Format.Extension
	if ext == "" {
		ext = "none"
	}
	fmt.Fprintf(a.stderr, "  %s .%s", a.ui.dim("extension:"), ext)
	if info.Format.AllNames != "" {
		fmt.Fprintf(a.stderr, "  %s %s", a.ui.dim("container:"), info.Format.AllNames)
	}
	if !info.Format.MatchOK && info.Format.Extension != "" {
		fmt.Fprintf(a.stderr, "  %s", a.ui.yellow("(mismatch)"))
	}
	fmt.Fprintln(a.stderr)

	meta := []string{info.Kind}
	if info.Format.Size > 0 {
		meta = append(meta, a.humanSize(info.Format.Size))
	}
	if info.Format.BitRate > 0 {
		meta = append(meta, humanBitRate(info.Format.BitRate))
	}
	fmt.Fprintf(a.stderr, "  %s %s\n", a.ui.dim("type:"), strings.Join(meta, " · "))

	if info.Format.Duration > 0 {
		fmt.Fprintf(a.stderr, "  %s %s\n", a.ui.dim("duration:"), a.humanDuration(info.Format.Duration))
	}

	for _, s := range info.Streams {
		fmt.Fprintf(a.stderr, "  %s %s\n", a.ui.dim("stream"), a.formatStream(s))
	}

	for _, w := range info.Warnings {
		fmt.Fprintf(a.stderr, "  %s %s\n", a.ui.yellow("!"), a.ui.yellow(w))
	}
}

// formatStream turns a streamInfo into a one-line human summary.
func (a *application) formatStream(s streamInfo) string {
	parts := []string{a.ui.cyan(s.Type + ":"), a.ui.bold(s.Codec)}
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
		parts = append(parts, a.humanDuration(s.Duration))
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
