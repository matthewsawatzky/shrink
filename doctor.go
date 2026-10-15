package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// doctorTool is one binary shrink depends on.
type doctorTool struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Note      string `json:"note,omitempty"`
}

// doctorHardware reports whether -f's hardware encoding has anything
// to use on this platform.
type doctorHardware struct {
	Available bool   `json:"available"`
	Note      string `json:"note"`
}

// doctorReport is the -m JSON document shrink doctor emits.
type doctorReport struct {
	Version  string          `json:"version"`
	Platform string          `json:"platform"`
	FFmpeg   doctorTool      `json:"ffmpeg"`
	FFprobe  doctorTool      `json:"ffprobe"`
	Encoders map[string]bool `json:"encoders,omitempty"`
	Missing  []string        `json:"missing,omitempty"` // targets with no encoder
	Hardware doctorHardware  `json:"hardware"`
	OK       bool            `json:"ok"`
}

// encoderTargets maps each encoder shrink can use to the targets it
// enables, in stable display order.
var encoderTargets = []struct{ encoder, targets string }{
	{"libx264", "mp4, mkv, mov, avi"},
	{"libx265", "hevc"},
	{"libvpx-vp9", "webm"},
	{"libsvtav1", "av1"},
	{"mjpeg", "jpeg"},
	{"png", "png"},
	{"libwebp", "webp"},
	{"gif", "gif"},
	{"libmp3lame", "mp3"},
	{"aac", "aac, m4a"},
	{"libopus", "opus, webm audio"},
	{"libvorbis", "ogg"},
	{"flac", "flac"},
	{"pcm_s16le", "wav"},
}

// ffmpegInstallHint names the command that installs ffmpeg on this
// machine, mirroring what install.sh offers.
func ffmpegInstallHint() string {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("brew"); err == nil {
			return "brew install ffmpeg"
		}
		return "install Homebrew (https://brew.sh), then: brew install ffmpeg"
	case "linux":
		hints := []struct{ pm, cmd string }{
			{"apt-get", "sudo apt-get install -y ffmpeg"},
			{"dnf", "sudo dnf install -y ffmpeg"},
			{"pacman", "sudo pacman -S --noconfirm ffmpeg"},
			{"apk", "sudo apk add ffmpeg"},
			{"zypper", "sudo zypper install -y ffmpeg"},
		}
		for _, h := range hints {
			if _, err := exec.LookPath(h.pm); err == nil {
				return h.cmd
			}
		}
	case "windows":
		return "winget install Gyan.FFmpeg"
	}
	return "install it from https://ffmpeg.org/download.html"
}

// toolVersion reports whether a dependency binary exists and, when it
// does, its first version line.
func toolVersion(bin string) doctorTool {
	t := doctorTool{}
	if _, err := exec.LookPath(bin); err != nil {
		t.Note = "not found on PATH"
		return t
	}
	t.Available = true
	if out, err := exec.Command(bin, "-version").Output(); err == nil {
		if i := strings.IndexByte(string(out), '\n'); i > 0 {
			t.Version = strings.TrimSpace(string(out[:i]))
		}
	}
	return t
}

// requireDeps exits with one clear message when ffmpeg or ffprobe are
// missing: every operation needs both, and failing later would only
// surface as a confusing per-file error.
func requireDeps() {
	var missing []string
	for _, dep := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(dep); err != nil {
			missing = append(missing, dep)
		}
	}
	if len(missing) == 0 {
		return
	}
	what := strings.Join(missing, " and ")
	if machine {
		exitMachineError(machineError{
			Error:       "shrink needs " + what + " on PATH - not found",
			Suggestions: []string{"shrink doctor lists what is installed and what is missing"},
		})
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", errMark(), ui.red("shrink needs "+what+" on PATH - not found"))
	fmt.Fprintf(os.Stderr, "  %s\n", ui.dim("install: "+ffmpegInstallHint()))
	fmt.Fprintf(os.Stderr, "  %s\n", ui.dim("shrink doctor lists what is installed and what is missing"))
	os.Exit(1)
}

// doctorMark renders a checklist entry: glyphs when styled, plain words
// when piped or color-disabled.
func doctorMark(ok bool) string {
	if ok {
		if ui.on {
			return ui.green("✓")
		}
		return "ok:"
	}
	if ui.on {
		return ui.red("✗")
	}
	return "missing:"
}

// runDoctor prints a checklist of everything shrink depends on: the
// core tools, every encoder behind a target, and hardware (-f) support.
// Exits 0 when the core tools are present (missing optional encoders
// are reported, not fatal), 1 when they are not.
func runDoctor() {
	report := doctorReport{
		Version:  version,
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		FFmpeg:   toolVersion("ffmpeg"),
		FFprobe:  toolVersion("ffprobe"),
	}
	report.OK = report.FFmpeg.Available && report.FFprobe.Available
	if !report.FFmpeg.Available {
		report.FFmpeg.Note = ffmpegInstallHint()
	}

	if report.FFmpeg.Available {
		avail := ffmpegEncoders()
		report.Encoders = map[string]bool{}
		for _, e := range encoderTargets {
			report.Encoders[e.encoder] = avail[e.encoder]
			if !avail[e.encoder] {
				report.Missing = append(report.Missing, e.targets)
			}
		}
		switch {
		case runtime.GOOS == "darwin" && avail["h264_videotoolbox"]:
			report.Hardware = doctorHardware{Available: true, Note: "VideoToolbox - -f encodes on the GPU"}
		case runtime.GOOS == "darwin":
			report.Hardware = doctorHardware{Note: "VideoToolbox not in this ffmpeg build - -f falls back to software"}
		default:
			report.Hardware = doctorHardware{Note: "hardware encoding (-f) supports macOS (VideoToolbox) only for now"}
		}
	}

	if machine {
		printJSON(report)
		if !report.OK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	fmt.Fprintf(os.Stderr, "shrink %s doctor - %s\n\n", version, report.Platform)
	fmt.Fprintf(os.Stderr, "  %-8s %-14s %s\n", doctorMark(report.FFmpeg.Available), "ffmpeg", report.FFmpeg.Version+report.FFmpeg.Note)
	fmt.Fprintf(os.Stderr, "  %-8s %-14s %s\n", doctorMark(report.FFprobe.Available), "ffprobe", report.FFprobe.Version+report.FFprobe.Note)
	if report.FFmpeg.Available {
		fmt.Fprintf(os.Stderr, "\n")
		for _, e := range encoderTargets {
			line := e.targets
			if !report.Encoders[e.encoder] {
				line = ui.red(e.targets + " unavailable")
			}
			fmt.Fprintf(os.Stderr, "  %-8s %-14s %s\n", doctorMark(report.Encoders[e.encoder]), e.encoder, line)
		}
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "  %-8s %-14s %s\n", doctorMark(report.Hardware.Available), "hardware (-f)", report.Hardware.Note)
	}
	fmt.Fprintf(os.Stderr, "\n")

	if !report.OK {
		fmt.Fprintf(os.Stderr, "%s shrink is not ready - install ffmpeg (it ships ffprobe)\n", errMark())
		fmt.Fprintf(os.Stderr, "  %s\n", ui.dim("install: "+ffmpegInstallHint()))
		os.Exit(1)
	}
	if len(report.Missing) > 0 {
		fmt.Fprintf(os.Stderr, "%s %d target groups unavailable: %s\n",
			ui.yellow("!"), len(report.Missing), ui.yellow(strings.Join(report.Missing, "; ")))
	}
	fmt.Fprintf(os.Stderr, "%s shrink is ready\n", doneMark())
	os.Exit(0)
}
