package main

import "testing"

func TestParseMakeDuration(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"10s", 10, true},
		{"2m", 120, true},
		{"90s", 90, true},
		{"1.5s", 1.5, true},
		{"10", 0, false},
		{"s", 0, false},
		{"", 0, false},
		{"0s", 0, false},
		{"-5s", 0, false},
		{"10x", 0, false},
	}
	for _, c := range cases {
		got, ok := parseMakeDuration(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseMakeDuration(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestParseMakeHz(t *testing.T) {
	if hz, ok := parseMakeHz("440hz"); !ok || hz != 440 {
		t.Errorf("parseMakeHz(440hz) = %v, %v", hz, ok)
	}
	if hz, ok := parseMakeHz("880hz"); !ok || hz != 880 {
		t.Errorf("parseMakeHz(880hz) = %v, %v", hz, ok)
	}
	for _, bad := range []string{"440", "0hz", "99999hz", "hz", "", "19hz"} {
		if _, ok := parseMakeHz(bad); ok {
			t.Errorf("parseMakeHz(%q) should fail", bad)
		}
	}
}

func TestMakeSize(t *testing.T) {
	cases := []struct {
		res  string
		w, h int
	}{
		{"1080p", 1920, 1080},
		{"720p", 1280, 720},
		{"4k", 3840, 2160},
		{"340p", 604, 340}, // 604.4 rounds down to even
		{"", 1920, 1080},   // default
	}
	for _, c := range cases {
		w, h := makeSize(c.res)
		if w != c.w || h != c.h {
			t.Errorf("makeSize(%q) = %dx%d, want %dx%d", c.res, w, h, c.w, c.h)
		}
	}
}

func TestMakeName(t *testing.T) {
	cases := []struct {
		spec makeSpec
		want string
	}{
		// defaults fill in per kind
		{makeSpec{kind: "video", format: "mp4", content: "red", res: "1080p", fps: "60fps", dur: "10s"}, "red-1080p-60fps-10s"},
		{makeSpec{kind: "image", format: "jpeg", content: "teal", res: "720p"}, "teal-720p"},
		{makeSpec{kind: "audio", format: "wav", content: "noise", hz: "440hz", dur: "5s"}, "noise-440hz-5s"},
		// an explicit level tags the name
		{makeSpec{kind: "video", format: "mp4", content: "blue", res: "1080p", fps: "30fps", dur: "2m", level: "neversee"}, "blue-1080p-30fps-2m-neversee"},
	}
	for _, c := range cases {
		if got := c.spec.resolve().name(); got != c.want {
			t.Errorf("name() = %q, want %q", got, c.want)
		}
	}
}

func TestParseMakeTargets(t *testing.T) {
	// the format picks the kind
	spec, err := parseMakeTargets([]string{"mp4", "red", "1080p", "60fps", "30s"})
	if err != nil || spec.kind != "video" || spec.format != "mp4" || spec.content != "red" || spec.res != "1080p" || spec.fps != "60fps" || spec.dur != "30s" {
		t.Errorf("parse video = %+v, %v", spec, err)
	}
	spec, err = parseMakeTargets([]string{"wav", "noise", "5s"})
	if err != nil || spec.kind != "audio" || spec.content != "noise" || spec.dur != "5s" {
		t.Errorf("parse audio = %+v, %v", spec, err)
	}
	spec, err = parseMakeTargets([]string{"jpeg"})
	if err != nil || spec.kind != "image" || spec.format != "jpeg" {
		t.Errorf("parse image = %+v, %v", spec, err)
	}
	// no format: video mp4
	spec, err = parseMakeTargets([]string{"red"})
	if err != nil || spec.kind != "video" || spec.format != "mp4" {
		t.Errorf("parse default = %+v, %v", spec, err)
	}

	// rejected: two formats, two contents, unknown, kind mismatches
	for _, args := range [][]string{
		{"mp4", "wav"},      // one format only
		{"red", "blue"},     // one content only
		{"gold", "xyzzy"},   // unknown target
		{"wav", "1080p"},    // resolution for audio
		{"wav", "60fps"},    // fps for audio
		{"wav", "red"},      // color content for audio
		{"jpeg", "60fps"},   // fps for image
		{"jpeg", "10s"},     // duration for image
		{"mp4", "sine"},     // sine is audio content
		{"mp4", "9999999s"}, // duration shape ok but absurd... parse ok shape? "9999999s" valid shape
	} {
		if _, err := parseMakeTargets(args); err == nil {
			t.Errorf("parseMakeTargets(%v) should fail", args)
		}
	}
}

func TestMakeColors(t *testing.T) {
	if makeColors["red"] != "0xFF0000" {
		t.Errorf("red = %q", makeColors["red"])
	}
	if makeColors["gray"] != makeColors["grey"] {
		t.Error("gray and grey should map to the same color")
	}
}

func TestMakePictureSource(t *testing.T) {
	src, filters := makePictureSource("red", 1920, 1080, 60)
	if src != "color=0xFF0000:s=1920x1080:r=60" || filters != nil {
		t.Errorf("red source = %q, %v", src, filters)
	}
	src, _ = makePictureSource("bars", 1280, 720, 30)
	if src != "smptebars=s=1280x720:r=30" {
		t.Errorf("bars source = %q", src)
	}
	src, filters = makePictureSource("noise", 1280, 720, 30)
	if src != "nullsrc=s=1280x720:r=30" || len(filters) == 0 {
		t.Errorf("noise source = %q, %v", src, filters)
	}
}

func TestAudioEncodeArgs(t *testing.T) {
	lv := compressionLevels["compress"]
	if got := audioEncodeArgs("pcm_s16le", lv); got != nil {
		t.Errorf("lossless codecs take no level args, got %v", got)
	}
	if got := audioEncodeArgs("libmp3lame", lv); got[0] != "-b:a" || got[1] != lv.audioBitrate {
		t.Errorf("mp3 args = %v", got)
	}
	if got := audioEncodeArgs("libvorbis", lv); got[0] != "-q:a" || got[1] != lv.vorbisQ {
		t.Errorf("vorbis args = %v", got)
	}
}
