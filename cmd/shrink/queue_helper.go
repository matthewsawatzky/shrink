package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The helper watches a private pipe owned solely by the worker. If the worker
// is killed (including SIGKILL), EOF cancels ffmpeg instead of orphaning an
// encoder. No PID-based signalling is used to recover old queue processes.
func (a *application) encodingCommand(args []string) (*exec.Cmd, func(), error) {
	if !a.queued {
		return a.command("ffmpeg", args...), func() {}, nil
	}
	exe := a.workerExecutable
	var err error
	if exe == "" {
		exe, err = os.Executable()
		if err != nil {
			return nil, nil, err
		}
	}
	read, write, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	cmd := a.command(exe, append([]string{"_queue-ffmpeg"}, args...)...)
	cmd.Stdin = read
	// Let the helper stop and reap its encoder before exiting. Killing the
	// helper directly (as CommandContext does on Windows) orphans ffmpeg.
	cmd.Cancel = func() error {
		_ = write.Close()
		return nil
	}
	cmd.Env = os.Environ()
	if a.lowPriority {
		cmd.Env = append(cmd.Env, "SHRINK_QUEUE_PRIORITY=low")
	} else {
		cmd.Env = append(cmd.Env, "SHRINK_QUEUE_PRIORITY=normal")
	}
	cmd.WaitDelay = 4 * time.Second
	return cmd, func() { read.Close(); write.Close() }, nil
}
func queueFFmpegHelper(parent context.Context, args []string) int {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); cancel() }()
	app := newApplication(ctx, io.Discard, io.Discard)
	cmd := app.command("ffmpeg", args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	err := cmd.Start()
	if err == nil {
		if os.Getenv("SHRINK_QUEUE_PRIORITY") == "low" {
			lowerQueuePriority(cmd.Process.Pid)
		}
		err = cmd.Wait()
	}
	if ctx.Err() != nil && len(args) > 0 {
		path := args[len(args)-1]
		if strings.HasPrefix(filepath.Base(path), ".shrink-que-") {
			_ = os.Remove(path)
		}
	}
	if err == nil {
		return 0
	}
	if e, ok := err.(*exec.ExitError); ok && e.ExitCode() > 0 {
		return e.ExitCode()
	}
	return 1
}
