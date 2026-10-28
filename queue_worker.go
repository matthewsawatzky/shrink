package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type queueRunOptions struct{ CPU, Jobs int }
type activeQueueJob struct {
	boost  bool
	input  string
	cancel context.CancelFunc
}
type queueOutcome struct {
	id     string
	job    queueJob
	report *fileReport
	err    error
}

func sameQueuePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) || isWindows() && strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) {
		return true
	}
	first, err1 := os.Stat(a)
	second, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(first, second)
}
func (a *application) startQueueWorker(s queueStore, options queueRunOptions) (bool, error) {
	running, err := s.running()
	if err != nil || running {
		return false, err
	}
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			return false, fmt.Errorf("%s is not on PATH", tool)
		}
	}
	exe := a.workerExecutable
	if exe == "" {
		exe, err = os.Executable()
		if err != nil {
			return false, err
		}
	}
	args := []string{"que", "_worker"}
	if options.CPU != 0 {
		args = append(args, "--cpu", fmt.Sprintf("%d%%", options.CPU))
	}
	if options.Jobs != 0 {
		args = append(args, "--jobs", fmt.Sprint(options.Jobs))
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "SHRINK_DATA_DIR="+s.dir)
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer null.Close()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
	detachWorker(cmd)
	if err = cmd.Start(); err != nil {
		return false, err
	}
	return true, cmd.Process.Release()
}

func queueBudget(j queueJob, settings queueSettings, opts queueRunOptions) (cpu, jobs int) {
	cpu, jobs = settings.CPU, settings.Jobs
	if opts.CPU > 0 {
		cpu = opts.CPU
	}
	if opts.Jobs > 0 {
		jobs = opts.Jobs
	}
	if j.CPU > 0 {
		cpu = j.CPU
	}
	if j.Boost {
		cpu, jobs = 100, 1
	}
	return
}

// selectQueueJob is deterministic. A waiting boost forms a barrier: no new
// ordinary work starts while existing jobs drain. Other jobs never get killed
// merely to promote or boost a waiting job.
func selectQueueJob(q *queueState, active map[string]activeQueueJob, opts queueRunOptions, currentCPU int) (index, cpu, slots, threads int) {
	for _, run := range active {
		if run.boost {
			return -1, 0, 0, 0
		}
	}
	order := make([]int, 0, len(q.Jobs))
	for pass := 0; pass < 2; pass++ {
		for i, j := range q.Jobs {
			if j.State == "queued" && j.Next == (pass == 0) {
				order = append(order, i)
			}
		}
	}
	for _, i := range order {
		j := q.Jobs[i]
		desired, jobs := queueBudget(j, q.Settings, opts)
		count, n := queueCapacity(queueCPUs(), desired, jobs)
		if j.Boost {
			if len(active) > 0 {
				return -1, 0, 0, 0
			}
			return i, 100, 1, max(1, queueCPUs())
		}
		if len(active) > 0 && (desired != currentCPU || len(active) >= count) {
			return -1, 0, 0, 0
		}
		busy := false
		for _, run := range active {
			if sameQueuePath(run.input, j.Input) {
				busy = true
			}
		}
		if busy {
			continue
		}
		return i, desired, count, n
	}
	return -1, 0, 0, 0
}

func queueOutput(q *queueState, j queueJob, candidate string) string {
	stem, ext := strings.TrimSuffix(candidate, filepath.Ext(candidate)), filepath.Ext(candidate)
	for n := 0; ; n++ {
		path := candidate
		if n > 0 {
			path = fmt.Sprintf("%s-%d%s", stem, n, ext)
		}
		busy := false
		for _, other := range q.Jobs {
			if other.ID == j.ID {
				continue
			}
			if other.State == "running" && sameQueuePath(other.Output, path) {
				busy = true
			}
			if other.Input != "" && sameQueuePath(other.Input, path) {
				busy = true
			}
		}
		if !busy && (j.Overwrite || !fileExists(path)) {
			return path
		}
	}
}
func queueTemp(output, id string) (string, error) {
	var token [12]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(output), ".shrink-que-"+id+"-"+hex.EncodeToString(token[:])+filepath.Ext(output)), nil
}
func cleanQueueTemp(j queueJob) {
	// No untrusted stored path can cause deletion outside the output directory.
	if j.Temp != "" && filepath.Dir(j.Temp) == filepath.Dir(j.Output) && strings.HasPrefix(filepath.Base(j.Temp), ".shrink-que-"+j.ID+"-") {
		_ = os.Remove(j.Temp)
	}
}
func (a *application) workQueue(s queueStore, opts queueRunOptions) error {
	owner, err := s.lock(a.ctx, "worker.lock", false)
	if err != nil {
		return err
	}
	defer func() { releaseFileLock(owner) }()
	controller := a.cpuFactory
	if controller == nil {
		controller = newCPUController
	}
	control := controller()
	defer control.Close()
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	if err = s.update(ctx, func(q *queueState) error {
		for i := range q.Jobs {
			j := &q.Jobs[i]
			if j.State == "running" {
				cleanQueueTemp(*j)
				j.State = "queued"
				j.Temp = ""
				j.Output = ""
				j.Note = "interrupted; will restart from the beginning"
			}
		}
		q.Worker = queueWorker{PID: os.Getpid(), Control: control.Mode()}
		return nil
	}); err != nil {
		return err
	}
	active := map[string]activeQueueJob{}
	outcomes := make(chan queueOutcome, 256)
	currentCPU := 0
	// Always join every job before releasing worker ownership, even on a store
	// error. Lifeline helpers also stop their ffmpeg if this worker is killed.
	defer func() {
		cancel()
		for len(active) > 0 {
			result := <-outcomes
			delete(active, result.id)
		}
	}()
	ticker := time.NewTicker(75 * time.Millisecond)
	defer ticker.Stop()
	for {
		var completed *queueOutcome
		select {
		case result := <-outcomes:
			completed = &result
			delete(active, result.id)
		default:
		}
		done := false
		type launchJob struct {
			id  string
			run func()
		}
		var pending []launchJob
		err = s.update(context.WithoutCancel(ctx), func(q *queueState) error {
			if completed != nil {
				recordQueueOutcome(q, *completed)
			}
			if ctx.Err() != nil {
				q.Paused = true
			}
			for id, run := range active {
				for _, j := range q.Jobs {
					if j.ID == id && (q.Paused || j.Cancel) {
						run.cancel()
					}
				}
			}
			if q.Paused && len(active) > 0 {
				return nil
			}
			for !q.Paused {
				index, cpu, slots, threads := selectQueueJob(q, active, opts, currentCPU)
				if index < 0 {
					break
				}
				if cpu != currentCPU {
					if err := control.SetPercent(cpu); err != nil {
						return fmt.Errorf("could not apply CPU budget: %w", err)
					}
					currentCPU = cpu
				}
				j := &q.Jobs[index]
				o, err := parseMediaTargets(j.Targets)
				if err != nil {
					j.State = "failed"
					j.Note = "invalid saved targets"
					q.Stats.FailedAttempts++
					continue
				}
				description := a.describeJob(j.Input, detectKind(j.Input), o)
				j.Output = queueOutput(q, *j, description.output)
				j.Temp, err = queueTemp(j.Output, j.ID)
				if err != nil {
					return err
				}
				j.State = "running"
				j.Attempts++
				j.Note = ""
				jobCtx, stop := context.WithCancel(ctx)
				active[j.ID] = activeQueueJob{input: j.Input, cancel: stop, boost: j.Boost}
				job := *j
				q.Worker = queueWorker{PID: os.Getpid(), CPU: cpu, Jobs: slots, Threads: threads, Control: control.Mode()}
				// Dispatch happens only after the running state is durably committed below.
				pending = append(pending, launchJob{id: job.ID, run: func() {
					go func() { outcomes <- a.executeQueueJob(jobCtx, job, threads, cpu == 100 && job.Boost); stop() }()
				}})
			}
			if len(active) == 0 {
				done = true
			}
			trimQueueHistory(q)
			return nil
		})
		if err != nil {
			// Jobs reserved but not dispatched have no goroutine to join.
			for _, launch := range pending {
				active[launch.id].cancel()
				delete(active, launch.id)
			}
			return err
		}
		for _, launch := range pending {
			launch.run()
		}
		if completed != nil {
			a.deleteQueueOriginal(s, *completed)
		}
		if done {
			// Finish post-commit cleanup before relinquishing ownership, then recheck
			// arrivals under the state lock so enqueue/exit cannot strand new work.
			err = s.update(context.WithoutCancel(ctx), func(q *queueState) error {
				if !q.Paused {
					for _, j := range q.Jobs {
						if j.State == "queued" {
							done = false
							return nil
						}
					}
				}
				q.Worker = queueWorker{}
				releaseFileLock(owner)
				owner = nil
				return nil
			})
			if err != nil {
				return err
			}
			if done {
				return nil
			}
		}
		<-ticker.C
	}
}

func (a *application) executeQueueJob(ctx context.Context, j queueJob, threads int, boost bool) queueOutcome {
	result := queueOutcome{id: j.ID, job: j}
	child := newApplication(ctx, io.Discard, io.Discard)
	child.machine = true
	child.fast = j.Fast
	child.overwrite = j.Overwrite
	child.queued = true
	child.cpuThreads = threads
	child.lowPriority = !boost
	child.outputTemp = j.Temp
	child.workerExecutable = a.workerExecutable
	stat, err := os.Stat(j.Input)
	if err != nil || !stat.Mode().IsRegular() || stat.Size() != j.Size || stat.ModTime().UnixNano() != j.Modified {
		result.err = errQueueSourceChanged
		return result
	}
	if a.queueExecutor != nil {
		result.report, result.err = a.queueExecutor(ctx, j, threads, boost)
		return result
	}
	options, err := parseMediaTargets(j.Targets)
	if err != nil {
		result.err = err
		return result
	}
	p, err := child.planJob(j.Input, options)
	if err != nil {
		result.err = err
		return result
	}
	p.output = j.Output
	result.report, result.err = p.execute()
	if result.err == nil {
		after, err := os.Stat(j.Input)
		if err != nil || after.Size() != j.Size || after.ModTime().UnixNano() != j.Modified {
			result.err = errQueueSourceChanged
		}
	}
	return result
}
func recordQueueOutcome(q *queueState, r queueOutcome) {
	for i := range q.Jobs {
		j := &q.Jobs[i]
		if j.ID != r.id {
			continue
		}
		// A successful result has already been published. A cancellation that
		// arrives before we commit it must not discard its completion or stats.
		if j.Cancel && (r.err != nil || r.report == nil) {
			cleanQueueTemp(*j)
			scrubJob(j, "cancelled", "")
			return
		}
		if errors.Is(r.err, context.Canceled) || errors.Is(r.err, errCancelled) {
			cleanQueueTemp(*j)
			j.State = "queued"
			j.Output = ""
			j.Temp = ""
			j.Note = "interrupted; will restart from the beginning"
			return
		}
		if r.err != nil {
			cleanQueueTemp(*j)
			j.State = "failed"
			j.Output = ""
			j.Temp = ""
			j.Note = "job failed; check the source and shrink doctor before retrying"
			if errors.Is(r.err, errQueueSourceChanged) {
				j.Note = "source missing or changed; restore it or cancel and enqueue the replacement"
			}
			q.Stats.FailedAttempts++
			return
		}
		if r.report == nil {
			j.State = "failed"
			j.Note = "job produced no result"
			q.Stats.FailedAttempts++
			return
		}
		if r.report.noop {
			q.Stats.Skipped++
		} else {
			q.Stats.Files++
			q.Stats.InputBytes += r.report.inBytes
			q.Stats.OutputBytes += r.report.outBytes
			derived := r.report.operation == "extract_audio" || r.report.operation == "grab_frame"
			if derived {
				q.Stats.DerivedFiles++
			} else {
				q.Stats.CompressedFiles++
				q.Stats.CompressionInputBytes += r.report.inBytes
				q.Stats.CompressionOutputBytes += r.report.outBytes
				q.Stats.SavedBytes = q.Stats.CompressionInputBytes - q.Stats.CompressionOutputBytes
			}
			q.Stats.ProcessingMS += r.report.took.Milliseconds()
			kind := strings.ToLower(filepath.Ext(j.Input))
			v := q.Stats.Types[kind]
			v.Files++
			v.InputBytes += r.report.inBytes
			v.OutputBytes += r.report.outBytes
			if !derived {
				v.SavedBytes += r.report.inBytes - r.report.outBytes
			}
			q.Stats.Types[kind] = v
		}
		scrubJob(j, "completed", "")
		return
	}
}
func (a *application) deleteQueueOriginal(s queueStore, r queueOutcome) {
	if !r.job.Delete || r.err != nil || r.report == nil || r.report.noop || r.report.operation != "compress" && r.report.operation != "rescale" {
		return
	}
	// The job context has already been released. Verify using the worker's
	// live context, then serialize the final reference check and deletion.
	if r.report.outBytes >= r.report.inBytes || !a.replacementSafe(r.job.Input, r.report.out) {
		return
	}
	_ = s.update(a.ctx, func(q *queueState) error {
		completed := false
		for _, job := range q.Jobs {
			if job.ID == r.id && job.State == "completed" {
				completed = true
			}
		}
		if !completed {
			return nil
		}
		for _, pending := range q.Jobs {
			if pending.Input != "" && sameQueuePath(pending.Input, r.job.Input) {
				return nil
			}
		}
		stat, err := os.Stat(r.job.Input)
		if err != nil || stat.Size() != r.job.Size || stat.ModTime().UnixNano() != r.job.Modified || a.ctx.Err() != nil {
			return nil
		}
		// Completion is already durable. A crash here can leave an original, but
		// cannot leave a pending job whose original has already been deleted.
		_ = os.Remove(r.job.Input)
		return nil
	})
}

func trimQueueHistory(q *queueState) {
	kept := make([]queueJob, 0, len(q.Jobs))
	history := 0
	for i := len(q.Jobs) - 1; i >= 0; i-- {
		j := q.Jobs[i]
		if j.State == "completed" || j.State == "cancelled" {
			history++
			if history > 100 {
				continue
			}
		}
		kept = append(kept, j)
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	q.Jobs = kept
}

func isWindows() bool { return runtime.GOOS == "windows" }

var errQueueSourceChanged = errors.New("source missing or changed")
