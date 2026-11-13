// Package servertiming is a PocketOne plugin for the pocker host: a Server-Timing response header with the
// timings the host and other plugins register for each request (see pocker.Timing).
//
//	srv := &pocker.Server{Config: cfg, Plugins: []pocker.Plugin{servertiming.New()}}
package servertiming

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pockethost/pocketone/ext/pocker"
)

// Header carries per-hop timing in the standard Server-Timing response header. Each hop measures from
// receiving the request to sending the response headers (time to first byte), keeps the entries the next hop sent
// back, and adds its own after them, so the innermost hop comes first. The Manager adds:
//
//   - origin: the instance's own handler, from handing it the request until it sent its response headers
//   - wake:   starting the instance for this request (0 when it was already running)
//
// Host code and plugins add their own entries with pocker.Timing (e.g. a total for the server).
// A hop's own overhead is its dur minus the entry it wraps. Only names go in desc, never addresses.
const Header = "Server-Timing"

// TimingEntry is one Server-Timing metric.
type TimingEntry = pocker.TimingEntry

// Plugin adds the header ([pocker.Wrapper]). List it before plugins and host code whose timings it should include:
// the first listed wrapper is the outermost.
type Plugin struct{}

// New returns the plugin.
func New() *Plugin { return &Plugin{} }

// Name is the plugin's name.
func (*Plugin) Name() string { return "servertiming" }

// Wrap collects the request's timings and appends them to the response's Server-Timing header just before the
// headers go out, after any values an upstream hop already sent.
func (*Plugin) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, eval := pocker.WithTimings(r.Context())
		next.ServeHTTP(WithServerTiming(w, eval), r.WithContext(ctx))
	})
}

// FormatTiming renders an entry, e.g. `edge;dur=63.5;desc="sfo"` (milliseconds, one decimal).
func FormatTiming(e TimingEntry) string {
	d := e.Dur
	if d < 0 {
		d = 0
	}
	s := fmt.Sprintf("%s;dur=%.1f", e.Name, float64(d)/float64(time.Millisecond))
	if desc := strings.NewReplacer(`"`, "", `\`, "").Replace(e.Desc); desc != "" {
		s += `;desc="` + desc + `"`
	}
	return s
}

// AppendServerTiming adds entries after any Server-Timing values already in h (from upstream hops), as one header.
func AppendServerTiming(h http.Header, entries ...TimingEntry) {
	if len(entries) == 0 {
		return
	}
	var parts []string
	for _, v := range h.Values(Header) {
		if strings.TrimSpace(v) != "" {
			parts = append(parts, v)
		}
	}
	for _, e := range entries {
		parts = append(parts, FormatTiming(e))
	}
	h.Set(Header, strings.Join(parts, ", "))
}

// WithServerTiming returns a writer that appends the entries fn returns just before the response headers go out
// (interim 1xx responses excluded). It keeps Flush, Hijack and http.ResponseController working.
func WithServerTiming(w http.ResponseWriter, fn func() []TimingEntry) http.ResponseWriter {
	return &timingWriter{ResponseWriter: w, fn: fn}
}

type timingWriter struct {
	http.ResponseWriter
	fn   func() []TimingEntry
	once sync.Once
}

func (t *timingWriter) stamp() {
	t.once.Do(func() { AppendServerTiming(t.ResponseWriter.Header(), t.fn()...) })
}

func (t *timingWriter) WriteHeader(code int) {
	if code >= 200 || code == http.StatusSwitchingProtocols {
		t.stamp()
	}
	t.ResponseWriter.WriteHeader(code)
}

func (t *timingWriter) Write(b []byte) (int, error) {
	t.stamp()
	return t.ResponseWriter.Write(b)
}

func (t *timingWriter) Flush() {
	t.stamp()
	_ = http.NewResponseController(t.ResponseWriter).Flush()
}

func (t *timingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	t.stamp()
	return http.NewResponseController(t.ResponseWriter).Hijack()
}

func (t *timingWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }
