package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestForegroundAndQueueStatsShareTotalsWithoutDoubleCounting(t *testing.T) {
	a, store := queueTestApp(t)
	// Existing queue totals survive upgrading to combined history.
	if err := store.update(a.ctx, func(q *queueState) error {
		q.Jobs = []queueJob{{ID: "queued", State: "running", Input: "secret-queued.wav"}}
		recordQueueOutcome(q, queueOutcome{id: "queued", report: &fileReport{inBytes: 200, outBytes: 100, took: time.Second, operation: "compress"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a.recordForegroundOutcome("secret-direct.WAV", &fileReport{inBytes: 100, outBytes: 25, took: time.Second, operation: "compress"}, nil)
	a.recordForegroundOutcome("secret-video.mp4", &fileReport{inBytes: 1000, outBytes: 10, took: time.Second, operation: "extract_audio"}, nil)
	a.recordForegroundOutcome("already-done.wav", &fileReport{noop: true}, nil)
	a.recordForegroundOutcome("failed.wav", nil, errors.New("private ffmpeg error"))
	a.recordForegroundOutcome("cancelled.wav", nil, errCancelled)
	// The queue execution path cannot persist a second independent record.
	a.queued = true
	a.recordForegroundOutcome("secret-queued.wav", &fileReport{inBytes: 200, outBytes: 100}, nil)
	q, err := store.snapshot(a.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if q.Stats.Files != 3 || q.Stats.CompressedFiles != 2 || q.Stats.DerivedFiles != 1 || q.Stats.SavedBytes != 175 || q.Stats.InputBytes != 1300 || q.Stats.OutputBytes != 135 || q.Stats.ProcessingMS != 3000 || q.Stats.Skipped != 1 || q.Stats.FailedAttempts != 1 {
		t.Fatalf("incorrect combined stats: %+v", q.Stats)
	}
	if q.Stats.Types[".wav"].Files != 2 || q.Stats.Types[".mp4"].SavedBytes != 0 {
		t.Fatal(q.Stats.Types)
	}
	data, _ := os.ReadFile(filepath.Join(store.dir, "queue.json"))
	for _, secret := range []string{"secret-", "already-done", "failed.wav", "private ffmpeg error"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("foreground history retained private data: %s", data)
		}
	}
}

func TestForegroundStatsConcurrentWithQueueCompletion(t *testing.T) {
	a, store := queueTestApp(t)
	var wg sync.WaitGroup
	failures := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			a.recordForegroundOutcome("foreground.wav", &fileReport{inBytes: 100, outBytes: 50}, nil)
		}()
		go func() {
			defer wg.Done()
			if err := store.update(a.ctx, func(q *queueState) error {
				q.Jobs = []queueJob{{ID: "queue", State: "running", Input: "queued.wav"}}
				recordQueueOutcome(q, queueOutcome{id: "queue", report: &fileReport{inBytes: 100, outBytes: 50}})
				return nil
			}); err != nil {
				failures <- err
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	q, err := store.snapshot(a.ctx)
	if err != nil || q.Stats.Files != 40 || q.Stats.SavedBytes != 2000 {
		t.Fatalf("concurrent totals lost: %+v %v", q.Stats, err)
	}
}

func TestForegroundStatsUnavailableDoesNotFailEncoding(t *testing.T) {
	source := fixture(t, "private-recording.wav", "-f", "lavfi", "-i", "sine=duration=0.1")
	var out, stderr bytes.Buffer
	a := newApplication(context.Background(), &out, &stderr)
	a.dataDir = t.TempDir()
	path := filepath.Join(a.dataDir, "queue.json")
	original := []byte("{broken")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if code := a.run([]string{source, "mp3", "-m"}); code != 0 {
		t.Fatalf("stats error failed completed encode: %d %s %s", code, out.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "could not save compression stats") || !fileExists(strings.TrimSuffix(source, ".wav")+"-compressed.mp3") {
		t.Fatal("missing stats warning or output", stderr.String())
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(original, after) {
		t.Fatal("corrupt stats were overwritten")
	}
}

func TestForegroundMediaStatsIncludeDeleteDerivedAndFolderRuns(t *testing.T) {
	_, store := queueTestApp(t)
	run := func(args ...string) int {
		t.Helper()
		a := newApplication(context.Background(), io.Discard, io.Discard)
		a.dataDir = store.dir
		return a.run(append(args, "-m"))
	}
	source := fixture(t, "deleted-original.wav", "-f", "lavfi", "-i", "sine=duration=0.4")
	if code := run(source, "mp3", "-d"); code != 0 || fileExists(source) {
		t.Fatal("foreground delete failed", code)
	}
	q, _ := store.snapshot(context.Background())
	if q.Stats.Files != 1 || q.Stats.InputBytes <= 0 || q.Stats.SavedBytes <= 0 {
		t.Fatal("original deletion lost source bytes", q.Stats)
	}
	output := strings.TrimSuffix(source, ".wav") + "-compressed.mp3"
	if code := run(output); code != 0 {
		t.Fatal("single-file no-op failed", code)
	}
	qNoop, _ := store.snapshot(context.Background())
	if qNoop.Stats.Files != 1 || qNoop.Stats.Skipped != 1 {
		t.Fatal("single-file no-op counted as an encode", qNoop.Stats)
	}
	video := fixture(t, "derived.mp4", "-f", "lavfi", "-i", "color=s=32x32:d=0.2", "-f", "lavfi", "-i", "sine=duration=0.2", "-c:v", "libx264", "-c:a", "aac", "-shortest")
	if code := run(video, "mp3"); code != 0 {
		t.Fatal("audio extraction failed", code)
	}
	q2, _ := store.snapshot(context.Background())
	if q2.Stats.Files != 2 || q2.Stats.DerivedFiles != 1 || q2.Stats.SavedBytes != q.Stats.SavedBytes {
		t.Fatal("extraction inflated savings", q2.Stats)
	}
	folder := t.TempDir()
	for _, name := range []string{"one.wav", "two.wav"} {
		input := fixture(t, name, "-f", "lavfi", "-i", "sine=duration=0.1")
		data, _ := os.ReadFile(input)
		if err := os.WriteFile(filepath.Join(folder, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if code := run("folder", folder, "mp3", "-j", "2"); code != 0 {
		t.Fatal("parallel folder encode failed", code)
	}
	q3, _ := store.snapshot(context.Background())
	if q3.Stats.Files != 4 || q3.Stats.CompressedFiles != 3 || q3.Stats.Types[".wav"].Files != 3 {
		t.Fatal("folder work missed or double counted", q3.Stats)
	}
	if code := run("folder", folder, "mp3", "-j", "2"); code != 0 {
		t.Fatal("folder rerun failed", code)
	}
	q4, _ := store.snapshot(context.Background())
	if q4.Stats.Files != 4 || q4.Stats.Skipped <= q3.Stats.Skipped {
		t.Fatal("rerun counted existing outputs as new encodes", q4.Stats)
	}
	before, _ := os.ReadFile(filepath.Join(store.dir, "queue.json"))
	if code := run("folder", folder, "mp3", "-o", "-n"); code != 0 {
		t.Fatal("dry run failed", code)
	}
	after, _ := os.ReadFile(filepath.Join(store.dir, "queue.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("dry run changed stats")
	}
}

func TestForegroundPlanningFailuresAreRecordedOnce(t *testing.T) {
	requireFFmpeg(t)
	_, store := queueTestApp(t)
	folder := t.TempDir()
	source := filepath.Join(folder, "invalid.wav")
	if err := os.WriteFile(source, []byte("not media"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{source, "mp3", "-m"}, {"folder", folder, "mp3", "-m"}} {
		a := newApplication(context.Background(), io.Discard, io.Discard)
		a.dataDir = store.dir
		if code := a.run(args); code != 1 {
			t.Fatal("invalid input accepted", args, code)
		}
	}
	q, err := store.snapshot(context.Background())
	if err != nil || q.Stats.FailedAttempts != 2 || q.Stats.Files != 0 || q.Stats.InputBytes != 0 {
		t.Fatalf("failed inputs incorrectly counted: %+v %v", q.Stats, err)
	}
}
