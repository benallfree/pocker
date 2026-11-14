// Forwarding to the owner edge. A request for an instance another edge runs ([Instance.Remote]) goes there over the
// private network. Where other edges are comes from an [EdgeSource] (built in: [YAMLProvider]; ext/mothership's mirror;
// combined with [MultiProvider]). Plug [Router.Forward] into [Handler.Forward].
//
// Any edge accepts any request (a load balancer sends visitors to the nearest one). While the owner edge is healthy
// (heard from within HealthyWindow, or, when the provider has no heartbeat data, answering its health path) a request
// it can't take right now is held and retried; once it is down or shut down, requests fail fast with a 503.
package edge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
)

// RouterConfig configures a [Router].
type RouterConfig struct {
	// Edges resolves an instance's Remote to its owner edge (required).
	Edges EdgeSource

	// ForwardedByHeader marks a request forwarded by another edge; such a request is never forwarded again
	// (default "X-Edge-Forwarded-By").
	ForwardedByHeader string
	// HealthPath is probed on the owner (GET <internal_url><HealthPath>, 200 {"status":"ok"}); default
	// [DefaultHealthPath].
	HealthPath string
	// HealthyWindow: an edge heard from more recently than this is healthy without a probe (default 30s).
	HealthyWindow time.Duration
	// Retry is how often a held request re-checks its owner (default 250ms).
	Retry time.Duration
	// UnavailableMessage is the 503 body (default "This instance is temporarily unavailable. Please retry shortly.").
	UnavailableMessage string
	// MisdirectedSuffix ends the 421 body for a request another edge already forwarded here: "<host> " + it
	// (default "is served by another edge").
	MisdirectedSuffix string

	Logger *slog.Logger
}

const (
	connectTimeout    = 3 * time.Second
	staleProbeTimeout = 2 * time.Second
	staleProbeTTL     = 5 * time.Second
)

// Router forwards requests to owner edges.
type Router struct {
	cfg       RouterConfig
	log       *slog.Logger
	transport *http.Transport
	probe     func(ctx context.Context, internalURL string, timeout time.Duration) bool
	now       func() time.Time

	mu      sync.Mutex
	suspect map[string]bool // edge id -> a connect failed; wait for its health check before forwarding
	probes  map[string]*probeEntry
}

type probeEntry struct {
	at   time.Time
	done chan struct{}
	ok   bool
}

// New returns a Router for cfg, filling defaults.
func NewRouter(cfg RouterConfig) *Router {
	if cfg.ForwardedByHeader == "" {
		cfg.ForwardedByHeader = "X-Edge-Forwarded-By"
	}
	if cfg.HealthPath == "" {
		cfg.HealthPath = DefaultHealthPath
	}
	if cfg.HealthyWindow <= 0 {
		cfg.HealthyWindow = 30 * time.Second
	}
	if cfg.Retry <= 0 {
		cfg.Retry = 250 * time.Millisecond
	}
	if cfg.MisdirectedSuffix == "" {
		cfg.MisdirectedSuffix = "is served by another edge"
	}
	if cfg.UnavailableMessage == "" {
		cfg.UnavailableMessage = "This instance is temporarily unavailable. Please retry shortly."
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	rt := &Router{
		cfg: cfg,
		log: log,
		// Not http.DefaultTransport: an egress policy may block private addresses there.
		transport: &http.Transport{
			// Only the connect is limited (an unreachable edge often drops packets instead of refusing); long
			// responses such as realtime streams are not.
			DialContext:         (&net.Dialer{Timeout: connectTimeout, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConnsPerHost: 512,
			IdleConnTimeout:     90 * time.Second,
		},
		now:     time.Now,
		suspect: map[string]bool{},
		probes:  map[string]*probeEntry{},
	}
	rt.probe = rt.reachable
	return rt
}

// ForwardedByHeader is the header that marks a forwarded request.
func (rt *Router) ForwardedByHeader() string { return rt.cfg.ForwardedByHeader }

var probeClient = &http.Client{Transport: &http.Transport{
	DialContext: (&net.Dialer{Timeout: connectTimeout}).DialContext, MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second,
}}

// reachable asks an edge's health path directly.
func (rt *Router) reachable(ctx context.Context, internalURL string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(internalURL, "/")+rt.cfg.HealthPath, nil)
	if err != nil {
		return false
	}
	res, err := probeClient.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	var body struct {
		Status string `json:"status"`
	}
	return res.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&body) == nil && body.Status == "ok"
}

// healthy: not offline and heard from within the window (an edge with no heartbeat data is never healthy here).
func (rt *Router) healthy(e Edge) bool {
	if e.Status == "offline" || e.LastSeen.IsZero() {
		return false
	}
	return rt.now().Sub(e.LastSeen) <= rt.cfg.HealthyWindow
}

// alive: healthy per the source, or silent there (but not shut down) and answering its own health check.
func (rt *Router) alive(ctx context.Context, e Edge) bool {
	if rt.healthy(e) {
		return true
	}
	if e.Status == "offline" || e.InternalURL == "" {
		return false
	}
	ok := rt.cachedProbe(ctx, e)
	if ok && !e.LastSeen.IsZero() {
		rt.log.Debug("edge: edge is stale in its source but answers its health check", "edge", e.ID)
	}
	return ok
}

// cachedProbe shares one in-flight probe per edge and reuses its result for staleProbeTTL.
func (rt *Router) cachedProbe(ctx context.Context, e Edge) bool {
	rt.mu.Lock()
	p := rt.probes[e.ID]
	if p == nil || rt.now().Sub(p.at) >= staleProbeTTL {
		p = &probeEntry{at: rt.now(), done: make(chan struct{})}
		rt.probes[e.ID] = p
		go func(url string) {
			p.ok = rt.probe(context.Background(), url, staleProbeTimeout)
			close(p.done)
		}(e.InternalURL)
	}
	rt.mu.Unlock()
	select {
	case <-p.done:
		return p.ok
	case <-ctx.Done():
		return false
	}
}

// holdFor waits until the owner answers its health check. False once it is no longer alive or the client left.
func (rt *Router) holdFor(r *http.Request, remote string) bool {
	logged := false
	for {
		if r.Context().Err() != nil {
			return false
		}
		e, ok := rt.cfg.Edges.Resolve(remote)
		if !ok || !rt.alive(r.Context(), e) {
			return false
		}
		if rt.probe(r.Context(), e.InternalURL, rt.cfg.Retry) {
			rt.mu.Lock()
			delete(rt.suspect, e.ID)
			rt.mu.Unlock()
			return true
		}
		if !logged {
			rt.log.Info("edge: edge unreachable; holding requests while it is within its healthy window", "edge", e.ID)
			logged = true
		}
		select {
		case <-r.Context().Done():
			return false
		case <-time.After(rt.cfg.Retry):
		}
	}
}

func (rt *Router) unavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "10")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = io.WriteString(w, rt.cfg.UnavailableMessage)
}

// isUnreachable: the connection to the other edge failed (refused, reset, timed out) before a response.
func isUnreachable(err error) bool {
	var ne net.Error
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || (errors.As(err, &ne) && ne.Timeout()) {
		return true
	}
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

var idempotent = map[string]bool{http.MethodGet: true, http.MethodHead: true, http.MethodOptions: true}

// Forward serves a request for an instance another edge runs (plug into [Handler.Forward]).
func (rt *Router) Forward(w http.ResponseWriter, r *http.Request, inst Instance) {
	host := NormalizeHost(r.Host)
	if r.Header.Get(rt.cfg.ForwardedByHeader) != "" {
		// the forwarding edge says it's ours, we say it isn't: answer, don't bounce it back
		http.Error(w, host+" "+rt.cfg.MisdirectedSuffix, http.StatusMisdirectedRequest)
		return
	}
	owner, ok := rt.cfg.Edges.Resolve(inst.Remote)
	if !ok || owner.InternalURL == "" || !rt.alive(r.Context(), owner) {
		rt.log.Debug("edge: owner edge unavailable", "host", host, "remote", inst.Remote, "known", ok)
		rt.unavailable(w)
		return
	}

	policy := corsPolicy(inst.Access.CORSOrigins, r)
	if policy != nil {
		policy.apply(w.Header())
		if r.Method == http.MethodOptions {
			policy.preflight(w.Header(), r)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	rt.mu.Lock()
	suspect := rt.suspect[owner.ID]
	rt.mu.Unlock()
	if suspect && !rt.holdFor(r, inst.Remote) {
		rt.unavailable(w)
		return
	}

	self := rt.cfg.Edges.Self()
	for {
		e, ok := rt.cfg.Edges.Resolve(inst.Remote)
		if !ok || e.InternalURL == "" {
			e = owner
		}
		target, err := url.Parse(strings.TrimRight(e.InternalURL, "/"))
		if err != nil || target.Host == "" {
			rt.log.Warn("edge: bad internal_url", "edge", e.ID, "url", e.InternalURL)
			rt.unavailable(w)
			return
		}
		var failed error
		proxy := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				pr.Out.Host = host // the owner routes by Host (bare hostname: no port, no trailing dot)
				pr.SetXForwarded()
				pr.Out.Header.Set(rt.cfg.ForwardedByHeader, self)
			},
			Transport: rt.transport,
			ModifyResponse: func(res *http.Response) error {
				// the owner fulfilled it: its X-Powered-By (or none, e.g. upstream PocketBase) replaces ours
				w.Header().Del("X-Powered-By")
				if policy != nil {
					policy.rewriteUpstream(res.Header)
				}
				// ReverseProxy appends the owner's headers to ours. Where both set one (the firewall's rate-limit
				// headers and Access-Control-Expose-Headers on both edges), the owner's replaces ours: it counted every
				// request for the instance, not just the ones that entered here.
				for k := range res.Header {
					w.Header().Del(k)
				}
				return nil
			},
			ErrorHandler: func(_ http.ResponseWriter, _ *http.Request, err error) { failed = err },
		}
		if m := MarkFrom(r.Context()); m != nil {
			m.Start, m.To = time.Now(), e.ID // each attempt restarts the clock
		}
		proxy.ServeHTTP(w, r)
		if failed == nil {
			return
		}
		if r.Context().Err() != nil {
			return // client went away
		}
		if !isUnreachable(failed) {
			rt.log.Warn("edge: edge proxy error", "edge", e.ID, "host", host, "error", failed)
			http.Error(w, "Bad gateway", http.StatusBadGateway)
			return
		}
		rt.mu.Lock()
		rt.suspect[owner.ID] = true
		rt.mu.Unlock()
		// a request with a body may already be partly sent; only replay ones without
		if !idempotent[r.Method] || !rt.holdFor(r, inst.Remote) {
			rt.unavailable(w)
			return
		}
	}
}

// Mark records the latest attempt to forward a request to its owner edge (set by Forward), e.g. for Server-Timing.
type Mark struct {
	Start time.Time
	To    string // owner edge id; "" when the request wasn't forwarded
}

type markKey struct{}

// WithMark returns a context that records forwarding attempts (read it back with [MarkFrom] after serving).
func WithMark(ctx context.Context) (context.Context, *Mark) {
	m := &Mark{}
	return context.WithValue(ctx, markKey{}, m), m
}

// MarkFrom returns the request's Mark (nil when none was attached).
func MarkFrom(ctx context.Context) *Mark {
	m, _ := ctx.Value(markKey{}).(*Mark)
	return m
}
