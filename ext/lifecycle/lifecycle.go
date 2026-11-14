// Package lifecycle looks after the data of the instances a host serves: it measures facts about them (disk used,
// last request served) and carries out decisions about them. What to do comes from a [PolicyProvider] (local YAML
// rules by default; a control plane can provide its own), and where facts go from a [Sink]. It knows nothing about
// plans, payments or tenants: that is inside whichever provider is configured.
//
//	lc := &lifecycle.Lifecycle{Directory: h.Directory, Folder: func(i host.Instance) string { return filepath.Join(root, i.ID) }}
//	h.Gates = append(h.Gates, lc)                  // records each local instance's last request
//	h.Background = append(h.Background, lc.Run)   // measures and reports on the policy's schedule
package lifecycle

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/pockethost/pocketone/ext/host"
)

// Facts are what this host knows about one of its instances.
type Facts struct {
	Instance   string    `json:"instance"`
	DiskBytes  int64     `json:"disk_bytes"`           // size of the instance folder (0 when it has none)
	MeasuredAt time.Time `json:"measured_at,omitzero"` // when DiskBytes was measured (zero = never)
	LastActive time.Time `json:"last_active,omitzero"` // the last request this host served for it (zero = none seen)
}

// Sink receives facts that changed: after a measuring pass, and on the report interval for last-request times.
type Sink interface {
	Report(ctx context.Context, facts []Facts) error
}

// Loader is an optional [Sink] extension: facts recorded earlier (e.g. by a control plane), loaded once at start so a
// restart doesn't measure everything again or forget last-request times.
type Loader interface {
	Load(ctx context.Context) ([]Facts, error)
}

// LogSink logs a summary of each report (the default Sink).
type LogSink struct{ Logger *slog.Logger }

// Report implements [Sink].
func (s LogSink) Report(_ context.Context, facts []Facts) error {
	var bytes int64
	for _, f := range facts {
		bytes += f.DiskBytes
	}
	log := s.Logger
	if log == nil {
		log = slog.Default()
	}
	log.Info("lifecycle: facts", "instances", len(facts), "disk_bytes", bytes)
	return nil
}

// Lifecycle measures and reports facts about the instances this host serves itself (not the ones it forwards), and
// carries out its policy's decisions. It is a [host.Gate] (to see requests) with a Run loop (a host background job).
type Lifecycle struct {
	Directory *host.Directory                 // the host's instances (required)
	Folder    func(inst host.Instance) string // an instance's folder (required): the runtime's root/<id>, or inst.Dir
	Policy    PolicyProvider                  // nil = YAMLPolicy from the `lifecycle` section
	Sink      Sink                            // nil = LogSink
	Logger    *slog.Logger
	Now       func() time.Time // for tests

	mu     sync.Mutex
	facts  map[string]*Facts
	active map[string]bool // instances whose LastActive changed since the last report
}

var _ host.Gate = (*Lifecycle)(nil)

// AllowPeer implements [host.Gate]; lifecycle never refuses a peer.
func (l *Lifecycle) AllowPeer(*http.Request) bool { return true }

// Admit implements [host.Gate]: it records the request as the instance's last activity when this host serves it.
func (l *Lifecycle) Admit(_ http.ResponseWriter, _ *http.Request, inst host.Instance) (func(), bool) {
	if inst.Remote == "" {
		l.touch(inst.ID)
	}
	return func() {}, true
}

func (l *Lifecycle) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Lifecycle) log() *slog.Logger {
	if l.Logger != nil {
		return l.Logger
	}
	return slog.Default()
}

func (l *Lifecycle) init() {
	if l.facts == nil {
		l.facts = map[string]*Facts{}
		l.active = map[string]bool{}
	}
}

func (l *Lifecycle) touch(id string) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.init()
	f := l.facts[id]
	if f == nil {
		f = &Facts{Instance: id}
		l.facts[id] = f
	}
	// one write per minute per instance is plenty for a staleness signal
	if now.Sub(f.LastActive) >= time.Minute {
		f.LastActive = now
		l.active[id] = true
	}
}

// Local returns the facts of every instance this host serves itself, in id order (instances never measured have a
// zero MeasuredAt).
func (l *Lifecycle) Local() []Facts {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.init()
	var out []Facts
	for _, inst := range l.Directory.List() {
		if inst.Remote != "" {
			continue
		}
		f := Facts{Instance: inst.ID}
		if known := l.facts[inst.ID]; known != nil {
			f = *known
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out
}

// Run measures and reports until ctx is done: every minute it asks the policy what to do and does it; changed
// last-request times are reported on the policy's report interval.
func (l *Lifecycle) Run(ctx context.Context) {
	l.defaults()
	if loader, ok := l.Sink.(Loader); ok {
		if facts, err := loader.Load(ctx); err != nil {
			l.log().Warn("lifecycle: loading recorded facts", "error", err)
		} else {
			l.seed(facts)
		}
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	lastReport := l.now()
	for {
		if ctx.Err() != nil {
			return
		}
		if l.Directory.Ready() {
			l.Step(ctx)
			if l.now().Sub(lastReport) >= l.Policy.ReportEvery() {
				l.reportActive(ctx)
				lastReport = l.now()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (l *Lifecycle) seed(facts []Facts) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.init()
	for _, f := range facts {
		f := f
		l.facts[f.Instance] = &f
	}
}

// defaults fills in the policy (YAMLPolicy) and sink (LogSink) when none were set.
func (l *Lifecycle) defaults() {
	if l.Policy == nil {
		l.Policy = YAMLPolicy{}
	}
	if l.Sink == nil {
		l.Sink = LogSink{Logger: l.Logger}
	}
}

// Step runs one round: the policy's decisions for the current facts, then a report of what was measured.
func (l *Lifecycle) Step(ctx context.Context) {
	l.defaults()
	decisions, err := l.Policy.Decide(ctx, l.Local())
	if err != nil {
		l.log().Warn("lifecycle: policy", "error", err)
		return
	}
	var measure []string
	for _, d := range decisions {
		switch d.Action {
		case Measure:
			measure = append(measure, d.Instance)
		default:
			l.log().Warn("lifecycle: action not supported yet", "action", d.Action, "instance", d.Instance)
		}
	}
	if len(measure) == 0 {
		return
	}
	t0 := l.now()
	measured := l.measure(ctx, measure, l.Policy.Concurrency())
	if err := l.Sink.Report(ctx, measured); err != nil {
		l.log().Warn("lifecycle: report", "error", err)
		return
	}
	var bytes int64
	for _, f := range measured {
		bytes += f.DiskBytes
	}
	l.log().Info("lifecycle: measured", "instances", len(measured), "disk_bytes", bytes, "ms", l.now().Sub(t0).Milliseconds())
}

// measure sizes the given instances' folders (a few at a time) and records the results.
func (l *Lifecycle) measure(ctx context.Context, ids []string, concurrency int) []Facts {
	if concurrency < 1 {
		concurrency = 1
	}
	type result struct {
		id    string
		bytes int64
		ok    bool
	}
	jobs := make(chan string)
	results := make(chan result)
	var wg sync.WaitGroup
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				inst, found := l.Directory.Get(id)
				if !found {
					results <- result{id: id}
					continue
				}
				bytes, err := DirBytes(ctx, l.Folder(inst))
				if err != nil {
					l.log().Warn("lifecycle: measuring", "instance", id, "error", err)
				}
				results <- result{id: id, bytes: bytes, ok: err == nil}
			}
		}()
	}
	go func() {
		for _, id := range ids {
			if ctx.Err() != nil {
				break
			}
			jobs <- id
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	now := l.now()
	var out []Facts
	for r := range results {
		if !r.ok {
			continue
		}
		l.mu.Lock()
		l.init()
		f := l.facts[r.id]
		if f == nil {
			f = &Facts{Instance: r.id}
			l.facts[r.id] = f
		}
		f.DiskBytes, f.MeasuredAt = r.bytes, now
		out = append(out, *f)
		delete(l.active, r.id) // its LastActive goes with this report
		l.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out
}

// reportActive reports the instances whose last-request time changed since the last report.
func (l *Lifecycle) reportActive(ctx context.Context) {
	l.mu.Lock()
	l.init()
	var out []Facts
	for id := range l.active {
		if f := l.facts[id]; f != nil {
			out = append(out, *f)
		}
	}
	l.active = map[string]bool{}
	l.mu.Unlock()
	if len(out) == 0 {
		return
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	if err := l.Sink.Report(ctx, out); err != nil {
		l.log().Warn("lifecycle: report", "error", err)
		l.mu.Lock()
		for _, f := range out {
			l.active[f.Instance] = true // try again next time
		}
		l.mu.Unlock()
	}
}

// DirBytes is the total size of the regular files under dir (0, nil when dir doesn't exist). Symlinks aren't
// followed.
func DirBytes(ctx context.Context, dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil // gone while walking (or never there)
			}
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}
