package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func queueBinary(t *testing.T) string {
	t.Helper()
	requireFFmpeg(t)
	if testing.Short() {
		t.Skip("background queue integration")
	}
	path := filepath.Join(t.TempDir(), "shrink")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", path, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build queue executable: %v %s", err, out)
	}
	return path
}
func queueCLI(t *testing.T, exe, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(exe, append(args, "-m")...)
	cmd.Env = append(os.Environ(), "SHRINK_DATA_DIR="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("queue CLI %v: %v %s", args, err, out)
	}
	return out
}
func stopTestWorker(t *testing.T, exe, dir string) {
	t.Helper()
	cmd := exec.Command(exe, "que", "stop", "-m")
	cmd.Env = append(os.Environ(), "SHRINK_DATA_DIR="+dir)
	_ = cmd.Run()
	s := queueStore{dir: dir}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		running, err := s.running()
		if err == nil && !running {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("background worker did not exit during cleanup")
}
func TestQueueBackgroundIntegrationAndPrivateStats(t *testing.T) {
	exe := queueBinary(t)
	dir := t.TempDir()
	s := queueStore{dir: dir}
	t.Cleanup(func() { stopTestWorker(t, exe, dir) })
	source := fixture(t, "private-recording.wav", "-f", "lavfi", "-i", "sine=duration=0.4")
	queueCLI(t, exe, dir, "setting", "--cpu", "100%", "--jobs", "2")
	raw := queueCLI(t, exe, dir, "que", source, "mp3", "--next", "--boost", "-d")
	var added struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &added); err != nil || added.ID == "" {
		t.Fatal(string(raw), err)
	}
	q := waitQueue(t, s, func(q queueState) bool {
		return len(q.Jobs) == 1 && q.Jobs[0].State == "completed" && !fileExists(source)
	})
	if q.Stats.Files != 1 || q.Stats.InputBytes == 0 || q.Stats.OutputBytes == 0 {
		t.Fatal(q.Stats)
	}
	stats := queueCLI(t, exe, dir, "stats")
	persisted, err := os.ReadFile(filepath.Join(dir, "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stats), "private-recording") || strings.Contains(string(persisted), "private-recording") {
		t.Fatal("completed filename persisted", string(persisted))
	}
	outputs, _ := filepath.Glob(filepath.Join(filepath.Dir(source), "private-recording-*.mp3"))
	if len(outputs) != 1 {
		t.Fatal(outputs)
	}
	info := testApplication().probeFile(outputs[0])
	if !info.ok || info.audioCodec != "mp3" {
		t.Fatal(info)
	}
	// An intentionally stopped queue accepts jobs without starting a worker.
	queueCLI(t, exe, dir, "que", "stop")
	source2 := fixture(t, "waiting.wav", "-f", "lavfi", "-i", "sine=duration=0.3")
	queueCLI(t, exe, dir, "que", source2, "mp3")
	q, _ = s.snapshot(context.Background())
	if len(q.Jobs) != 2 || q.Jobs[1].State != "queued" {
		t.Fatal(q.Jobs)
	}
	queueCLI(t, exe, dir, "que", "start", "--cpu", "100%", "--jobs", "2")
	waitQueue(t, s, func(q queueState) bool { return q.Stats.Files == 2 })
}

func TestQueueHelperStopsFFmpegWhenWorkerConnectionCloses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("controlled POSIX child")
	}
	exe := queueBinary(t)
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	stopped := filepath.Join(dir, "stopped")
	fakebin := filepath.Join(dir, "bin")
	if err := os.Mkdir(fakebin, 0755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ntrap 'touch \"$QUEUE_STOPPED\"; exit 1' INT TERM\ntouch \"$QUEUE_READY\"\nwhile :; do sleep 0.1; done\n"
	if err := os.WriteFile(filepath.Join(fakebin, "ffmpeg"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "_queue-ffmpeg", "-i", "unused", filepath.Join(dir, ".shrink-que-j-1-test.mp3"))
	cmd.Stdin = read
	cmd.Env = append(os.Environ(), "PATH="+fakebin+string(os.PathListSeparator)+os.Getenv("PATH"), "QUEUE_READY="+ready, "QUEUE_STOPPED="+stopped)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !fileExists(ready) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !fileExists(ready) {
		_ = write.Close()
		_ = cmd.Wait()
		t.Fatal("controlled encoder did not start")
	}
	// Closing the only write end simulates abrupt death of the owning worker.
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if !fileExists(stopped) {
		t.Fatal("ffmpeg did not receive shutdown after worker connection closed")
	}
}

// Runs on Windows too: killing the helper directly leaves ffmpeg running and
// the temporary file behind; closing its lifeline lets it reap ffmpeg first.
func TestQueueCancellationLetsHelperStopEncoder(t *testing.T) {
	exe := queueBinary(t)
	partial := filepath.Join(t.TempDir(), ".shrink-que-cancel-test.wav")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	a := newApplication(ctx, nil, nil)
	a.queued = true
	a.workerExecutable = exe
	cmd, closeLifeline, err := a.encodingCommand([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-re", "-f", "lavfi", "-i", "sine=duration=30", "-c:a", "pcm_s16le", "-y", partial})
	if err != nil {
		t.Fatal(err)
	}
	defer closeLifeline()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	defer func() { cancel(); closeLifeline() }()
	deadline := time.Now().Add(5 * time.Second)
	for !fileExists(partial) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !fileExists(partial) {
		cancel()
		<-waited
		t.Fatal("encoder did not start writing its temporary output")
	}
	cancel()
	select {
	case err := <-waited:
		if err == nil {
			t.Fatal("cancelled encoding reported success")
		}
	case <-time.After(6 * time.Second):
		t.Fatal("helper did not stop after cancellation")
	}
	if fileExists(partial) {
		t.Fatal("helper exited before stopping its encoder and removing the partial output")
	}
}

func TestQueueKilledWorkerRestartsEntireJob(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("controlled POSIX encoder")
	}
	exe := queueBinary(t)
	realFFmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	source := fixture(t, "restart-from-zero.wav", "-f", "lavfi", "-i", "sine=duration=0.4")
	dir := t.TempDir()
	s := queueStore{dir: dir}
	fakebin := filepath.Join(dir, "bin")
	if err := os.Mkdir(fakebin, 0755); err != nil {
		t.Fatal(err)
	}
	ready, stopped, allow := filepath.Join(dir, "ready"), filepath.Join(dir, "stopped"), filepath.Join(dir, "allow")
	script := `#!/bin/sh
trap 'touch "$QUEUE_STOPPED"; exit 1' INT TERM
for arg do last=$arg; done
printf partial > "$last"
touch "$QUEUE_READY"
while [ ! -f "$QUEUE_ALLOW" ]; do sleep 0.1; done
exec "$QUEUE_REAL_FFMPEG" "$@"
`
	if err := os.WriteFile(filepath.Join(fakebin, "ffmpeg"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakebin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("QUEUE_READY", ready)
	t.Setenv("QUEUE_STOPPED", stopped)
	t.Setenv("QUEUE_ALLOW", allow)
	t.Setenv("QUEUE_REAL_FFMPEG", realFFmpeg)
	t.Cleanup(func() { stopTestWorker(t, exe, dir) })
	queueCLI(t, exe, dir, "que", source, "mp3", "--cpu", "100%")
	before := waitQueue(t, s, func(q queueState) bool { return len(q.Jobs) == 1 && q.Jobs[0].State == "running" && fileExists(ready) })
	process, err := os.FindProcess(before.Worker.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	process.Release()
	deadline := time.Now().Add(5 * time.Second)
	for !fileExists(stopped) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !fileExists(stopped) {
		t.Fatal("encoder was orphaned after worker was killed")
	}
	if err := os.WriteFile(allow, []byte("resume by redoing"), 0600); err != nil {
		t.Fatal(err)
	}
	queueCLI(t, exe, dir, "que", "start", "--cpu", "100%")
	after := waitQueue(t, s, func(q queueState) bool { return q.Jobs[0].State == "completed" })
	if after.Jobs[0].Attempts != 2 || after.Stats.Files != 1 {
		t.Fatalf("job was not restarted exactly once: %+v", after)
	}
	if fileExists(before.Jobs[0].Temp) {
		t.Fatal("interrupted partial output survived recovery")
	}
	outputs, _ := filepath.Glob(filepath.Join(filepath.Dir(source), "restart-from-zero-*.mp3"))
	if len(outputs) != 1 || !testApplication().probeFile(outputs[0]).ok {
		t.Fatal("restart did not produce one complete encode", outputs)
	}
}

func TestQueueTwoRealEncodersRunConcurrently(t *testing.T) {
	if runtime.GOOS == "windows" || queueCPUs() < 2 {
		t.Skip("controlled POSIX encoders and two logical CPUs required")
	}
	exe := queueBinary(t)
	realFFmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	first := fixture(t, "parallel-first.wav", "-f", "lavfi", "-i", "sine=duration=0.3")
	second := fixture(t, "parallel-second.wav", "-f", "lavfi", "-i", "sine=duration=0.3")
	dir := t.TempDir()
	s := queueStore{dir: dir}
	markers := filepath.Join(dir, "markers")
	fakebin := filepath.Join(dir, "bin")
	allow := filepath.Join(dir, "allow")
	for _, path := range []string{markers, fakebin} {
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
trap 'exit 1' INT TERM
for arg do last=$arg; done
touch "$QUEUE_MARKERS/$(basename "$last")"
while [ ! -f "$QUEUE_ALLOW" ]; do sleep 0.1; done
exec "$QUEUE_REAL_FFMPEG" "$@"
`
	if err := os.WriteFile(filepath.Join(fakebin, "ffmpeg"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakebin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("QUEUE_ALLOW", allow)
	t.Setenv("QUEUE_MARKERS", markers)
	t.Setenv("QUEUE_REAL_FFMPEG", realFFmpeg)
	t.Cleanup(func() { stopTestWorker(t, exe, dir) })
	queueCLI(t, exe, dir, "que", "stop")
	queueCLI(t, exe, dir, "que", first, "mp3")
	queueCLI(t, exe, dir, "que", second, "mp3")
	queueCLI(t, exe, dir, "que", "start", "--cpu", "100%", "--jobs", "2")
	waitQueue(t, s, func(q queueState) bool {
		count := 0
		for _, j := range q.Jobs {
			if j.State == "running" {
				count++
			}
		}
		files, _ := os.ReadDir(markers)
		return count == 2 && len(files) == 2
	})
	if err := os.WriteFile(allow, []byte("finish both"), 0600); err != nil {
		t.Fatal(err)
	}
	waitQueue(t, s, func(q queueState) bool { return q.Stats.Files == 2 })
}
