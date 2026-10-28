package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// jobPlan is the single resolved description consumed by previews and execution.
// Building a plan never reserves a name or writes a file.
type jobPlan struct {
	app                                           *application
	input, output, kind, format, operation, label string
	opts                                          options
	info                                          mediaInfo
	args                                          []string
	duration                                      float64
	noop                                          bool
}

func (a *application) describeJob(input, kind string, o options) jobPlan {
	p := jobPlan{app: a, input: input, kind: kind, opts: o, operation: "compress", label: "converting"}
	tag := ""
	switch kind {
	case "video":
		p.format = coalesce(o.videoFmt, "mp4")
		if o.audioFmt != "" {
			p.kind, p.format, p.operation, tag = "audio", o.audioFmt, "extract_audio", "audio"
			o.resolution = ""
		}
		if o.imageFmt != "" {
			p.kind, p.format, p.operation, tag = "image", o.imageFmt, "grab_frame", "frame"
		}
	case "image":
		p.format = coalesce(o.imageFmt, "jpeg")
	case "audio":
		p.format = coalesce(o.audioFmt, "mp3")
		o.resolution = ""
	}
	// Only explicit levels appear in names. The implicit default is a quality
	// decision, not an operation tag, so bare runs consistently use -compressed.
	p.output = filepath.Join(filepath.Dir(input), candidateBase(input, tag, o.resolution, o.compress, o.speed)+"."+outExt(p.kind, p.format))
	return p
}

func (a *application) planJob(input string, o options) (jobPlan, error) {
	kind := detectKind(input)
	p := a.describeJob(input, kind, o)
	if kind == "unknown" {
		return p, fmt.Errorf("unsupported file type: %s", filepath.Ext(input))
	}
	if err := checkKindTargets(kind, o); err != nil {
		return p, err
	}
	factor := 1.0
	if o.speed != "" {
		var ok bool
		factor, _, ok = parseSpeed(o.speed)
		if !ok || factor == 1 {
			return p, fmt.Errorf("bad speed factor: %s", o.speed)
		}
	}
	done := a.trackStep(input, "inspect input")
	p.info = a.probeFile(input)
	done()
	if !p.info.ok {
		return p, fmt.Errorf("%s does not look like a valid %s file", input, kind)
	}
	if (kind == "video" || kind == "image") && !p.info.hasVideo {
		return p, fmt.Errorf("%s has no video or image stream", input)
	}
	if p.kind == "audio" && !p.info.hasAudio {
		return p, fmt.Errorf("%s has no audio track", input)
	}
	// Animation is deliberately rejected until the encoder path supports it.
	// Silently flattening an image must never qualify as safe compression.
	if kind == "image" && p.info.frames > 1 {
		return p, fmt.Errorf("%s is animated; animated image compression is not supported (original kept)", input)
	}
	if !a.overwrite && looksConverted(input) && o.compress == "" && o.speed == "" {
		if p.matches(input, p.info) {
			p.noop = true
			return p, nil
		}
		sameFormat := p
		sameFormat.opts.resolution = ""
		if o.resolution != "" && p.operation == "compress" && sameFormat.matches(input, p.info) {
			p.operation = "rescale"
		}
	}
	grade := o.compress
	if grade == "" && o.plainRun() {
		grade = "compress"
	}
	levels := transparentLevel
	if grade != "" {
		levels = compressionLevels[grade]
		p.label = "compressing"
	}
	p.duration = p.info.duration / factor
	if math.IsInf(p.duration, 0) || math.IsNaN(p.duration) {
		return p, fmt.Errorf("speed factor produces an unrepresentable duration")
	}
	p.args = []string{"-i", input}
	switch p.kind {
	case "video":
		vf := videoFormats[p.format]
		codec, encArgs := vf.codec, append([]string(nil), vf.args...)
		crf := videoCRF(codec, levels)
		if a.fast {
			if hw, hwArgs, ok := a.hwEncodeFor(codec, coalesce(grade, "transparent"), p.info); ok {
				codec, encArgs, crf = hw, hwArgs, ""
				if p.format == "mp4" || p.format == "mov" {
					encArgs = append(encArgs, "-movflags", "+faststart")
				}
				if p.format == "hevc" {
					encArgs = append(encArgs, "-tag:v", "hvc1")
				}
			}
		}
		p.args = append(p.args, "-map", "0:v:0", "-map", "0:a?", "-c:v", codec)
		if crf != "" {
			p.args = append(p.args, "-crf", crf)
		}
		p.args = append(p.args, encArgs...)
		audioCodec := vf.audioCodec
		if grade == "" && o.speed == "" && p.format != "webm" && p.info.audioCodec == "aac" && p.info.streams["audio"] == 1 {
			p.args = append(p.args, "-c:a", "copy")
		} else {
			p.args = append(p.args, "-c:a", audioCodec, "-b:a", levels.audioBitrate)
		}
	case "image":
		img := imageFormats[p.format]
		p.args = append(p.args, "-map", "0:v:0")
		p.args = append(p.args, imageCodecArgs(img.codec, levels.imageQuality, levels.webpQuality)...)
		p.args = append(p.args, "-frames:v", "1")
		// image2 otherwise interprets percent characters in literal filenames.
		if p.format != "gif" && p.format != "webp" {
			p.args = append(p.args, "-update", "1")
		}
		p.duration = 0
	case "audio":
		af := audioFormats[p.format]
		p.args = append(p.args, "-map", "0:a:0", "-vn", "-c:a", af.codec)
		p.args = append(p.args, audioEncodeArgs(af.codec, levels)...)
	}
	if p.kind != "audio" {
		var filters []string
		if o.resolution != "" {
			filters = append(filters, resolutions[o.resolution])
			p.label += " + scaling"
		}
		if o.speed != "" {
			filters = append(filters, setptsFilter(o.speed))
		}
		if len(filters) > 0 {
			p.args = append(p.args, "-vf", strings.Join(filters, ","))
		}
	}
	if o.speed != "" && p.kind != "image" && p.info.hasAudio {
		p.args = append(p.args, "-af", atempoChain(factor))
		p.label += " + speed"
	}
	if p.operation == "rescale" {
		p.label = "downscaling"
	}
	if p.operation == "extract_audio" {
		p.label = "extracting audio"
	}
	if p.operation == "grab_frame" {
		p.label = "grabbing frame"
	}
	return p, nil
}

func (p *jobPlan) reserve() {
	stem := strings.TrimSuffix(filepath.Base(p.output), filepath.Ext(p.output))
	p.output = p.app.claimOutput(filepath.Dir(p.output), stem, strings.TrimPrefix(filepath.Ext(p.output), "."))
}

func (p jobPlan) execute() (report *fileReport, err error) {
	defer func() { p.app.recordForegroundOutcome(p.input, report, err) }()
	if p.noop {
		return p.app.noopReport(p.input), nil
	}
	took, err := p.app.encodeJob(p.input, p.output, p.label, p.duration, p.args)
	if err != nil {
		return nil, err
	}
	return p.app.finishJob(p.input, p.kind, p.output, took, p.operation), nil
}

// matches requires the actual codec and container as well as the requested size.
func (p jobPlan) matches(path string, info mediaInfo) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if ext == "jpeg" {
		ext = "jpg"
	}
	if ext != outExt(p.kind, p.format) || !info.ok || !containerMatches(p.kind, p.format, info.formatName) {
		return false
	}
	if p.kind != "audio" && p.opts.resolution != "" && info.height > resHeight(p.opts.resolution) {
		return false
	}
	switch p.kind {
	case "video":
		return info.videoCodec == targetVideoCodec(p.format) && (!p.info.hasAudio || info.hasAudio)
	case "image":
		return info.frames <= 1 && info.videoCodec == probeCodec(imageFormats[p.format].codec)
	case "audio":
		return info.audioCodec == probeCodec(audioFormats[p.format].codec)
	}
	return false
}

func probeCodec(encoder string) string {
	if encoder == "h264_videotoolbox" {
		return "h264"
	}
	if encoder == "hevc_videotoolbox" {
		return "hevc"
	}
	for _, kind := range mediaKinds {
		for _, f := range formatsForKind(kind) {
			if f.codec == encoder {
				return f.probe
			}
		}
	}
	return encoder
}

func containerMatches(kind, format, names string) bool {
	want := formatsForKind(kind)[format].container
	for _, name := range strings.Split(names, ",") {
		if name == want {
			return true
		}
		if kind == "image" && (name == "image2" || name == "image2pipe") {
			return true
		}
	}
	return false
}

func (a *application) replacementSafe(input, output string) bool {
	before, after := a.probeFile(input), a.probeFile(output)
	if !before.ok || !after.ok || before.frames > 1 {
		return false
	}
	for kind, count := range before.streams {
		if after.streams[kind] < count {
			return false
		}
	}
	// Catch truncated audio/video outputs. Speed changes are intentional, so
	// duration equality is checked by execution against its resolved timeline.
	return true
}

// existingResult checks validity before batch mode calls an output completed.
func (p jobPlan) existingResult() bool {
	if !fileExists(p.output) {
		return false
	}
	info := p.app.probeFile(p.output)
	if !p.matches(p.output, info) {
		return false
	}
	if p.duration > 0 && math.Abs(info.duration-p.duration) > math.Max(0.5, p.duration*0.05) {
		return false
	}
	source, err := os.Stat(p.input)
	if err != nil {
		return false
	}
	output, err := os.Stat(p.output)
	return err == nil && !output.ModTime().Before(source.ModTime())
}
