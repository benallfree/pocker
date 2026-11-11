package pocker

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"time"

	"github.com/pockethost/pocketbase/ext/sandbox"
)

// MemoryWatch guards the shared heap against JS that grows memory faster than a request would. goja has no per-VM
// heap accounting, so the watch works on the process heap and on who is running JS when it climbs:
//
//   - every Interval it reads the live heap (runtime/metrics);
//   - above SoftLimit and still growing, it interrupts JS calls older than MinRunAge and strikes their instances;
//   - above HardLimit it interrupts every JS call, whatever its age;
//   - after an interrupt it asks the GC to return memory.
//
// One-shot large allocations are refused up front by the jsvm sandbox (MaxAllocation); this catches gradual growth.
type MemoryWatch struct {
	SoftLimit uint64        // bytes of live heap
	HardLimit uint64        // bytes of live heap (default SoftLimit * 1.4)
	Interval  time.Duration // default 50ms
	MinRunAge time.Duration // default 100ms
	OnStrike  func(instanceID string)
	Logger    *slog.Logger
}

const heapMetric = "/memory/classes/heap/objects:bytes"

func liveHeap() uint64 {
	s := []metrics.Sample{{Name: heapMetric}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return s[0].Value.Uint64()
}

// Run samples until ctx is done.
func (w *MemoryWatch) Run(ctx context.Context) {
	if w.SoftLimit == 0 {
		return
	}
	interval := w.Interval
	if interval <= 0 {
		interval = 50 * time.Millisecond
	}
	minAge := w.MinRunAge
	if minAge <= 0 {
		minAge = 100 * time.Millisecond
	}
	hard := w.HardLimit
	if hard == 0 {
		hard = w.SoftLimit + w.SoftLimit*2/5
	}
	log := w.Logger
	if log == nil {
		log = slog.Default()
	}

	t := time.NewTicker(interval)
	defer t.Stop()
	var last uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		heap := liveHeap()
		var owners []string
		switch {
		case heap >= hard:
			owners = sandbox.InterruptActive(0, fmt.Sprintf("memory: process heap over %d MiB", hard>>20))
		case heap >= w.SoftLimit && heap > last:
			owners = sandbox.InterruptActive(minAge, fmt.Sprintf("memory: process heap over %d MiB", w.SoftLimit>>20))
		}
		last = heap
		if len(owners) == 0 {
			continue
		}
		log.Warn("pocker: memory watch interrupted JS", "heap_mib", heap>>20, "instances", owners)
		for _, id := range owners {
			if w.OnStrike != nil {
				w.OnStrike(id)
			}
		}
		go func() {
			runtime.GC()
			debug.FreeOSMemory()
		}()
	}
}
