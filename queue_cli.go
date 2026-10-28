package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *application) runQueue(c cliRequest) {
	s, err := a.queueStore()
	if err != nil {
		a.exitError("%v", err)
	}
	command := "status"
	if len(c.args) > 0 {
		command = c.args[0]
	}
	switch command {
	case "_worker":
		a.validateFlags(c, "queue worker", "cpu,j")
		opts := queueRunOptions{CPU: c.cpu}
		if c.flags["j"] {
			opts.Jobs = c.jobs
		}
		if opts.Jobs > 256 {
			a.exitError("queue jobs must be between 1 and 256")
		}
		if err := a.workQueue(s, opts); err != nil && err != errQueueBusy {
			a.exitError("%v", err)
		}
	case "start":
		a.validateFlags(c, "que start", "m,cpu,j")
		if len(c.args) != 1 {
			a.exitError("que start takes no file arguments")
		}
		running, err := s.running()
		if err != nil {
			a.exitError("%v", err)
		}
		if running && (c.cpu != 0 || c.flags["j"]) {
			a.exitError("stop the current worker before changing its temporary limits")
		}
		opts := queueRunOptions{CPU: c.cpu}
		if c.flags["j"] {
			opts.Jobs = c.jobs
		}
		if opts.Jobs > 256 {
			a.exitError("queue jobs must be between 1 and 256")
		}
		if err = s.update(a.ctx, func(q *queueState) error { q.Paused = false; return nil }); err != nil {
			a.exitError("%v", err)
		}
		started, err := a.startQueueWorker(s, opts)
		if err != nil {
			a.exitError("could not start worker: %v", err)
		}
		a.queueMessage(map[string]any{"started": started}, fmt.Sprintf("queue worker started: %t", started))
	case "stop":
		a.validateFlags(c, "que stop", "m")
		if len(c.args) != 1 {
			a.exitError("que stop takes no arguments")
		}
		err = s.update(a.ctx, func(q *queueState) error { q.Paused = true; return nil })
		if err != nil {
			a.exitError("%v", err)
		}
		a.queueMessage(map[string]any{"paused": true}, "queue stopped; unfinished jobs will restart from the beginning on que start")
	case "status", "list":
		a.validateFlags(c, "que status", "m")
		if len(c.args) > 1 {
			a.exitError("que status takes no arguments")
		}
		q, err := s.snapshot(a.ctx)
		if err != nil {
			a.exitError("%v", err)
		}
		running, err := s.running()
		if err != nil {
			a.exitError("%v", err)
		}
		if !running {
			q.Worker = queueWorker{}
			for i := range q.Jobs {
				if q.Jobs[i].State == "running" {
					q.Jobs[i].State = "interrupted"
				}
			}
		}
		if a.machine {
			a.printJSON(struct {
				Running bool `json:"running"`
				queueState
			}{running, q})
			return
		}
		fmt.Fprintf(a.stdout, "queue: running=%t stopped=%t; default CPU=%d%% jobs=%d\n", running, q.Paused, q.Settings.CPU, q.Settings.Jobs)
		if running {
			fmt.Fprintf(a.stdout, "CPU: %d%% (%s); active-job limit %d, threads/job %d\n", q.Worker.CPU, q.Worker.Control, q.Worker.Jobs, q.Worker.Threads)
		}
		for _, j := range q.Jobs {
			detail := j.Input
			if j.Next {
				detail += " [next]"
			}
			if j.Boost {
				detail += " [boost: 100%, exclusive]"
			} else if j.CPU > 0 {
				detail += fmt.Sprintf(" [CPU %d%%]", j.CPU)
			}
			if j.Note != "" {
				detail += " - " + j.Note
			}
			fmt.Fprintf(a.stdout, "%s  %-11s %s\n", j.ID, j.State, detail)
		}
	case "retry":
		a.validateFlags(c, "que retry", "m")
		if len(c.args) != 2 {
			a.exitError("usage: shrink que retry <job-id|all>")
		}
		err = s.update(a.ctx, func(q *queueState) error {
			count := 0
			for i := range q.Jobs {
				j := &q.Jobs[i]
				if j.State == "failed" && (c.args[1] == "all" || c.args[1] == j.ID) {
					j.State = "queued"
					j.Note = ""
					count++
				}
			}
			if count == 0 {
				return fmt.Errorf("no matching failed jobs")
			}
			return nil
		})
		if err != nil {
			a.exitError("%v", err)
		}
		workerErr := a.autoStartQueue(s)
		response := map[string]any{"retried": true}
		text := "failed jobs queued for retry"
		if workerErr != "" {
			response["worker_error"] = workerErr
			text += "; worker not started: " + workerErr
		}
		a.queueMessage(response, text)
	case "cancel":
		a.validateFlags(c, "que cancel", "m")
		if len(c.args) != 2 {
			a.exitError("usage: shrink que cancel <job-id>")
		}
		err = s.update(a.ctx, func(q *queueState) error {
			for i := range q.Jobs {
				j := &q.Jobs[i]
				if j.ID != c.args[1] {
					continue
				}
				if j.State == "completed" || j.State == "cancelled" {
					return fmt.Errorf("job has already finished")
				}
				if j.State == "running" {
					j.Cancel = true
				} else {
					scrubJob(j, "cancelled", "")
				}
				return nil
			}
			return fmt.Errorf("job not found")
		})
		if err != nil {
			a.exitError("%v", err)
		}
		a.queueMessage(map[string]bool{"cancel_requested": true}, "job cancellation requested")
	case "clear":
		a.validateFlags(c, "que clear", "m")
		if len(c.args) != 1 {
			a.exitError("que clear takes no arguments")
		}
		running, err := s.running()
		if err != nil || running {
			a.exitError("stop the worker and wait for it to exit before clearing the queue")
		}
		err = s.update(a.ctx, func(q *queueState) error {
			busy, err := s.running()
			if err != nil {
				return err
			}
			if busy {
				return fmt.Errorf("worker started; stop it before clearing the queue")
			}
			for _, j := range q.Jobs {
				cleanQueueTemp(j)
			}
			q.Jobs = []queueJob{}
			return nil
		})
		if err != nil {
			a.exitError("%v", err)
		}
		a.queueMessage(map[string]bool{"cleared": true}, "queue cleared; aggregate stats kept")
	default:
		a.validateFlags(c, "queue file", "m,f,d,o,next,boost,cpu")
		if len(c.args) == 0 {
			a.exitError("usage: shrink que <file> [targets]")
		}
		if c.boost && c.cpu != 0 {
			a.exitError("--boost already uses 100%% CPU; omit --cpu")
		}
		options, err := parseMediaTargets(c.args[1:])
		if err != nil {
			a.exitError("%v", err)
		}
		path, err := filepath.Abs(c.args[0])
		if err != nil {
			a.exitError("%v", err)
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			a.exitError("cannot queue file: %v", err)
		}
		stat, err := os.Stat(path)
		if err != nil || !stat.Mode().IsRegular() {
			a.exitError("queue input must be a regular file")
		}
		if detectKind(path) == "unknown" {
			a.exitError("unsupported queue input type")
		}
		if err = checkKindTargets(detectKind(path), options); err != nil {
			a.exitError("%v", err)
		}
		var id string
		err = s.update(a.ctx, func(q *queueState) error {
			for _, j := range q.Jobs {
				if j.State == "running" && sameQueuePath(j.Output, path) {
					return fmt.Errorf("file is the destination of a running job")
				}
			}
			current, err := os.Stat(path)
			if err != nil || !current.Mode().IsRegular() || current.Size() != stat.Size() || current.ModTime().UnixNano() != stat.ModTime().UnixNano() {
				return fmt.Errorf("source changed or disappeared before it could be queued")
			}
			q.Sequence++
			id = fmt.Sprintf("j-%06d", q.Sequence)
			q.Jobs = append(q.Jobs, queueJob{ID: id, State: "queued", Input: path, Targets: append([]string(nil), c.args[1:]...), Size: stat.Size(), Modified: stat.ModTime().UnixNano(), CPU: c.cpu, Next: c.next, Boost: c.boost, Fast: a.fast, Delete: a.deleteAfter, Overwrite: a.overwrite, Created: time.Now().UTC()})
			return nil
		})
		if err != nil {
			a.exitError("%v", err)
		}
		workerErr := a.autoStartQueue(s)
		response := map[string]any{"id": id, "queued": true}
		if workerErr != "" {
			response["worker_error"] = workerErr
		}
		text := "queued " + id
		if workerErr != "" {
			text += "; worker not started: " + workerErr
		}
		a.queueMessage(response, text)
	}
}
func (a *application) autoStartQueue(s queueStore) string {
	q, err := s.snapshot(a.ctx)
	if err != nil {
		return err.Error()
	}
	if q.Paused {
		return ""
	}
	_, err = a.startQueueWorker(s, queueRunOptions{})
	if err != nil {
		return err.Error()
	}
	return ""
}
func (a *application) queueMessage(value any, text string) {
	if a.machine {
		a.printJSON(value)
	} else {
		fmt.Fprintln(a.stdout, text)
	}
}
func (a *application) runSettings(c cliRequest) {
	a.validateFlags(c, "setting", "m,cpu,j")
	if len(c.args) > 0 {
		a.exitError("usage: shrink setting [--cpu 20%%] [--jobs 1]")
	}
	if c.flags["j"] && c.jobs > 256 {
		a.exitError("queue jobs must be between 1 and 256")
	}
	s, err := a.queueStore()
	if err != nil {
		a.exitError("%v", err)
	}
	err = s.update(a.ctx, func(q *queueState) error {
		if c.cpu > 0 {
			q.Settings.CPU = c.cpu
		}
		if c.flags["j"] {
			q.Settings.Jobs = c.jobs
		}
		return nil
	})
	if err != nil {
		a.exitError("%v", err)
	}
	q, err := s.snapshot(a.ctx)
	if err != nil {
		a.exitError("%v", err)
	}
	a.queueMessage(q.Settings, fmt.Sprintf("queue defaults: CPU %d%%, jobs %d (foreground commands unchanged)", q.Settings.CPU, q.Settings.Jobs))
}
func (a *application) runStats(c cliRequest) {
	a.validateFlags(c, "stats", "m")
	if len(c.args) > 0 {
		a.exitError("stats takes no arguments")
	}
	s, err := a.queueStore()
	if err != nil {
		a.exitError("%v", err)
	}
	q, err := s.snapshot(a.ctx)
	if err != nil {
		a.exitError("%v", err)
	}
	if a.machine {
		a.printJSON(q.Stats)
		return
	}
	fmt.Fprintf(a.stdout, "compression stats (foreground + queue): %d files, %s -> %s, net same-kind reduction %s; processing time %s\n", q.Stats.Files, a.humanSize(q.Stats.InputBytes), a.humanSize(q.Stats.OutputBytes), a.humanSize(q.Stats.SavedBytes), time.Duration(q.Stats.ProcessingMS)*time.Millisecond)
	for _, kind := range sortedKeys(q.Stats.Types) {
		v := q.Stats.Types[kind]
		fmt.Fprintf(a.stdout, "  %s: %d files\n", strings.TrimPrefix(kind, "."), v.Files)
	}
}
