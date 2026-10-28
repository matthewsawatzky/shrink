package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func queueTestApp(t *testing.T) (*application, queueStore) {
	t.Helper()
	a := testApplication()
	a.dataDir = t.TempDir()
	return a, queueStore{dir: a.dataDir}
}
func addTestQueueJob(t *testing.T, s queueStore, name string, next, boost bool) queueJob {
	t.Helper()
	input := filepath.Join(s.dir, name+".wav")
	if err := os.WriteFile(input, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(input)
	if err != nil {
		t.Fatal(err)
	}
	var job queueJob
	err = s.update(context.Background(), func(q *queueState) error {
		q.Sequence++
		job = queueJob{ID: fmt.Sprintf("j-%06d", q.Sequence), Input: input, State: "queued", Size: info.Size(), Modified: info.ModTime().UnixNano(), Next: next, Boost: boost, Created: time.Now()}
		q.Jobs = append(q.Jobs, job)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return job
}
func waitQueue(t *testing.T, s queueStore, predicate func(queueState) bool) queueState {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		q, err := s.snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if predicate(q) {
			return q
		}
		time.Sleep(10 * time.Millisecond)
	}
	q, _ := s.snapshot(context.Background())
	t.Fatalf("queue did not reach expected state: %+v", q)
	return q
}
func TestQueueCPUParsingAndCapacity(t *testing.T) {
	for _, s := range []string{"0%", "101%", "20", "NaN%", "1.5%", "-5%"} {
		if _, err := parseCPU(s); err == nil {
			t.Errorf("accepted %s", s)
		}
	}
	for _, n := range []int{1, 20, 100} {
		if got, err := parseCPU(fmt.Sprintf("%d%%", n)); err != nil || got != n {
			t.Fatal(got, err)
		}
	}
	for _, c := range []struct{ cpus, cpu, jobs, slots, threads int }{{16, 20, 2, 2, 1}, {4, 20, 4, 1, 1}, {16, 100, 2, 2, 8}, {1, 20, 2, 1, 1}} {
		slots, threads := queueCapacity(c.cpus, c.cpu, c.jobs)
		if slots != c.slots || threads != c.threads {
			t.Errorf("%+v: %d %d", c, slots, threads)
		}
	}
	args := strings.Join(queueThreadArgs([]string{"-i", "file", "-c:v", "libx265"}, 2), " ")
	if !strings.Contains(args, "-threads 2 -i") || !strings.Contains(args, "-filter_threads 2") || !strings.Contains(args, "pools=2:frame-threads=1") {
		t.Fatal(args)
	}
}
func TestQueueStoreConcurrentUpdatesAndCorruption(t *testing.T) {
	_, s := queueTestApp(t)
	var wg sync.WaitGroup
	failures := make(chan error, 30)
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.update(context.Background(), func(q *queueState) error { q.Sequence++; return nil }); err != nil {
				failures <- err
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	q, err := s.snapshot(context.Background())
	if err != nil || q.Sequence != 30 {
		t.Fatalf("lost update: %+v %v", q, err)
	}
	path := filepath.Join(s.dir, "queue.json")
	before := []byte("{broken")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.update(context.Background(), func(q *queueState) error { q.Sequence++; return nil }); err == nil {
		t.Fatal("accepted corrupt state")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("corrupt state overwritten")
	}
}
func TestQueueWorkerLockIsExclusive(t *testing.T) {
	_, s := queueTestApp(t)
	lock, err := s.lock(context.Background(), "worker.lock", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.lock(context.Background(), "worker.lock", false); !errors.Is(err, errQueueBusy) {
		t.Fatalf("second worker admitted: %v", err)
	}
	releaseFileLock(lock)
	if running, err := s.running(); err != nil || running {
		t.Fatal(running, err)
	}
}
func TestQueueSelectionPriorityBoostAndSharedBudget(t *testing.T) {
	q := freshQueue()
	q.Settings = queueSettings{CPU: 100, Jobs: 2}
	q.Jobs = []queueJob{{ID: "ordinary", Input: "one", State: "queued"}, {ID: "boost", Input: "two", State: "queued", Next: true, Boost: true}}
	active := map[string]activeQueueJob{"running": {input: "other"}}
	if i, _, _, _ := selectQueueJob(&q, active, queueRunOptions{}, 100); i != -1 {
		t.Fatal("boost did not wait for existing work")
	}
	i, cpu, slots, _ := selectQueueJob(&q, map[string]activeQueueJob{}, queueRunOptions{}, 0)
	if i != 1 || cpu != 100 || slots != 1 {
		t.Fatal(i, cpu, slots)
	}
	q.Jobs[1].State = "running"
	if i, _, _, _ := selectQueueJob(&q, map[string]activeQueueJob{"boost": {input: "two", boost: true}}, queueRunOptions{}, 100); i != -1 {
		t.Fatal("ordinary job ran beside boosted job")
	}
	q.Jobs = []queueJob{{ID: "same", Input: "other", State: "queued"}, {ID: "different", Input: "different", State: "queued"}}
	i, _, _, _ = selectQueueJob(&q, active, queueRunOptions{}, 100)
	if queueCPUs() >= 2 && i != 1 {
		t.Fatal("did not skip busy input", i)
	}
	cpu, jobs := queueBudget(queueJob{}, queueSettings{CPU: 20, Jobs: 1}, queueRunOptions{CPU: 40, Jobs: 2})
	if cpu != 40 || jobs != 2 {
		t.Fatal(cpu, jobs)
	}
	cpu, _ = queueBudget(queueJob{CPU: 70}, queueSettings{CPU: 20, Jobs: 1}, queueRunOptions{CPU: 40})
	if cpu != 70 {
		t.Fatal(cpu)
	}
}

type recordedCPU struct {
	mu     sync.Mutex
	values []int
}

func (c *recordedCPU) SetPercent(n int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values = append(c.values, n)
	return nil
}
func (*recordedCPU) Close()       {}
func (*recordedCPU) Mode() string { return "test CPU controller" }
func (c *recordedCPU) snapshot() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int(nil), c.values...)
}

func TestQueueBoostFailureRestoresNormalLimitsAndForgetsNames(t *testing.T) {
	a, s := queueTestApp(t)
	control := &recordedCPU{}
	a.cpuFactory = func() cpuController { return control }
	boost := addTestQueueJob(t, s, "secret-boost", true, true)
	ordinary := addTestQueueJob(t, s, "secret-ordinary", false, false)
	var mu sync.Mutex
	var order []string
	a.queueExecutor = func(ctx context.Context, j queueJob, threads int, boost bool) (*fileReport, error) {
		mu.Lock()
		order = append(order, j.ID)
		mu.Unlock()
		if boost {
			return nil, fmt.Errorf("sensitive ffmpeg path %s", j.Input)
		}
		return &fileReport{inBytes: 100, outBytes: 25, took: time.Second}, nil
	}
	if err := a.workQueue(s, queueRunOptions{}); err != nil {
		t.Fatal(err)
	}
	values := control.snapshot()
	if len(values) != 2 || values[0] != 100 || values[1] != 20 {
		t.Fatal("limits not restored", values)
	}
	if len(order) != 2 || order[0] != boost.ID || order[1] != ordinary.ID {
		t.Fatal(order)
	}
	q, err := s.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if q.Stats.Files != 1 || q.Stats.SavedBytes != 75 || q.Stats.FailedAttempts != 1 {
		t.Fatalf("stats: %+v", q.Stats)
	}
	for _, j := range q.Jobs {
		if j.ID == ordinary.ID && (j.Input != "" || j.Output != "" || j.Targets != nil || j.Temp != "") {
			t.Fatalf("completed path retained: %+v", j)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(s.dir, "queue.json"))
	if strings.Contains(string(raw), "secret-ordinary") || strings.Contains(string(raw), "sensitive ffmpeg") {
		t.Fatal("sensitive history retained", string(raw))
	}
	stats, _ := json.Marshal(q.Stats)
	if strings.Contains(string(stats), "secret") || strings.Contains(string(stats), s.dir) {
		t.Fatal("stats retained a path")
	}
}
func TestQueueInterruptedJobRestartsAndCleansOldTemp(t *testing.T) {
	a, s := queueTestApp(t)
	a.cpuFactory = func() cpuController { return threadCPUControl{} }
	job := addTestQueueJob(t, s, "interrupted", false, false)
	var temp string
	if err := s.update(a.ctx, func(q *queueState) error {
		j := &q.Jobs[0]
		j.State = "running"
		j.Attempts = 1
		j.Output = filepath.Join(s.dir, "output.mp3")
		j.Temp, _ = queueTemp(j.Output, j.ID)
		temp = j.Temp
		return os.WriteFile(temp, []byte("partial"), 0600)
	}); err != nil {
		t.Fatal(err)
	}
	called := false
	a.queueExecutor = func(ctx context.Context, j queueJob, _ int, _ bool) (*fileReport, error) {
		called = true
		if j.ID != job.ID || fileExists(temp) || j.Attempts != 2 {
			t.Errorf("incorrect restart: %+v old temp=%t", j, fileExists(temp))
		}
		return &fileReport{inBytes: 10, outBytes: 3}, nil
	}
	if err := a.workQueue(s, queueRunOptions{}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("job was not redone")
	}
	q, _ := s.snapshot(a.ctx)
	if q.Stats.Files != 1 || q.Jobs[0].Attempts != 2 {
		t.Fatal(q)
	}
}
func TestQueueStopCancelsOnlyUnfinishedWorkAndCanRestart(t *testing.T) {
	a, s := queueTestApp(t)
	a.cpuFactory = func() cpuController { return threadCPUControl{} }
	addTestQueueJob(t, s, "stop-test", false, false)
	started := make(chan struct{})
	a.queueExecutor = func(ctx context.Context, _ queueJob, _ int, _ bool) (*fileReport, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	done := make(chan error, 1)
	go func() { done <- a.workQueue(s, queueRunOptions{}) }()
	<-started
	if err := s.update(a.ctx, func(q *queueState) error { q.Paused = true; return nil }); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
	q, _ := s.snapshot(a.ctx)
	if q.Jobs[0].State != "queued" || q.Stats.Files != 0 || !q.Paused {
		t.Fatal(q)
	}
	if err := s.update(a.ctx, func(q *queueState) error { q.Paused = false; return nil }); err != nil {
		t.Fatal(err)
	}
	a.queueExecutor = func(context.Context, queueJob, int, bool) (*fileReport, error) {
		return &fileReport{inBytes: 10, outBytes: 5}, nil
	}
	if err := a.workQueue(s, queueRunOptions{}); err != nil {
		t.Fatal(err)
	}
	q, _ = s.snapshot(a.ctx)
	if q.Jobs[0].Attempts != 2 || q.Stats.Files != 1 {
		t.Fatal(q)
	}
}

func TestQueueLateCancellationPreservesPublishedCompletion(t *testing.T) {
	a, s := queueTestApp(t)
	a.cpuFactory = func() cpuController { return threadCPUControl{} }
	job := addTestQueueJob(t, s, "late-cancel", false, false)
	var output string
	a.queueExecutor = func(_ context.Context, j queueJob, _ int, _ bool) (*fileReport, error) {
		output = j.Output
		if err := os.WriteFile(output, []byte("encoded"), 0600); err != nil {
			return nil, err
		}
		// Deterministically cancel between publication and the worker's next
		// outcome commit, without relying on scheduler timing.
		if err := s.update(a.ctx, func(q *queueState) error {
			q.Jobs[0].Cancel = true
			return nil
		}); err != nil {
			return nil, err
		}
		return &fileReport{out: output, operation: "compress", inBytes: 100, outBytes: 7, took: time.Second}, nil
	}
	if err := a.workQueue(s, queueRunOptions{}); err != nil {
		t.Fatal(err)
	}
	q, err := s.snapshot(a.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Jobs) != 1 || q.Jobs[0].ID != job.ID || q.Jobs[0].State != "completed" || !fileExists(output) {
		t.Fatalf("published completion lost: %+v output exists=%t", q.Jobs, fileExists(output))
	}
	if q.Stats.Files != 1 || q.Stats.SavedBytes != 93 || q.Stats.ProcessingMS != 1000 || q.Stats.Types[".wav"].Files != 1 {
		t.Fatalf("published result not counted: %+v", q.Stats)
	}
	if q.Jobs[0].Input != "" || q.Jobs[0].Output != "" || q.Jobs[0].Cancel {
		t.Fatalf("finished job was not scrubbed: %+v", q.Jobs[0])
	}
}

func TestQueueCancellationBeforePublicationRemovesPartial(t *testing.T) {
	for _, resultErr := range []error{errCancelled, context.Canceled, errors.New("encoder stopped")} {
		t.Run(resultErr.Error(), func(t *testing.T) {
			q := freshQueue()
			output := filepath.Join(t.TempDir(), "output.mp3")
			partial, err := queueTemp(output, "j-000001")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(partial, []byte("partial"), 0600); err != nil {
				t.Fatal(err)
			}
			q.Jobs = []queueJob{{ID: "j-000001", State: "running", Input: "source.wav", Output: output, Temp: partial, Cancel: true}}
			recordQueueOutcome(&q, queueOutcome{id: "j-000001", err: resultErr})
			if q.Jobs[0].State != "cancelled" || q.Stats.Files != 0 || q.Stats.FailedAttempts != 0 || fileExists(partial) || fileExists(output) {
				t.Fatalf("cancelled partial was retained or counted: %+v", q)
			}
			if q.Jobs[0].Input != "" || q.Jobs[0].Temp != "" {
				t.Fatalf("cancelled paths retained: %+v", q.Jobs[0])
			}
		})
	}
}

func TestQueueParallelJobs(t *testing.T) {
	if queueCPUs() < 2 {
		t.Skip("parallel capacity requires two logical CPUs")
	}
	a, s := queueTestApp(t)
	a.cpuFactory = func() cpuController { return threadCPUControl{} }
	for i := 0; i < 2; i++ {
		addTestQueueJob(t, s, fmt.Sprintf("parallel-%d", i), false, false)
	}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	a.queueExecutor = func(ctx context.Context, _ queueJob, _ int, _ bool) (*fileReport, error) {
		started <- struct{}{}
		select {
		case <-release:
			return &fileReport{inBytes: 100, outBytes: 50}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	done := make(chan error, 1)
	go func() { done <- a.workQueue(s, queueRunOptions{CPU: 100, Jobs: 2}) }()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("jobs did not run concurrently")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	q, _ := s.snapshot(a.ctx)
	if q.Stats.Files != 2 {
		t.Fatal(q.Stats)
	}
}

func TestQueueDerivedOutputsDoNotInflateCompressionSavings(t *testing.T) {
	q := freshQueue()
	q.Jobs = []queueJob{{ID: "extract", State: "running", Input: "private-video.mp4"}, {ID: "compress", State: "running", Input: "private-audio.wav"}}
	recordQueueOutcome(&q, queueOutcome{id: "extract", report: &fileReport{operation: "extract_audio", inBytes: 1000, outBytes: 50}})
	recordQueueOutcome(&q, queueOutcome{id: "compress", report: &fileReport{operation: "compress", inBytes: 100, outBytes: 50}})
	if q.Stats.Files != 2 || q.Stats.DerivedFiles != 1 || q.Stats.CompressedFiles != 1 || q.Stats.SavedBytes != 50 || q.Stats.InputBytes != 1100 {
		t.Fatalf("misleading savings: %+v", q.Stats)
	}
	if q.Stats.Types[".mp4"].SavedBytes != 0 || q.Stats.Types[".wav"].SavedBytes != 50 {
		t.Fatal(q.Stats.Types)
	}
}

func TestQueueFlagNamesDoNotAccidentallyAllowDryRun(t *testing.T) {
	for _, args := range [][]string{{"que", "file.wav", "-n", "-m"}, {"que", "status", "--next", "-m"}, {"setting", "--boost", "-m"}, {"stats", "--cpu", "20%", "-m"}} {
		var out, stderr bytes.Buffer
		a := newApplication(context.Background(), &out, &stderr)
		a.dataDir = t.TempDir()
		if code := a.run(args); code != 1 {
			t.Fatalf("unrelated flags accepted: %v code %d", args, code)
		}
		if fileExists(filepath.Join(a.dataDir, "queue.json")) {
			t.Fatal("invalid command mutated queue state")
		}
	}
}
