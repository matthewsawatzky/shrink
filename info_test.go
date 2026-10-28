package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInfoWithoutFilesShowsInstallationAndQueueStats(t *testing.T) {
	for _, machine := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "machine"}[machine], func(t *testing.T) {
			// Installation info must work even if media dependencies are absent.
			t.Setenv("PATH", "")
			var out, stderr bytes.Buffer
			a := newApplication(context.Background(), &out, &stderr)
			a.dataDir = t.TempDir()
			s := queueStore{dir: a.dataDir}
			if err := s.update(a.ctx, func(q *queueState) error {
				q.Paused = true
				q.Jobs = []queueJob{{ID: "private", State: "queued", Input: "/private/secret-recording.wav"}}
				q.Stats = queueStats{Files: 3, CompressedFiles: 2, DerivedFiles: 1, InputBytes: 120 * 1024, OutputBytes: 55 * 1024, CompressionInputBytes: 100 * 1024, CompressionOutputBytes: 50 * 1024, SavedBytes: 50 * 1024, ProcessingMS: 2500, Skipped: 1, FailedAttempts: 2, Types: map[string]typeStats{".wav": {Files: 2}, ".mp4": {Files: 1}}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(a.dataDir, "queue.json"))
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"info"}
			if machine {
				args = append(args, "-m")
			}
			if code := a.run(args); code != 0 {
				t.Fatalf("info failed without ffprobe: %d %s %s", code, out.String(), stderr.String())
			}
			if strings.Contains(out.String()+stderr.String(), "secret-recording") {
				t.Fatal("overview exposed a queued filename")
			}
			after, _ := os.ReadFile(filepath.Join(a.dataDir, "queue.json"))
			if !bytes.Equal(before, after) {
				t.Fatal("overview modified queue data")
			}
			if machine {
				var info installationInfo
				if err := json.Unmarshal(out.Bytes(), &info); err != nil {
					t.Fatal(err, out.String())
				}
				self, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				binary, err := os.Stat(self)
				if err != nil {
					t.Fatal(err)
				}
				if info.Version != version || info.Platform != runtime.GOOS+"/"+runtime.GOARCH || info.BinarySizeBytes != binary.Size() || info.StatsScope != "foreground_and_queue" || info.Stats.Files != 3 || info.Stats.SavedBytes != 50*1024 || stderr.Len() != 0 {
					t.Fatalf("incorrect overview: %+v stderr=%q", info, stderr.String())
				}
				return
			}
			text := stderr.String()
			for _, want := range []string{"shrink " + version, "Binary", "Platform", "3 files (2 same-kind, 1 derived)", "50KB", "- 50%", "mp4", "wav", "foreground + queue"} {
				if !strings.Contains(text, want) {
					t.Errorf("missing %q in overview:\n%s", want, text)
				}
			}
		})
	}
}

func TestInfoOverviewWithNoHistory(t *testing.T) {
	t.Setenv("PATH", "")
	var out, stderr bytes.Buffer
	a := newApplication(context.Background(), &out, &stderr)
	a.dataDir = t.TempDir()
	if code := a.run([]string{"info"}); code != 0 || !strings.Contains(stderr.String(), "No completed encodes recorded yet.") || !strings.Contains(stderr.String(), "0 files") {
		t.Fatalf("empty history: code=%d %s %s", code, out.String(), stderr.String())
	}
	if fileExists(filepath.Join(a.dataDir, "queue.json")) {
		t.Fatal("empty overview wrote queue history")
	}
}

func TestInfoOverviewPreservesCorruptHistory(t *testing.T) {
	var out, stderr bytes.Buffer
	a := newApplication(context.Background(), &out, &stderr)
	a.dataDir = t.TempDir()
	path := filepath.Join(a.dataDir, "queue.json")
	before := []byte("{broken")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	if code := a.run([]string{"info", "-m"}); code != 1 || !strings.Contains(out.String(), "could not load compression stats") {
		t.Fatalf("corrupt history was ignored: %d %s", code, out.String())
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("overview replaced corrupt history")
	}
}

func TestInfoWithFilesStillReturnsMediaArray(t *testing.T) {
	first := fixture(t, "first.wav", "-f", "lavfi", "-i", "sine=duration=0.1")
	second := fixture(t, "second.wav", "-f", "lavfi", "-i", "sine=duration=0.2")
	var out, stderr bytes.Buffer
	a := newApplication(context.Background(), &out, &stderr)
	a.dataDir = t.TempDir()
	if code := a.run([]string{"info", first, second, "-m"}); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	var infos []fileInfo
	if err := json.Unmarshal(out.Bytes(), &infos); err != nil || len(infos) != 2 {
		t.Fatal(err, out.String())
	}
	if infos[0].Path != first || infos[1].Path != second || infos[0].Kind != "audio" || infos[1].Format.Size <= 0 {
		t.Fatal(infos)
	}
	if fileExists(filepath.Join(a.dataDir, "queue.json")) {
		t.Fatal("media info unexpectedly wrote queue data")
	}
}

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
	a := testApplication()

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

	info, err := a.parseProbeInfo("clip.mp4", data)
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
	if info.Format.Extension != "mp4" {
		t.Errorf("Format.Extension = %q, want mp4", info.Format.Extension)
	}
	if info.Format.AllNames != "mov,mp4,m4a,3gp,3g2,mj2" {
		t.Errorf("Format.AllNames = %q, want mov,mp4,m4a,3gp,3g2,mj2", info.Format.AllNames)
	}
	if !info.Format.MatchOK {
		t.Error("Format.MatchOK = false, want true for .mp4 in container list")
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
	audio := info.Streams[1]
	if audio.Type != "audio" || audio.Codec != "aac" || audio.SampleRate != 48000 || audio.Channels != 2 {
		t.Errorf("audio stream = %+v", audio)
	}
	if len(info.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", info.Warnings)
	}
}

func TestParseProbeInfoDurationMismatch(t *testing.T) {
	a := testApplication()

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

	info, err := a.parseProbeInfo("weird.mp4", data)
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
	a := testApplication()

	data := []byte(`{
		"format": {"format_name": "mp4", "duration": "10.000000", "size": "1000"},
		"streams": []
	}`)

	info, err := a.parseProbeInfo("empty.mp4", data)
	if err != nil {
		t.Fatalf("parseProbeInfo: %v", err)
	}
	if len(info.Warnings) != 1 || !strings.Contains(info.Warnings[0], "no video or audio streams") {
		t.Errorf("warnings = %v, want no-streams warning", info.Warnings)
	}
}

func TestParseProbeInfoExtensionMismatch(t *testing.T) {
	a := testApplication()

	data := []byte(`{
		"format": {"format_name": "matroska,webm", "duration": "10.000000", "size": "1000"},
		"streams": [
			{
				"index": 0,
				"codec_type": "video",
				"codec_name": "h264",
				"width": 1920,
				"height": 1080,
				"avg_frame_rate": "30/1",
				"nb_frames": "300",
				"duration": "10.000000"
			}
		]
	}`)

	info, err := a.parseProbeInfo("clip.mp4", data)
	if err != nil {
		t.Fatalf("parseProbeInfo: %v", err)
	}
	if info.Format.MatchOK {
		t.Fatal("expected extension mismatch for .mp4 vs matroska")
	}
	found := false
	for _, w := range info.Warnings {
		if strings.Contains(w, "file extension .mp4 does not match") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want extension-mismatch warning", info.Warnings)
	}
}

func TestFormatStream(t *testing.T) {
	a := testApplication()

	s := streamInfo{
		Type:     "video",
		Codec:    "h264",
		Width:    1660,
		Height:   1080,
		FPS:      29.97,
		Duration: 74.8,
	}
	got := a.formatStream(s)
	for _, want := range []string{"video:", "h264", "1660×1080", "29.97fps", "1m15s"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatStream missing %q in %q", want, got)
		}
	}
}
