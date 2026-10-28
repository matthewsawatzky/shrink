package main

import (
	"fmt"
	"strconv"
	"strings"
)

type cpuController interface {
	SetPercent(int) error
	Close()
	Mode() string
}
type threadCPUControl struct{}

func (threadCPUControl) SetPercent(int) error { return nil }
func (threadCPUControl) Close()               {}
func (threadCPUControl) Mode() string         { return "approximate thread budget" }
func parseCPU(s string) (int, error) {
	if !strings.HasSuffix(s, "%") {
		return 0, fmt.Errorf("CPU must be a percentage from 1%% to 100%%")
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, "%"))
	if err != nil || n < 1 || n > 100 {
		return 0, fmt.Errorf("CPU must be a percentage from 1%% to 100%%")
	}
	return n, nil
}
func queueCapacity(cpus, percent, jobs int) (slots, threads int) {
	budget := max(1, cpus*percent/100)
	slots = min(jobs, budget)
	slots = max(1, slots)
	return slots, max(1, budget/slots)
}

// Thread limits cover decoding, filters and encoding. Codec-specific worker
// pools need their own controls; a fallback remains approximate, not a quota.
func queueThreadArgs(args []string, threads int) []string {
	if threads < 1 {
		return append([]string(nil), args...)
	}
	n := strconv.Itoa(threads)
	out := []string{"-filter_threads", n, "-filter_complex_threads", n}
	for _, arg := range args {
		if arg == "-i" {
			out = append(out, "-threads", n)
		}
		out = append(out, arg)
	}
	out = append(out, "-threads", n)
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "-c:v" {
			continue
		}
		switch args[i+1] {
		case "libx265":
			out = append(out, "-x265-params", "pools="+n+":frame-threads=1")
		case "libsvtav1":
			out = append(out, "-svtav1-params", "lp="+n)
		}
	}
	return out
}
func queueCPUs() int { return max(1, int(queuePlatformCapacity())) }
