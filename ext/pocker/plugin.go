package pocker

import (
	"context"
	"crypto/tls"
	"net/http"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Plugin extends a Pocker server (see [Server.Plugins]). A plugin implements Name and any of the optional
// interfaces below; the server checks each one with a type assertion.
type Plugin interface {
	// Name identifies the plugin: its key under an instance's `plugins:` block, and in logs.
	Name() string
}

// Configurer reads a plugin's settings. doc is the whole config file (nil when there is none), so a plugin can own
// its own top-level keys; cfg is the parsed core config. Configure runs once, before the server starts.
type Configurer interface {
	Configure(doc *yaml.Node, cfg Config) error
}

// Gate admits requests (e.g. a firewall). AllowPeer runs before routing and refuses with 403; Admit runs before an
// instance's request is served here or forwarded to another server, may answer it itself (ok=false), and otherwise
// returns a release function called when the response ends (long-lived responses hold it until they end).
type Gate interface {
	AllowPeer(r *http.Request) bool
	Admit(w http.ResponseWriter, r *http.Request, inst Instance) (release func(), ok bool)
}

// TLSProvider supplies the main listener's TLS (e.g. automatic certificates). served reports whether a hostname
// belongs to an instance, for providers that must not issue certificates for anything else. httpHandler, if not
// nil, wraps the plain-HTTP listener's handler (tls.http_listen), e.g. to answer ACME challenges.
type TLSProvider interface {
	TLS(ctx context.Context, served func(host string) bool) (cfg *tls.Config, httpHandler func(http.Handler) http.Handler, err error)
}

// Wrapper wraps the server's handler. Wrappers nest in [Server.Plugins] order: the first listed is the outermost.
type Wrapper interface {
	Wrap(next http.Handler) http.Handler
}

// Starter runs background work once the Manager exists, until ctx is done.
type Starter interface {
	Start(ctx context.Context, m *Manager)
}

// PluginConfig returns an instance's settings for the named plugin (its `plugins.<name>` value), or nil.
func (i Instance) PluginConfig(name string) any {
	if i.Plugins == nil {
		return nil
	}
	return i.Plugins[name]
}

// DecodePluginConfig fills out from an instance's plugin settings: a value of out's own type (or a pointer to it,
// as a host application's instance source would set) is copied; anything else (e.g. a map from YAML) is converted
// through YAML. A nil value leaves out unchanged.
func DecodePluginConfig[T any](v any, out *T) error {
	switch x := v.(type) {
	case nil:
		return nil
	case T:
		*out = x
		return nil
	case *T:
		if x != nil {
			*out = *x
		}
		return nil
	}
	b, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, out)
}

// TopLevel returns the value node for a top-level key of a config document, or nil.
func TopLevel(doc *yaml.Node, key string) *yaml.Node {
	if doc == nil {
		return nil
	}
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

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
// plugin collects timings (see [WithTimings]). The Manager registers `origin` (the instance's own handler) and
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
