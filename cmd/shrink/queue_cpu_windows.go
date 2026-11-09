package main

import (
	"runtime"
	"syscall"
	"unsafe"
)

type windowsCPUControl struct{ handle syscall.Handle }

func newCPUController() cpuController {
	h, _, _ := queueKernel.NewProc("CreateJobObjectW").Call(0, 0)
	if h == 0 {
		return threadCPUControl{}
	}
	c := windowsCPUControl{handle: syscall.Handle(h)}
	if c.SetPercent(20) != nil {
		c.Close()
		return threadCPUControl{}
	}
	process, err := syscall.GetCurrentProcess()
	if err != nil {
		c.Close()
		return threadCPUControl{}
	}
	ok, _, _ := queueKernel.NewProc("AssignProcessToJobObject").Call(h, uintptr(process))
	if ok == 0 {
		c.Close()
		return threadCPUControl{}
	}
	return c
}
func (c windowsCPUControl) SetPercent(percent int) error {
	info := struct{ Flags, Rate uint32 }{Flags: 1 | 4, Rate: uint32(percent * 100)}
	ok, _, err := queueKernel.NewProc("SetInformationJobObject").Call(uintptr(c.handle), 15, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if ok == 0 {
		return err
	}
	return nil
}
func (c windowsCPUControl) Close()     { syscall.CloseHandle(c.handle) }
func (windowsCPUControl) Mode() string { return "native Windows Job Object CPU cap" }

func queuePlatformCapacity() float64 { return float64(runtime.NumCPU()) }
