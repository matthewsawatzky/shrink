//go:build !linux && !windows

package main

import "runtime"

func newCPUController() cpuController { return threadCPUControl{} }

func queuePlatformCapacity() float64 { return float64(runtime.NumCPU()) }
