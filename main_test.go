package main

import "testing"

func TestKindForExt(t *testing.T) {
	cases := []struct{ ext, want string }{
		{".mp4", "video"}, {".MOV", "video"}, {".Mkv", "video"}, {".ts", "video"},
		{".png", "image"}, {".JPEG", "image"}, {".heic", "image"}, {".avif", "image"},
		{".mp3", "audio"}, {".WMA", "audio"}, {".m4a", "audio"},
		{".txt", ""}, {"", ""}, {"png", ""}, {"mp4", ""},
	}
	for _, c := range cases {
		if got := kindForExt(c.ext); got != c.want {
			t.Errorf("kindForExt(%q) = %q, want %q", c.ext, got, c.want)
		}
	}
}

func TestDetectKind(t *testing.T) {
	cases := []struct{ path, want string }{
		{"a/vid.mp4", "video"},
		{"pics/IMG_1234.HEIC", "image"},
		{"song.Mp3", "audio"},
		{"notes.txt", "unknown"},
		{"noext", "unknown"},
	}
	for _, c := range cases {
		if got := detectKind(c.path); got != c.want {
			t.Errorf("detectKind(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestOutputFormats(t *testing.T) {
	video := outputFormats("video")
	for _, want := range []string{"mp4", "mkv", "webm", "mov", "avi", "hevc", "av1"} {
		found := false
		for _, f := range video {
			if f == want {
				found = true
			}
		}
		if !found {
			t.Errorf("outputFormats(video) missing %q", want)
		}
	}
	for _, alias := range []string{"jpg", "aac"} {
		for _, f := range video {
			if f == alias {
				t.Errorf("outputFormats(video) should drop alias %q", alias)
			}
		}
	}
	image := outputFormats("image")
	for _, f := range image {
		if f == "jpg" {
			t.Errorf("outputFormats(image) should drop the jpg alias")
		}
	}
}

func TestCoalesce(t *testing.T) {
	if coalesce("", "mp4") != "mp4" {
		t.Error("coalesce empty should return the fallback")
	}
	if coalesce("webm", "mp4") != "webm" {
		t.Error("coalesce non-empty should return the first value")
	}
}

func TestVideoCRF(t *testing.T) {
	lv := compressionLevels["compress"]
	cases := []struct{ codec, want string }{
		{"libx264", lv.videoCRF},
		{"libx265", lv.hevcCRF},
		{"libsvtav1", lv.av1CRF},
		{"libvpx-vp9", lv.videoCRF}, // unknown codec falls back to x264's CRF
	}
	for _, c := range cases {
		if got := videoCRF(c.codec, lv); got != c.want {
			t.Errorf("videoCRF(%q) = %q, want %q", c.codec, got, c.want)
		}
	}
}

func TestResHeight(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		{"340p", 340},
		{"480p", 480},
		{"720p", 720},
		{"1080p", 1080},
		{"1440p", 1440},
		{"4k", 2160},
		{"bogus", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := resHeight(c.name); got != c.want {
			t.Errorf("resHeight(%q) = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestTargetVideoCodec(t *testing.T) {
	cases := []struct {
		videoFmt string
		want     string
	}{
		{"mp4", "h264"},
		{"mkv", "h264"},
		{"mov", "h264"},
		{"avi", "h264"},
		{"hevc", "hevc"},
		{"av1", "av1"},
		{"webm", "vp9"},
	}
	for _, c := range cases {
		if got := targetVideoCodec(c.videoFmt); got != c.want {
			t.Errorf("targetVideoCodec(%q) = %q, want %q", c.videoFmt, got, c.want)
		}
	}
}

func TestParseSpeed(t *testing.T) {
	cases := []struct {
		arg     string
		factor  float64
		tag     string
		ok      bool
		isSpeed bool // does isSpeedFactor accept it
	}{
		{"x5", 5, "x5", true, true},
		{"x0.1", 0.1, "x0.1", true, true},
		{"x2.5", 2.5, "x2.5", true, true},
		{"X5", 5, "x5", true, true},
		{"x0.01", 0.01, "x0.01", true, true},
		{"x1", 1, "x1", true, false}, // valid shape, but no speed change
		{"5", 0, "", false, false},
		{"x", 0, "", false, false},
		{"x0", 0, "", false, false},
		{"x-2", 0, "", false, false},
		{"x.5", 0.5, "x.5", true, true},
		{"xabc", 0, "", false, false},
		{"speed", 0, "", false, false},
	}
	for _, c := range cases {
		f, tag, ok := parseSpeed(c.arg)
		if ok != c.ok || (ok && (f != c.factor || tag != c.tag)) {
			t.Errorf("parseSpeed(%q) = (%v, %q, %v), want (%v, %q, %v)", c.arg, f, tag, ok, c.factor, c.tag, c.ok)
		}
		if got := isSpeedFactor(c.arg); got != c.isSpeed {
			t.Errorf("isSpeedFactor(%q) = %v, want %v", c.arg, got, c.isSpeed)
		}
	}
}

func TestSetptsFilter(t *testing.T) {
	if got, want := setptsFilter("x5"), "setpts=PTS/5"; got != want {
		t.Errorf("setptsFilter(x5) = %q, want %q", got, want)
	}
	if got, want := setptsFilter("x0.1"), "setpts=PTS/0.1"; got != want {
		t.Errorf("setptsFilter(x0.1) = %q, want %q", got, want)
	}
}

func TestAtempoChain(t *testing.T) {
	cases := []struct {
		speed float64
		want  string
	}{
		{1, "atempo=1"},
		{5, "atempo=5"},
		{0.5, "atempo=0.5"},
		{0.1, "atempo=0.5,atempo=0.5,atempo=0.5,atempo=0.8"},
		{0.25, "atempo=0.5,atempo=0.5"},
		{2.5, "atempo=2.5"},
		{125, "atempo=100,atempo=1.25"},
	}
	for _, c := range cases {
		if got := atempoChain(c.speed); got != c.want {
			t.Errorf("atempoChain(%v) = %q, want %q", c.speed, got, c.want)
		}
	}
}

func TestPlainRun(t *testing.T) {
	bare := options{}
	if !bare.plainRun() {
		t.Error("a run with no targets is a plain run")
	}
	level := options{compress: "spacesaver"}
	if !level.plainRun() {
		t.Error("an explicit level alone is still a bare-compression run shape")
	}
	res := options{resolution: "720p"}
	if res.plainRun() {
		t.Error("a resolution target is not a plain run")
	}
	spd := options{speed: "x5"}
	if spd.plainRun() {
		t.Error("a speed target is not a plain run")
	}
	for name, fmtOpt := range map[string]options{
		"video": {videoFmt: "mp4"}, "image": {imageFmt: "png"}, "audio": {audioFmt: "mp3"},
	} {
		if fmtOpt.plainRun() {
			t.Errorf("a %s target is not a plain run", name)
		}
	}
}

func TestTransparentLevel(t *testing.T) {
	// each codec family has its own near-lossless CRF
	if got := videoCRF("libx264", transparentLevel); got != "18" {
		t.Errorf("videoCRF(libx264, transparent) = %q, want 18", got)
	}
	if got := videoCRF("libx265", transparentLevel); got != "22" {
		t.Errorf("videoCRF(libx265, transparent) = %q, want 22", got)
	}
	if got := videoCRF("libvpx-vp9", transparentLevel); got != "28" {
		t.Errorf("videoCRF(libvpx-vp9, transparent) = %q, want 28", got)
	}
	if got := videoCRF("libvpx-vp9", compressionLevels["compress"]); got != "23" {
		t.Errorf("preset levels keep the vp9 fallback: got %q, want 23", got)
	}
	if got := audioEncodeArgs("libmp3lame", transparentLevel); got[1] != "256k" {
		t.Errorf("transparent audio bitrate = %q, want 256k", got[1])
	}
}
