package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// accumulateStats is shared by foreground work and durable queue completion.
// Only the source extension is retained; file paths never enter the totals.
func accumulateStats(stats *queueStats, input string, report *fileReport) {
	if report.noop {
		stats.Skipped++
		return
	}
	stats.Files++
	stats.InputBytes += report.inBytes
	stats.OutputBytes += report.outBytes
	derived := report.operation == "extract_audio" || report.operation == "grab_frame"
	if derived {
		stats.DerivedFiles++
	} else {
		stats.CompressedFiles++
		stats.CompressionInputBytes += report.inBytes
		stats.CompressionOutputBytes += report.outBytes
		stats.SavedBytes = stats.CompressionInputBytes - stats.CompressionOutputBytes
	}
	stats.ProcessingMS += report.took.Milliseconds()
	if stats.Types == nil {
		stats.Types = map[string]typeStats{}
	}
	kind := strings.ToLower(filepath.Ext(input))
	v := stats.Types[kind]
	v.Files++
	v.InputBytes += report.inBytes
	v.OutputBytes += report.outBytes
	if !derived {
		v.SavedBytes += report.inBytes - report.outBytes
	}
	stats.Types[kind] = v
}

// Foreground history is best effort: a stats problem must not turn a completed
// conversion into an encode failure. Queued work commits its own totals atomically
// with completion, so it must never use this second recording path.
func (a *application) updateForegroundStats(change func(*queueStats)) {
	if a.queued || a.dryRun {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(a.ctx), 2*time.Second)
	defer cancel()
	store, err := a.queueStore()
	if err == nil {
		err = store.update(ctx, func(q *queueState) error { change(&q.Stats); return nil })
	}
	if err != nil {
		a.statsWarning.Do(func() {
			fmt.Fprintf(a.stderr, "warning: could not save compression stats: %v\n", err)
		})
	}
}

func (a *application) recordForegroundOutcome(input string, report *fileReport, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, errCancelled) || err != nil && a.ctx.Err() != nil || err == nil && report == nil {
		return
	}
	a.updateForegroundStats(func(stats *queueStats) {
		if err != nil {
			stats.FailedAttempts++
			return
		}
		accumulateStats(stats, input, report)
	})
}
