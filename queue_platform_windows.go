package main

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

var queueKernel = syscall.NewLazyDLL("kernel32.dll")
var lockFileEx = queueKernel.NewProc("LockFileEx")
var unlockFileEx = queueKernel.NewProc("UnlockFileEx")

func tryFileLock(f *os.File) (bool, error) {
	var overlap syscall.Overlapped
	ok, _, err := lockFileEx.Call(f.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(&overlap)))
	if ok != 0 {
		return true, nil
	}
	if err == syscall.Errno(33) {
		return false, nil
	}
	return false, err
}
func unlockFile(f *os.File) {
	var o syscall.Overlapped
	unlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&o)))
}
func detachWorker(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | syscall.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
}
func lowerQueuePriority(pid int) {
	h, err := syscall.OpenProcess(0x0200, false, uint32(pid))
	if err != nil {
		return
	}
	defer syscall.CloseHandle(h)
	queueKernel.NewProc("SetPriorityClass").Call(uintptr(h), 0x00004000)
}
