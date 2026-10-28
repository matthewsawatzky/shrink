package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Only unfinished jobs retain paths and targets. Completion and aggregate stats
// are committed together, so a restart cannot count a completed job twice.
type queueJob struct {
	ID        string     `json:"id"`
	State     string     `json:"state"`
	Input     string     `json:"input,omitempty"`
	Targets   []string   `json:"targets,omitempty"`
	Output    string     `json:"output,omitempty"`
	Temp      string     `json:"temp,omitempty"`
	Size      int64      `json:"source_bytes,omitempty"`
	Modified  int64      `json:"source_modified_ns,omitempty"`
	CPU       int        `json:"cpu,omitempty"`
	Next      bool       `json:"next,omitempty"`
	Boost     bool       `json:"boost,omitempty"`
	Fast      bool       `json:"fast,omitempty"`
	Delete    bool       `json:"delete,omitempty"`
	Overwrite bool       `json:"overwrite,omitempty"`
	Cancel    bool       `json:"cancel_requested,omitempty"`
	Attempts  int        `json:"attempts"`
	Note      string     `json:"note,omitempty"`
	Created   time.Time  `json:"created"`
	Finished  *time.Time `json:"finished,omitempty"`
}
type queueSettings struct {
	CPU  int `json:"cpu_percent"`
	Jobs int `json:"jobs"`
}
type typeStats struct {
	SavedBytes  int64 `json:"saved_bytes"`
	Files       int64 `json:"files"`
	InputBytes  int64 `json:"input_bytes"`
	OutputBytes int64 `json:"output_bytes"`
}
type queueStats struct {
	CompressedFiles        int64                `json:"compressed_files"`
	DerivedFiles           int64                `json:"derived_files"`
	CompressionInputBytes  int64                `json:"compression_input_bytes"`
	CompressionOutputBytes int64                `json:"compression_output_bytes"`
	Files                  int64                `json:"files"`
	Skipped                int64                `json:"skipped"`
	FailedAttempts         int64                `json:"failed_attempts"`
	InputBytes             int64                `json:"input_bytes"`
	OutputBytes            int64                `json:"output_bytes"`
	SavedBytes             int64                `json:"saved_bytes"`
	ProcessingMS           int64                `json:"processing_ms"`
	Types                  map[string]typeStats `json:"file_types"`
}
type queueWorker struct {
	PID     int    `json:"pid,omitempty"`
	CPU     int    `json:"cpu_percent,omitempty"`
	Jobs    int    `json:"jobs,omitempty"`
	Threads int    `json:"threads_per_job,omitempty"`
	Control string `json:"cpu_control,omitempty"`
}
type queueState struct {
	Version  int           `json:"version"`
	Sequence uint64        `json:"sequence"`
	Paused   bool          `json:"paused"`
	Settings queueSettings `json:"settings"`
	Jobs     []queueJob    `json:"jobs"`
	Stats    queueStats    `json:"stats"`
	Worker   queueWorker   `json:"worker"`
}
type queueStore struct{ dir string }

func defaultQueueDir() (string, error) {
	if dir := os.Getenv("SHRINK_DATA_DIR"); dir != "" {
		return filepath.Abs(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "shrink"), nil
	case "windows":
		dir := os.Getenv("LOCALAPPDATA")
		if dir == "" {
			return "", fmt.Errorf("LOCALAPPDATA is not set")
		}
		return filepath.Join(dir, "shrink"), nil
	default:
		if dir := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
			return filepath.Join(dir, "shrink"), nil
		}
		return filepath.Join(home, ".local", "share", "shrink"), nil
	}
}
func (a *application) queueStore() (queueStore, error) {
	dir := a.dataDir
	if dir == "" {
		var err error
		dir, err = defaultQueueDir()
		if err != nil {
			return queueStore{}, err
		}
	}
	return queueStore{dir: dir}, nil
}
func (s queueStore) init() error { return os.MkdirAll(s.dir, 0700) }
func (s queueStore) lock(ctx context.Context, name string, wait bool) (*os.File, error) {
	if err := s.init(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, name), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		ok, err := tryFileLock(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if ok {
			return f, nil
		}
		if !wait {
			f.Close()
			return nil, errQueueBusy
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

var errQueueBusy = errors.New("queue worker is already running")

func releaseFileLock(f *os.File) {
	if f != nil {
		unlockFile(f)
		f.Close()
	}
}
func (s queueStore) running() (bool, error) {
	f, err := s.lock(context.Background(), "worker.lock", false)
	if errors.Is(err, errQueueBusy) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	releaseFileLock(f)
	return false, nil
}
func freshQueue() queueState {
	return queueState{Version: 1, Settings: queueSettings{CPU: 20, Jobs: 1}, Jobs: []queueJob{}, Stats: queueStats{Types: map[string]typeStats{}}}
}
func (s queueStore) read() (queueState, error) {
	q := freshQueue()
	stale, _ := filepath.Glob(filepath.Join(s.dir, ".queue-*"))
	for _, name := range stale {
		_ = os.Remove(name)
	}
	data, err := os.ReadFile(filepath.Join(s.dir, "queue.json"))
	if errors.Is(err, os.ErrNotExist) {
		return q, nil
	}
	if err != nil {
		return q, err
	}
	q = queueState{}
	if err = json.Unmarshal(data, &q); err != nil {
		return q, fmt.Errorf("cannot read queue data (kept unchanged): %w", err)
	}
	if q.Version != 1 || q.Settings.CPU < 1 || q.Settings.CPU > 100 || q.Settings.Jobs < 1 || q.Settings.Jobs > 256 {
		return q, fmt.Errorf("invalid or unsupported queue data (kept unchanged)")
	}
	if q.Stats.Types == nil {
		q.Stats.Types = map[string]typeStats{}
	}
	seen := map[string]bool{}
	for _, job := range q.Jobs {
		if job.ID == "" || strings.ContainsAny(job.ID, "/\\") || seen[job.ID] {
			return q, fmt.Errorf("invalid queue job IDs")
		}
		seen[job.ID] = true
		switch job.State {
		case "queued", "running", "failed", "completed", "cancelled":
		default:
			return q, fmt.Errorf("invalid job state")
		}
		if job.CPU < 0 || job.CPU > 100 {
			return q, fmt.Errorf("invalid job CPU budget")
		}
	}
	return q, nil
}
func (s queueStore) write(q queueState) error {
	data, err := json.MarshalIndent(q, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".queue-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, filepath.Join(s.dir, "queue.json")); err != nil {
		return err
	}
	if d, err := os.Open(s.dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}
func (s queueStore) update(ctx context.Context, change func(*queueState) error) error {
	f, err := s.lock(ctx, "state.lock", true)
	if err != nil {
		return err
	}
	defer releaseFileLock(f)
	q, err := s.read()
	if err != nil {
		return err
	}
	before, err := json.Marshal(q)
	if err != nil {
		return err
	}
	if err = change(&q); err != nil {
		return err
	}
	after, err := json.Marshal(q)
	if err != nil {
		return err
	}
	if bytes.Equal(before, after) {
		return nil
	}
	return s.write(q)
}
func (s queueStore) snapshot(ctx context.Context) (queueState, error) {
	f, err := s.lock(ctx, "state.lock", true)
	if err != nil {
		return queueState{}, err
	}
	defer releaseFileLock(f)
	return s.read()
}
func scrubJob(j *queueJob, state, note string) {
	now := time.Now().UTC()
	*j = queueJob{ID: j.ID, State: state, Attempts: j.Attempts, Created: j.Created, Finished: &now, Note: note}
}
