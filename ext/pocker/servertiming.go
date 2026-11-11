package pocker

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ServerTimingHeader carries per-hop timing in the standard Server-Timing response header. Each hop measures from
// receiving the request to sending the response headers (time to first byte), keeps the entries the next hop sent
// back, and adds its own after them, so the innermost hop comes first. The Manager adds:
//
//   - origin: the instance's own handler, from handing it the request until it sent its response headers
//   - wake:   starting the instance for this request (0 when it was already running)
//
// Host code in front of the Manager can add its own entries (e.g. a total for the server) with WithServerTiming.
// A hop's own overhead is its dur minus the entry it wraps. Only names go in desc, never addresses.
const ServerTimingHeader = "Server-Timing"

// TimingEntry is one Server-Timing metric.
type TimingEntry struct {
	Name string
	Dur  time.Duration
	Desc string
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
	for _, v := range h.Values(ServerTimingHeader) {
		if strings.TrimSpace(v) != "" {
			parts = append(parts, v)
		}
	}
	for _, e := range entries {
		parts = append(parts, FormatTiming(e))
	}
	h.Set(ServerTimingHeader, strings.Join(parts, ", "))
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
