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
