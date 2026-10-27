package main

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func invoke(t *testing.T, args ...string) (int, machineOutput, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	a := newApplication(context.Background(), &stdout, &stderr)
	code := a.run(append(args, "-m"))
	var result machineOutput
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil && code == 0 {
		t.Fatalf("invalid JSON: %s (%v)", stdout.String(), err)
	}
	return code, result, stdout.String() + stderr.String()
}

func requireFFmpeg(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("integration fixture requires " + bin)
		}
	}
}

func fixture(t *testing.T, name string, args ...string) string {
	t.Helper()
	requireFFmpeg(t)
	path := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("ffmpeg", append(append([]string{"-hide_banner", "-loglevel", "error"}, args...), path)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	return path
}

func TestRejectNonFiniteTargets(t *testing.T) {
	for _, target := range []string{"xinf", "x+inf", "xnan", "x-inf", "x1e309", "x1e-320"} {
		if _, _, ok := parseSpeed(target); ok {
			t.Errorf("accepted %s", target)
		}
	}
	for _, target := range []string{"NaNs", "Infs", "1e309s", "1e308m"} {
		if _, ok := parseMakeDuration(target); ok {
			t.Errorf("accepted %s", target)
		}
	}
	for _, factor := range []float64{math.Inf(1), math.NaN(), 0, -1} {
		if got := atempoChain(factor); got != "" {
			t.Errorf("invalid factor returned %s", got)
		}
	}
}

func TestCLIFlagsAndIndependentInvocations(t *testing.T) {
	for _, args := range [][]string{{"--bad", "-m"}, {"-m", "--bad"}, {"-j", "bad", "-m"}, {"doctor", "-d", "-m"}} {
		var out, errout bytes.Buffer
		code := newApplication(context.Background(), &out, &errout).run(args)
		var e machineError
		if code != 1 || json.Unmarshal(out.Bytes(), &e) != nil || e.Error == "" || errout.Len() != 0 {
			t.Fatalf("%v: code=%d stdout=%s stderr=%s", args, code, &out, &errout)
		}
	}
	var out, errout bytes.Buffer
	if code := newApplication(context.Background(), &out, &errout).run([]string{"--help"}); code != 0 || !strings.Contains(errout.String(), "usage:") {
		t.Fatalf("help: %d %s", code, &errout)
	}
	out.Reset()
	errout.Reset()
	if code := newApplication(context.Background(), &out, &errout).run([]string{"--version"}); code != 0 || !strings.HasPrefix(out.String(), "shrink ") {
		t.Fatalf("version: %d %s", code, &out)
	}
	out.Reset()
	if code := newApplication(context.Background(), &out, &errout).run([]string{"--completions", "bash"}); code != 0 || !strings.Contains(out.String(), "info") {
		t.Fatalf("completion: %d %s", code, &out)
	}
	a := testApplication()
	c, err := a.parseCLI([]string{"--", "-clip.wav", "mp3"})
	if err != nil || len(c.args) != 2 || c.args[0] != "-clip.wav" {
		t.Fatalf("literal arguments: %+v %v", c, err)
	}
}

func TestDefaultBatchPreviewAndRerun(t *testing.T) {
	input := fixture(t, "tone.wav", "-f", "lavfi", "-i", "sine=duration=0.3")
	dir := filepath.Dir(input)
	code, preview, msg := invoke(t, "folder", dir, "-n")
	if code != 0 || len(preview.Files) != 1 {
		t.Fatal(msg)
	}
	code, result, msg := invoke(t, "folder", dir)
	if code != 0 || len(result.Files) != 1 || result.Files[0].Output != preview.Files[0].Output {
		t.Fatalf("preview/execution differ: %+v %+v %s", preview, result, msg)
	}
	code, again, msg := invoke(t, "folder", dir)
	if code != 0 || again.Summary.Processed != 0 || again.AlreadyDone != 2 {
		t.Fatalf("rerun: %+v %s", again, msg)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("rerun created extra files: %v", entries)
	}
}

func TestAnimatedImageCannotDeleteOriginal(t *testing.T) {
	input := fixture(t, "animation.gif", "-f", "lavfi", "-i", "testsrc2=s=64x64:r=5:d=1")
	before, _ := os.ReadFile(input)
	code, _, msg := invoke(t, input, "gif", "-d")
	after, err := os.ReadFile(input)
	if code != 1 || !strings.Contains(msg, "animated") || err != nil || !bytes.Equal(before, after) {
		t.Fatalf("animation not protected: %d %s %v", code, msg, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(input))
	if len(entries) != 1 {
		t.Fatalf("unexpected output: %v", entries)
	}
}

func TestExplicitContainerAndFormatConversion(t *testing.T) {
	input := fixture(t, "clip-compressed.mp4", "-f", "lavfi", "-i", "color=red:s=64x64:d=0.3", "-c:v", "libx264")
	code, converted, msg := invoke(t, input, "mkv")
	if code != 0 || len(converted.Files) != 1 || filepath.Ext(converted.Files[0].Output) != ".mkv" {
		t.Fatalf("container conversion: %+v %s", converted, msg)
	}
	code, preview, msg := invoke(t, "folder", filepath.Dir(input), "webm", "-n")
	if code != 0 || preview.Summary.Processed != 2 {
		t.Fatalf("format conversion skipped: %+v %s", preview, msg)
	}
}

func fakeFFmpeg(t *testing.T, body string) {
	t.Helper()
	requireFFmpeg(t)
	if runtime.GOOS == "windows" {
		t.Skip("controlled POSIX child")
	}
	dir := t.TempDir()
	// Every generated ffmpeg invocation ends with its private output path.
	script := "#!/bin/sh\nfor arg do last=$arg; done\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func assertNoTemps(t *testing.T, dir string) {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(dir, ".shrink-*"))
	if len(matches) > 0 {
		t.Fatalf("temporary output leaked: %v", matches)
	}
}

func TestFailedEncodePreservesExistingOutputAndAllowsRetry(t *testing.T) {
	input := fixture(t, "tone.wav", "-f", "lavfi", "-i", "sine=duration=0.3")
	output := filepath.Join(filepath.Dir(input), "tone-compressed.mp3")
	original := []byte("existing destination")
	if err := os.WriteFile(output, original, 0644); err != nil {
		t.Fatal(err)
	}
	t.Run("failed replacement", func(t *testing.T) {
		fakeFFmpeg(t, "printf broken > \"$last\"\nexit 1")
		code, _, msg := invoke(t, input, "mp3", "-o", "-d")
		got, _ := os.ReadFile(output)
		if code != 1 || !bytes.Equal(got, original) {
			t.Fatalf("failed replacement: %d %s %s", code, msg, got)
		}
		if _, err := os.Stat(input); err != nil {
			t.Fatal("original removed on failure", err)
		}
		assertNoTemps(t, filepath.Dir(input))
	})
	// A broken destination from an older version must not suppress a retry.
	code, result, msg := invoke(t, "folder", filepath.Dir(input), "mp3")
	if code != 1 || result.Summary.Processed != 1 || result.Summary.Failed != 1 {
		t.Fatalf("broken output suppressed retry: %+v %s", result, msg)
	}
	assertNoTemps(t, filepath.Dir(input))
}

func TestPublishDoesNotClobberConcurrentFile(t *testing.T) {
	input := fixture(t, "seed.wav", "-f", "lavfi", "-i", "sine=duration=0.3")
	output := filepath.Join(filepath.Dir(input), "result.wav")
	t.Setenv("SHRINK_TEST_SEED", input)
	t.Setenv("SHRINK_TEST_DEST", output)
	fakeFFmpeg(t, "cp \"$SHRINK_TEST_SEED\" \"$last\"\nprintf competing > \"$SHRINK_TEST_DEST\"")
	a := testApplication()
	_, err := a.encodeJob(input, output, "test", 0, []string{"-i", input, "-c:a", "pcm_s16le"})
	got, _ := os.ReadFile(output)
	if err == nil || string(got) != "competing" {
		t.Fatalf("destination clobbered: %v %q", err, got)
	}
	assertNoTemps(t, filepath.Dir(input))
}

func TestCancelWaitsForDelayedOutputAndCleansUp(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	output := filepath.Join(dir, "result.wav")
	t.Setenv("SHRINK_TEST_READY", ready)
	fakeFFmpeg(t, "trap '' INT\ntouch \"$SHRINK_TEST_READY\"\nsleep 0.2\nprintf partial > \"$last\"")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApplication(ctx, &bytes.Buffer{}, &bytes.Buffer{})
	done := make(chan error, 1)
	go func() { _, err := a.encodeJob("input.wav", output, "test", 0, nil); done <- err }()
	deadline := time.After(5 * time.Second)
	for !fileExists(ready) {
		select {
		case <-deadline:
			t.Fatal("child did not start")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != errCancelled {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel hung")
	}
	if fileExists(output) {
		t.Fatal("cancel published output")
	}
	assertNoTemps(t, dir)
}

func TestDownscaleUsesSharedTransparentAudioSettings(t *testing.T) {
	input := fixture(t, "clip-compressed.mkv", "-f", "lavfi", "-i", "color=red:s=640x480:d=0.3", "-f", "lavfi", "-i", "sine=duration=0.3", "-c:v", "libx264", "-c:a", "libmp3lame")
	p, err := testApplication().planJob(input, options{resolution: "340p", videoFmt: "mkv"})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(p.args, " ")
	if !strings.Contains(args, "-b:a 256k") || !strings.Contains(args, "-crf 18") {
		t.Fatalf("wrong transparent settings: %s", args)
	}
}

func TestIndependentApplicationsCanRunConcurrently(t *testing.T) {
	done := make(chan bool, 2)
	for _, args := range [][]string{{"--version"}, {"--help"}} {
		go func(args []string) {
			var out, stderr bytes.Buffer
			done <- newApplication(context.Background(), &out, &stderr).run(args) == 0
		}(args)
	}
	if !<-done || !<-done {
		t.Fatal("independent application failed")
	}
}

func TestDeleteRetainsOriginalWhenAuxiliaryStreamsWouldBeLost(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	subtitle := filepath.Join(dir, "captions.srt")
	if err := os.WriteFile(subtitle, []byte("1\n00:00:00,000 --> 00:00:00,300\nCaption\n"), 0644); err != nil {
		t.Fatal(err)
	}
	input := fixture(t, "with-subtitle.mkv", "-f", "lavfi", "-i", "color=red:s=64x64:d=0.3", "-f", "lavfi", "-i", "sine=duration=0.3", "-i", subtitle, "-map", "0:v", "-map", "1:a", "-map", "2:s", "-c:v", "libx264", "-c:a", "pcm_s16le", "-c:s", "srt")
	code, result, msg := invoke(t, input, "mp4", "-d")
	if code != 0 || len(result.Files) != 1 || result.Files[0].DeletedInput || !fileExists(input) {
		t.Fatalf("auxiliary streams lost with original: %+v %s", result, msg)
	}
	if !strings.Contains(result.Files[0].Warning, "required streams") {
		t.Fatalf("missing deletion explanation: %+v", result.Files[0])
	}
}

func TestVideoPreservesAllAudioTracks(t *testing.T) {
	input := fixture(t, "two-tracks.mkv", "-f", "lavfi", "-i", "color=red:s=64x64:d=0.3", "-f", "lavfi", "-i", "sine=duration=0.3", "-map", "0:v", "-map", "1:a", "-map", "1:a", "-c:v", "libx264", "-c:a", "pcm_s16le")
	code, result, msg := invoke(t, input, "mp4")
	if code != 0 || len(result.Files) != 1 {
		t.Fatal(msg)
	}
	info := testApplication().probeFile(result.Files[0].Output)
	if info.streams["audio"] != 2 {
		t.Fatalf("audio tracks lost: %+v", info)
	}
}

func TestCancelledApplicationReturns130(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, stderr bytes.Buffer
	if code := newApplication(ctx, &out, &stderr).run([]string{"--version"}); code != 130 {
		t.Fatalf("cancelled run returned %d", code)
	}
}

func TestHardwareRescaleUsesSharedPlanner(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("VideoToolbox selection applies to macOS")
	}
	input := fixture(t, "clip-compressed.mp4", "-f", "lavfi", "-i", "color=red:s=640x480:d=0.3", "-c:v", "libx264")
	a := testApplication()
	a.fast = true
	a.ffmpegEncoders = func() map[string]bool { return map[string]bool{"h264_videotoolbox": true, "hevc_videotoolbox": true} }
	p, err := a.planJob(input, options{resolution: "340p", videoFmt: "mp4"})
	if err != nil || p.operation != "rescale" || !strings.Contains(strings.Join(p.args, " "), "-c:v h264_videotoolbox") {
		t.Fatalf("hardware rescale: %+v %v", p, err)
	}
	p, err = a.planJob(input, options{videoFmt: "hevc"})
	if err != nil || !strings.Contains(strings.Join(p.args, " "), "-tag:v hvc1") {
		t.Fatalf("hardware HEVC tag: %+v %v", p, err)
	}
}

func TestInstallerFailsClosedOnMissingChecksum(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer")
	}
	dir := t.TempDir()
	bindir := filepath.Join(dir, "install")
	fakebin := filepath.Join(dir, "bin")
	if err := os.Mkdir(fakebin, 0755); err != nil {
		t.Fatal(err)
	}
	// Supply a fake download, then an empty checksum document. No network or
	// installation is performed; verification must fail before extracting it.
	script := "#!/bin/sh\nwhile [ $# -gt 0 ]; do\n if [ \"$1\" = -o ]; then shift; printf broken > \"$1\"; exit 0; fi\n shift\ndone\nexit 0\n"
	if err := os.WriteFile(filepath.Join(fakebin, "curl"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "install.sh", "v-test")
	cmd.Env = append(os.Environ(), "PATH="+fakebin+string(os.PathListSeparator)+os.Getenv("PATH"), "SHRINK_INSTALL_DIR="+bindir)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "checksum") {
		t.Fatalf("installer accepted unverified download: %v %s", err, out)
	}
	if fileExists(filepath.Join(bindir, "shrink")) {
		t.Fatal("installer wrote unverified binary")
	}
}
