//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func tryFileLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}
func unlockFile(f *os.File)      { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
func detachWorker(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func lowerQueuePriority(pid int) { _ = syscall.Setpriority(syscall.PRIO_PROCESS, pid, 10) }
