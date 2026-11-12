package edge

import (
	"context"
	"sync"
	"time"
)

// TimingEntry is one Server-Timing metric (see the servertiming plugin).
type TimingEntry struct {
	Name string
	Dur  time.Duration
	Desc string
}

type timingsKey struct{}

type timings struct {
	mu  sync.Mutex
	fns []func() []TimingEntry
}

// WithTimings returns a context that collects timing entries for one request, and the function that evaluates
// them, innermost hop first (the last registered first). A plugin that reports timings (e.g. as a Server-Timing
// header) calls it at the start of a request and evaluates when the response headers go out.
func WithTimings(ctx context.Context) (context.Context, func() []TimingEntry) {
	t := &timings{}
	return context.WithValue(ctx, timingsKey{}, t), func() []TimingEntry {
		t.mu.Lock()
		fns := append([]func() []TimingEntry(nil), t.fns...)
		t.mu.Unlock()
		var out []TimingEntry
		for i := len(fns) - 1; i >= 0; i-- {
			out = append(out, fns[i]()...)
		}
		return out
	}
}

// Timing registers entries for the current request, computed when they are reported. It does nothing unless a
// plugin collects timings (see [WithTimings]). A runtime registers `origin` (the instance's own handler) and
// `wake` (starting the instance for this request).
func Timing(ctx context.Context, fn func() []TimingEntry) {
	t, _ := ctx.Value(timingsKey{}).(*timings)
	if t == nil {
		return
	}
	t.mu.Lock()
	t.fns = append(t.fns, fn)
	t.mu.Unlock()
}
