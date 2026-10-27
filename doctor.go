package main

import (
	"fmt"
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
func encoderTargets() []struct{ encoder, targets string } {
	groups := map[string][]string{}
	for _, kind := range mediaKinds {
		for _, name := range outputFormats(kind) {
			f := formatsForKind(kind)[name]
			groups[f.codec] = append(groups[f.codec], name)
			if f.audioCodec != "" {
				groups[f.audioCodec] = append(groups[f.audioCodec], name+" audio")
			}
		}
	}
	var result []struct{ encoder, targets string }
	for _, encoder := range sortedKeys(groups) {
		result = append(result, struct{ encoder, targets string }{encoder, strings.Join(groups[encoder], ", ")})
	}
	return result
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
func (a *application) toolVersion(bin string) doctorTool {
	t := doctorTool{}
	if _, err := exec.LookPath(bin); err != nil {
		t.Note = "not found on PATH"
		return t
	}
	t.Available = true
	if out, err := a.command(bin, "-version").Output(); err == nil {
		if i := strings.IndexByte(string(out), '\n'); i > 0 {
			t.Version = strings.TrimSpace(string(out[:i]))
		}
	}
	return t
}

// requireDeps exits with one clear message when ffmpeg or ffprobe are
// missing: every operation needs both, and failing later would only
// surface as a confusing per-file error.
func (a *application) requireDeps() {
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
	if a.machine {
		a.exitMachineError(machineError{
			Error:       "shrink needs " + what + " on PATH - not found",
			Suggestions: []string{"shrink doctor lists what is installed and what is missing"},
		})
	}
	fmt.Fprintf(a.stderr, "%s %s\n", a.errMark(), a.ui.red("shrink needs "+what+" on PATH - not found"))
	fmt.Fprintf(a.stderr, "  %s\n", a.ui.dim("install: "+ffmpegInstallHint()))
	fmt.Fprintf(a.stderr, "  %s\n", a.ui.dim("shrink doctor lists what is installed and what is missing"))
	exitCLI(1)
}

// doctorMark renders a checklist entry: glyphs when styled, plain words
// when piped or color-disabled.
func (a *application) doctorMark(ok bool) string {
	if ok {
		if a.ui.on {
			return a.ui.green("✓")
		}
		return "ok:"
	}
	if a.ui.on {
		return a.ui.red("✗")
	}
	return "missing:"
}

// runDoctor prints a checklist of everything shrink depends on: the
// core tools, every encoder behind a target, and hardware (-f) support.
// Exits 0 when the core tools are present (missing optional encoders
// are reported, not fatal), 1 when they are not.
func (a *application) runDoctor() {
	report := doctorReport{
		Version:  version,
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		FFmpeg:   a.toolVersion("ffmpeg"),
		FFprobe:  a.toolVersion("ffprobe"),
	}
	report.OK = report.FFmpeg.Available && report.FFprobe.Available
	if !report.FFmpeg.Available {
		report.FFmpeg.Note = ffmpegInstallHint()
	}

	if report.FFmpeg.Available {
		avail := a.ffmpegEncoders()
		report.Encoders = map[string]bool{}
		for _, e := range encoderTargets() {
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

	if a.machine {
		a.printJSON(report)
		if !report.OK {
			exitCLI(1)
		}
		exitCLI(0)
	}

	fmt.Fprintf(a.stderr, "shrink %s doctor - %s\n\n", version, report.Platform)
	fmt.Fprintf(a.stderr, "  %-8s %-14s %s\n", a.doctorMark(report.FFmpeg.Available), "ffmpeg", report.FFmpeg.Version+report.FFmpeg.Note)
	fmt.Fprintf(a.stderr, "  %-8s %-14s %s\n", a.doctorMark(report.FFprobe.Available), "ffprobe", report.FFprobe.Version+report.FFprobe.Note)
	if report.FFmpeg.Available {
		fmt.Fprintf(a.stderr, "\n")
		for _, e := range encoderTargets() {
			line := e.targets
			if !report.Encoders[e.encoder] {
				line = a.ui.red(e.targets + " unavailable")
			}
			fmt.Fprintf(a.stderr, "  %-8s %-14s %s\n", a.doctorMark(report.Encoders[e.encoder]), e.encoder, line)
		}
		fmt.Fprintf(a.stderr, "\n")
		fmt.Fprintf(a.stderr, "  %-8s %-14s %s\n", a.doctorMark(report.Hardware.Available), "hardware (-f)", report.Hardware.Note)
	}
	fmt.Fprintf(a.stderr, "\n")

	if !report.OK {
		fmt.Fprintf(a.stderr, "%s shrink is not ready - install ffmpeg (it ships ffprobe)\n", a.errMark())
		fmt.Fprintf(a.stderr, "  %s\n", a.ui.dim("install: "+ffmpegInstallHint()))
		exitCLI(1)
	}
	if len(report.Missing) > 0 {
		fmt.Fprintf(a.stderr, "%s %d target groups unavailable: %s\n",
			a.ui.yellow("!"), len(report.Missing), a.ui.yellow(strings.Join(report.Missing, "; ")))
	}
	fmt.Fprintf(a.stderr, "%s shrink is ready\n", a.doneMark())
	exitCLI(0)
}
