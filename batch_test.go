package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestKindAllowed(t *testing.T) {
	// no format targets: every kind is in scope
	if !kindAllowed("video", options{}) || !kindAllowed("image", options{}) || !kindAllowed("audio", options{}) {
		t.Error("with no format targets every kind should be allowed")
	}
	onlyVideo := options{videoFmt: "mp4"}
	if !kindAllowed("video", onlyVideo) {
		t.Error("video target should allow video files")
	}
	if kindAllowed("image", onlyVideo) || kindAllowed("audio", onlyVideo) {
		t.Error("video target should not allow image or audio files")
	}
	if kindAllowed("unknown", onlyVideo) {
		t.Error("unknown kind is never allowed")
	}
	// a speed target excludes images, which cannot be sped
	speedOnly := options{speed: "x5"}
	if kindAllowed("image", speedOnly) {
		t.Error("a speed target should not allow image files")
	}
	if !kindAllowed("video", speedOnly) || !kindAllowed("audio", speedOnly) {
		t.Error("a speed target should allow video and audio")
	}
}

func TestFailMsg(t *testing.T) {
	// errors that already name the file stay unchanged
	err := fmt.Errorf("a.mp4: does not look like a valid video file")
	if got := failMsg("/x/a.mp4", err); got != err.Error() {
		t.Errorf("failMsg should not repeat the file, got %q", got)
	}
	// otherwise the file is named
	err = fmt.Errorf("no audio track")
	if got, want := failMsg("/x/a.mp4", err), "a.mp4: no audio track"; got != want {
		t.Errorf("failMsg = %q, want %q", got, want)
	}
}

func TestWouldProduce(t *testing.T) {
	dir := t.TempDir()

	// plain video re-encode
	f := filepath.Join(dir, "v.mp4")
	if got, want := wouldProduce(f, "video", options{}), filepath.Join(dir, "v-compressed.mp4"); got != want {
		t.Errorf("video default = %q, want %q", got, want)
	}

	// audio extraction names the operation
	if got, want := wouldProduce(f, "video", options{audioFmt: "mp3"}), filepath.Join(dir, "v-audio.mp3"); got != want {
		t.Errorf("audio extract = %q, want %q", got, want)
	}

	// frame grabs tag the operation and resolution
	if got, want := wouldProduce(f, "video", options{imageFmt: "png", resolution: "720p"}), filepath.Join(dir, "v-720p-frame.png"); got != want {
		t.Errorf("frame grab = %q, want %q", got, want)
	}

	// resolution and level tags
	if got, want := wouldProduce(f, "video", options{resolution: "1080p", compress: "neversee"}), filepath.Join(dir, "v-1080p-neversee.mp4"); got != want {
		t.Errorf("tagged video = %q, want %q", got, want)
	}

	// hevc output lands in an mp4 container
	if got, want := wouldProduce(f, "video", options{videoFmt: "hevc"}), filepath.Join(dir, "v-compressed.mp4"); got != want {
		t.Errorf("hevc = %q, want %q", got, want)
	}

	// speed targets tag the output name
	if got, want := wouldProduce(f, "video", options{speed: "x5"}), filepath.Join(dir, "v-x5.mp4"); got != want {
		t.Errorf("video speed = %q, want %q", got, want)
	}
	if got, want := wouldProduce(f, "video", options{speed: "x0.1", compress: "spacesaver"}), filepath.Join(dir, "v-x0.1-spacesaver.mp4"); got != want {
		t.Errorf("video speed + level = %q, want %q", got, want)
	}
	if got, want := wouldProduce(filepath.Join(dir, "s.mp3"), "audio", options{speed: "x2.5"}), filepath.Join(dir, "s-x2.5.mp3"); got != want {
		t.Errorf("audio speed = %q, want %q", got, want)
	}

	// image and audio defaults
	if got, want := wouldProduce(filepath.Join(dir, "p.png"), "image", options{}), filepath.Join(dir, "p-compressed.jpg"); got != want {
		t.Errorf("image default = %q, want %q", got, want)
	}
	if got, want := wouldProduce(filepath.Join(dir, "s.mp3"), "audio", options{}), filepath.Join(dir, "s-compressed.mp3"); got != want {
		t.Errorf("audio default = %q, want %q", got, want)
	}
}

func TestPerKindOpts(t *testing.T) {
	raw := options{videoFmt: "mp4", imageFmt: "jpeg", audioFmt: "mp3", resolution: "720p"}
	// a video file ignores the image/audio targets a batch run was given
	video := perKindOpts("video", raw)
	if video.videoFmt != "mp4" || video.imageFmt != "" || video.audioFmt != "" {
		t.Errorf("perKindOpts(video) = %+v", video)
	}
	if video.resolution != "720p" {
		t.Error("perKindOpts should keep non-format targets")
	}
	if got := perKindOpts("image", raw); got.imageFmt != "jpeg" || got.videoFmt != "" || got.audioFmt != "" {
		t.Errorf("perKindOpts(image) = %+v", got)
	}
	if got := perKindOpts("audio", raw); got.audioFmt != "mp3" || got.videoFmt != "" || got.imageFmt != "" {
		t.Errorf("perKindOpts(audio) = %+v", got)
	}
}

func TestLooksConverted(t *testing.T) {
	converted := []string{
		"vid-compressed.mp4", "vid-720p.mp4", "vid-720p-neversee.mp4",
		"vid-4k.mp4", "clip-audio.mp3", "clip-frame.png", "vid-spacesaver.mp4",
		"clip-x5.mp4", "clip-x0.1.mp3", "tone-x2.5.wav",
	}
	for _, name := range converted {
		if !looksConverted(name) {
			t.Errorf("looksConverted(%q) should be true", name)
		}
	}
	fresh := []string{
		"IMG_1234.MOV", "holiday.mp4", "IMG_1080p.MOV", "clip.mp3", "note.txt",
		"mix-x.mp4", "box-drawer.mp4",
	}
	for _, name := range fresh {
		if looksConverted(name) {
			t.Errorf("looksConverted(%q) should be false", name)
		}
	}
}

func TestWorthRevisit(t *testing.T) {
	// an explicit level always squeezes, whatever the file is called:
	// name detection must never override a direct user request
	if !worthRevisit("whatever.mp4", options{compress: "compress"}) {
		t.Error("an explicit level should always revisit a converted-looking file")
	}
	if !worthRevisit("whatever.mp4", options{compress: "neversee"}) {
		t.Error("an explicit level should always revisit a converted-looking file")
	}
	// so does an explicit speed request
	if !worthRevisit("whatever.mp4", options{speed: "x5"}) {
		t.Error("an explicit speed should always revisit a converted-looking file")
	}
	// no level, speed or resolution: nothing this run would change
	if worthRevisit("whatever.mp4", options{}) {
		t.Error("no level, speed or resolution should not revisit")
	}
}

func TestFindAndCountMediaFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.mp4")
	write("b.txt")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	write("sub/c.mov")
	if err := os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(".hidden/d.mp4")

	// non-recursive: top level only
	if got := findMediaFiles(dir, false); len(got) != 1 || got[0] != filepath.Join(dir, "a.mp4") {
		t.Errorf("findMediaFiles(non-recursive) = %v", got)
	}

	// recursive: subfolders join in, hidden stay out
	if got := findMediaFiles(dir, true); len(got) != 2 {
		t.Errorf("findMediaFiles(recursive) = %v, want 2 files", got)
	}

	// the count-only walk agrees with the recursive scan
	if n := countMediaFiles(dir); n != 2 {
		t.Errorf("countMediaFiles = %d, want 2", n)
	}
}
