package main

import (
	"os"
	"path/filepath"
	"testing"
)

// resetNaming clears the reservation table and overwrite flag so each
// test starts with clean naming state.
func resetNaming(t *testing.T) {
	t.Helper()
	reservedMu.Lock()
	reservedOut = map[string]bool{}
	reservedMu.Unlock()
	overwrite = false
}

func TestCandidateBase(t *testing.T) {
	cases := []struct {
		input, outKind, fmtName, operation, resolution, level, want string
	}{
		{"/d/vid.mp4", "video", "mp4", "", "", "", "vid-compressed"},
		{"/d/vid.mp4", "video", "mp4", "", "720p", "neversee", "vid-720p-neversee"},
		{"/d/clip.mov", "audio", "mp3", "audio", "", "compress", "clip-audio-compress"},
		{"/d/clip.mov", "image", "png", "frame", "1080p", "", "clip-1080p-frame"},
		{"x.png", "image", "jpeg", "", "", "", "x-compressed"},
	}
	for _, c := range cases {
		got := candidateBase(c.input, c.outKind, c.fmtName, c.operation, c.resolution, c.level)
		if got != c.want {
			t.Errorf("candidateBase(%q...) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestOutExt(t *testing.T) {
	cases := []struct{ outKind, fmtName, want string }{
		{"video", "mp4", "mp4"},
		{"video", "mkv", "mkv"},
		{"video", "hevc", "mp4"}, // hevc rides in an mp4 container
		{"video", "av1", "mp4"},
		{"audio", "mp3", "mp3"},
		{"audio", "aac", "m4a"},
		{"image", "jpeg", "jpg"},
		{"image", "jpg", "jpg"},
		{"image", "png", "png"},
	}
	for _, c := range cases {
		if got := outExt(c.outKind, c.fmtName); got != c.want {
			t.Errorf("outExt(%q, %q) = %q, want %q", c.outKind, c.fmtName, got, c.want)
		}
	}
}

func TestOutputFileName(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "a.mp4")
	if err := os.WriteFile(input, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// fresh name
	resetNaming(t)
	if got := outputFileName(input, "video", "mp4", "", "", ""); got != filepath.Join(dir, "a-compressed.mp4") {
		t.Errorf("fresh output = %q", got)
	}

	// an existing output bumps the counter
	resetNaming(t)
	if err := os.WriteFile(filepath.Join(dir, "a-compressed.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := outputFileName(input, "video", "mp4", "", "", ""); got != filepath.Join(dir, "a-compressed-1.mp4") {
		t.Errorf("collision should bump the counter, got %q", got)
	}

	// -o claims the existing name instead of bumping
	resetNaming(t)
	overwrite = true
	if got := outputFileName(input, "video", "mp4", "", "", ""); got != filepath.Join(dir, "a-compressed.mp4") {
		t.Errorf("overwrite should claim the existing name, got %q", got)
	}
	overwrite = false

	// two claims in the same run can't share a name
	resetNaming(t)
	if err := os.Remove(filepath.Join(dir, "a-compressed.mp4")); err != nil {
		t.Fatal(err)
	}
	if got := outputFileName(input, "video", "mp4", "", "", ""); got != filepath.Join(dir, "a-compressed.mp4") {
		t.Errorf("first claim = %q", got)
	}
	if got := outputFileName(input, "video", "mp4", "", "", ""); got != filepath.Join(dir, "a-compressed-1.mp4") {
		t.Errorf("second claim should bump, got %q", got)
	}

	// tags and format-aware extensions
	resetNaming(t)
	if got := outputFileName(input, "video", "hevc", "", "", ""); got != filepath.Join(dir, "a-compressed.mp4") {
		t.Errorf("hevc output should use the mp4 extension, got %q", got)
	}
}

func TestBitrateForLevel(t *testing.T) {
	cases := []struct {
		compress string
		bitRate  int64
		want     int
	}{
		{"compress", 6_000_000, 3000},
		{"spacesaver", 6_000_000, 2100},
		{"neversee", 6_000_000, 1200},
		{"neversee", 800_000, 250}, // floor for tiny sources
	}
	for _, c := range cases {
		if got := bitrateForLevel(c.compress, c.bitRate); got != c.want {
			t.Errorf("bitrateForLevel(%q, %d) = %d, want %d", c.compress, c.bitRate, got, c.want)
		}
	}
}

func TestParseMissingEncoder(t *testing.T) {
	msg := "[vost#0:0] Unknown encoder 'libvorbis'\nmore noise\n"
	if got := parseMissingEncoder(msg); got != "libvorbis" {
		t.Errorf("parseMissingEncoder = %q, want libvorbis", got)
	}
	if got := parseMissingEncoder("unrelated error"); got != "" {
		t.Errorf("parseMissingEncoder should find nothing, got %q", got)
	}
}

func TestFirstMeaningfulLine(t *testing.T) {
	msg := "Error opening output file out.mp4.\nreal problem here\n"
	if got := firstMeaningfulLine(msg); got != "real problem here" {
		t.Errorf("firstMeaningfulLine = %q", got)
	}
	if got := firstMeaningfulLine("\n \n"); got != "unknown error" {
		t.Errorf("empty stderr should say unknown error, got %q", got)
	}
}

func TestHumanSize(t *testing.T) {
	verbose = false
	if got := humanSize(2048); got != "2KB" {
		t.Errorf("humanSize(2048) = %q, want 2KB", got)
	}
	if got := humanSize(5 * 1024 * 1024); got != "5.00MB" {
		t.Errorf("humanSize(5MB) = %q, want 5.00MB", got)
	}
	verbose = true
	if got := humanSize(1024 * 1024); got != "1024KB" {
		t.Errorf("verbose humanSize(1MB) = %q, want 1024KB", got)
	}
	verbose = false
}

func TestSavingsText(t *testing.T) {
	// unstyled output (pipes) uses plain +/- arrows
	if got := savingsText(100, 25); got != "- 75%" {
		t.Errorf("savingsText(100,25) = %q, want \"- 75%%\"", got)
	}
	if got := savingsText(100, 125); got != "+ 25%" {
		t.Errorf("savingsText(100,125) = %q, want \"+ 25%%\"", got)
	}
	if got := savingsText(0, 10); got != "" {
		t.Errorf("savingsText(0,10) = %q, want empty", got)
	}
}
