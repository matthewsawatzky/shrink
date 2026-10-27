package main

import (
	"strings"
	"testing"
)

func TestParseFrameRate(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"30000/1001", 29.97002997002997},
		{"60/1", 60},
		{"25/1", 25},
		{"0/0", 0},
		{"", 0},
		{"60", 60},
	}
	for _, c := range cases {
		if got := parseFrameRate(c.in); got != c.want {
			t.Errorf("parseFrameRate(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestHumanBitRate(t *testing.T) {
	cases := []struct {
		bps  int64
		want string
	}{
		{500, "500bps"},
		{500_000, "500kbps"},
		{3_300_000, "3.30Mbps"},
		{100_000_000, "100.00Mbps"},
	}
	for _, c := range cases {
		if got := humanBitRate(c.bps); got != c.want {
			t.Errorf("humanBitRate(%d) = %q, want %q", c.bps, got, c.want)
		}
	}
}

func TestChannelName(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, "mono"},
		{2, "stereo"},
		{6, "6ch"},
	}
	for _, c := range cases {
		if got := channelName(c.n); got != c.want {
			t.Errorf("channelName(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestParseProbeInfoVideo(t *testing.T) {
	data := []byte(`{
		"format": {
			"format_name": "mov,mp4,m4a,3gp,3g2,mj2",
			"duration": "74.800000",
			"bit_rate": "3300000",
			"size": "30870000"
		},
		"streams": [
			{
				"index": 0,
				"codec_type": "video",
				"codec_name": "h264",
				"width": 1660,
				"height": 1080,
				"avg_frame_rate": "30000/1001",
				"nb_frames": "2244",
				"duration": "74.800000"
			},
			{
				"index": 1,
				"codec_type": "audio",
				"codec_name": "aac",
				"sample_rate": "48000",
				"channels": 2,
				"duration": "74.800000"
			}
		]
	}`)

	info, err := parseProbeInfo("clip.mp4", data)
	if err != nil {
		t.Fatalf("parseProbeInfo: %v", err)
	}
	if info.Path != "clip.mp4" {
		t.Errorf("Path = %q, want clip.mp4", info.Path)
	}
	if info.Kind != "video" {
		t.Errorf("Kind = %q, want video", info.Kind)
	}
	if info.Format.Name != "mov" {
		t.Errorf("Format.Name = %q, want mov", info.Format.Name)
	}
	if info.Format.Duration != 74.8 {
		t.Errorf("Format.Duration = %v, want 74.8", info.Format.Duration)
	}
	if info.Format.BitRate != 3300000 {
		t.Errorf("Format.BitRate = %v, want 3300000", info.Format.BitRate)
	}
	if info.Format.Size != 30870000 {
		t.Errorf("Format.Size = %v, want 30870000", info.Format.Size)
	}
	if len(info.Streams) != 2 {
		t.Fatalf("len(Streams) = %d, want 2", len(info.Streams))
	}
	v := info.Streams[0]
	if v.Type != "video" || v.Codec != "h264" || v.Width != 1660 || v.Height != 1080 {
		t.Errorf("video stream = %+v", v)
	}
	if v.FPS < 29.9 || v.FPS > 30.0 {
		t.Errorf("video fps = %v, want ~29.97", v.FPS)
	}
	if v.Frames != 2244 {
		t.Errorf("video frames = %v, want 2244", v.Frames)
	}
	a := info.Streams[1]
	if a.Type != "audio" || a.Codec != "aac" || a.SampleRate != 48000 || a.Channels != 2 {
		t.Errorf("audio stream = %+v", a)
	}
	if len(info.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", info.Warnings)
	}
}

func TestParseProbeInfoDurationMismatch(t *testing.T) {
	data := []byte(`{
		"format": {
			"format_name": "mp4",
			"duration": "75.000000",
			"size": "1000"
		},
		"streams": [
			{
				"index": 0,
				"codec_type": "video",
				"codec_name": "h264",
				"width": 1920,
				"height": 1080,
				"avg_frame_rate": "30/1",
				"nb_frames": "108000",
				"duration": "3600.000000"
			}
		]
	}`)

	info, err := parseProbeInfo("weird.mp4", data)
	if err != nil {
		t.Fatalf("parseProbeInfo: %v", err)
	}
	if len(info.Warnings) == 0 {
		t.Fatal("expected duration-mismatch warning, got none")
	}
	if !strings.Contains(info.Warnings[0], "video stream duration") {
		t.Errorf("warning = %q, want mention of video stream duration", info.Warnings[0])
	}
}

func TestParseProbeInfoNoStreams(t *testing.T) {
	data := []byte(`{
		"format": {"format_name": "mp4", "duration": "10.000000", "size": "1000"},
		"streams": []
	}`)

	info, err := parseProbeInfo("empty.mp4", data)
	if err != nil {
		t.Fatalf("parseProbeInfo: %v", err)
	}
	if len(info.Warnings) != 1 || !strings.Contains(info.Warnings[0], "no video or audio streams") {
		t.Errorf("warnings = %v, want no-streams warning", info.Warnings)
	}
}

func TestFormatStream(t *testing.T) {
	s := streamInfo{
		Type:     "video",
		Codec:    "h264",
		Width:    1660,
		Height:   1080,
		FPS:      29.97,
		Duration: 74.8,
	}
	got := formatStream(s)
	for _, want := range []string{"video:", "h264", "1660×1080", "29.97fps", "1m15s"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatStream missing %q in %q", want, got)
		}
	}
}
