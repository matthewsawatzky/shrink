package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type linuxCPUControl struct {
	dir, parent string
	capacity    float64
}

func newCPUController() cpuController {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return threadCPUControl{}
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "0::") {
			continue
		}
		path := strings.TrimPrefix(line, "0::")
		parent := filepath.Join("/sys/fs/cgroup", filepath.Clean("/"+path))
		// filepath.Join handles the relative cgroup path under the mounted hierarchy.
		dir, err := os.MkdirTemp(parent, "shrink-que-")
		if err != nil {
			return threadCPUControl{}
		}
		c := linuxCPUControl{dir: dir, parent: parent, capacity: queuePlatformCapacity()}
		if err = c.SetPercent(20); err != nil {
			os.Remove(dir)
			return threadCPUControl{}
		}
		if err = os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(fmt.Sprint(os.Getpid())), 0600); err != nil {
			os.Remove(dir)
			return threadCPUControl{}
		}
		return c
	}
	return threadCPUControl{}
}
func (c linuxCPUControl) SetPercent(percent int) error {
	quota := max(1, int(c.capacity*float64(percent)*1000))
	return os.WriteFile(filepath.Join(c.dir, "cpu.max"), []byte(fmt.Sprintf("%d 100000", quota)), 0600)
}
func (c linuxCPUControl) Close() {
	_ = os.WriteFile(filepath.Join(c.parent, "cgroup.procs"), []byte(fmt.Sprint(os.Getpid())), 0600)
	_ = os.Remove(c.dir)
}
func (linuxCPUControl) Mode() string { return "native cgroup CPU cap" }

// Honour inherited container/user quotas as well as the logical CPU count.
// Ignore our own temporary control group when recomputing scheduling capacity.
func queuePlatformCapacity() float64 {
	capacity := float64(runtime.NumCPU())
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return capacity
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "0::") {
			continue
		}
		dir := filepath.Join("/sys/fs/cgroup", filepath.Clean("/"+strings.TrimPrefix(line, "0::")))
		for {
			if !strings.HasPrefix(filepath.Base(dir), "shrink-que-") {
				if raw, err := os.ReadFile(filepath.Join(dir, "cpu.max")); err == nil {
					parts := strings.Fields(string(raw))
					if len(parts) == 2 && parts[0] != "max" {
						quota, e1 := strconv.ParseFloat(parts[0], 64)
						period, e2 := strconv.ParseFloat(parts[1], 64)
						if e1 == nil && e2 == nil && quota > 0 && period > 0 {
							capacity = min(capacity, quota/period)
						}
					}
				}
			}
			if dir == "/sys/fs/cgroup" || filepath.Dir(dir) == dir {
				break
			}
			dir = filepath.Dir(dir)
		}
	}
	return capacity
}
