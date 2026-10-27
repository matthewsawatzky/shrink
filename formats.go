package main

// formatSpec is shared by parsing, encoding, request matching, doctor,
// completion and help. Aliases point at their canonical target name.
type formatSpec struct {
	codec, ext, probe, container, aliasOf, audioCodec string
	args                                              []string
}

var videoFormats = map[string]formatSpec{
	"mp4":  {codec: "libx264", ext: "mp4", probe: "h264", container: "mp4", audioCodec: "aac", args: []string{"-preset", "medium", "-movflags", "+faststart"}},
	"mkv":  {codec: "libx264", ext: "mkv", probe: "h264", container: "matroska", audioCodec: "aac", args: []string{"-preset", "medium"}},
	"webm": {codec: "libvpx-vp9", ext: "webm", probe: "vp9", container: "webm", audioCodec: "libopus", args: []string{"-b:v", "0", "-row-mt", "1", "-deadline", "good", "-cpu-used", "4"}},
	"mov":  {codec: "libx264", ext: "mov", probe: "h264", container: "mov", audioCodec: "aac", args: []string{"-preset", "medium", "-movflags", "+faststart"}},
	"avi":  {codec: "libx264", ext: "avi", probe: "h264", container: "avi", audioCodec: "aac", args: []string{"-preset", "medium"}},
	"hevc": {codec: "libx265", ext: "mp4", probe: "hevc", container: "mp4", audioCodec: "aac", args: []string{"-preset", "medium", "-tag:v", "hvc1"}},
	"av1":  {codec: "libsvtav1", ext: "mp4", probe: "av1", container: "mp4", audioCodec: "aac", args: []string{"-preset", "8"}},
}
var imageFormats = map[string]formatSpec{
	"jpeg": {codec: "mjpeg", ext: "jpg", probe: "mjpeg", container: "jpeg_pipe"},
	"jpg":  {codec: "mjpeg", ext: "jpg", probe: "mjpeg", container: "jpeg_pipe", aliasOf: "jpeg"},
	"png":  {codec: "png", ext: "png", probe: "png", container: "png_pipe"},
	"webp": {codec: "libwebp", ext: "webp", probe: "webp", container: "webp_pipe"},
	"gif":  {codec: "gif", ext: "gif", probe: "gif", container: "gif"},
}
var audioFormats = map[string]formatSpec{
	"mp3":  {codec: "libmp3lame", ext: "mp3", probe: "mp3", container: "mp3"},
	"aac":  {codec: "aac", ext: "m4a", probe: "aac", container: "mp4", aliasOf: "m4a"},
	"m4a":  {codec: "aac", ext: "m4a", probe: "aac", container: "mp4"},
	"opus": {codec: "libopus", ext: "opus", probe: "opus", container: "ogg"},
	"ogg":  {codec: "libvorbis", ext: "ogg", probe: "vorbis", container: "ogg"},
	"wav":  {codec: "pcm_s16le", ext: "wav", probe: "pcm_s16le", container: "wav"},
	"flac": {codec: "flac", ext: "flac", probe: "flac", container: "flac"},
}

func formatsForKind(kind string) map[string]formatSpec {
	switch kind {
	case "video":
		return videoFormats
	case "image":
		return imageFormats
	case "audio":
		return audioFormats
	}
	return nil
}
