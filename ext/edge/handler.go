package edge

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"time"
)

// DefaultHealthPath answers on any host with the edge's status (the path pocker always used).
const DefaultHealthPath = "/_pocker/health"

// Handler is the edge's request pipeline: health → peer check → resolve the Host to an instance (directory) →
// blocked → owned by another edge? admit and forward : precheck, admit and hand to the instance's runtime.
type Handler struct {
	Dir      *Directory
	Gate     Gate      // nil = admit everything
	Runtimes []Runtime // in preference order (see [Runtime.Serves])

	// Forward serves a request for an instance another edge runs ([Instance.Remote] set), after the gates. Nil = a
	// plain reverse proxy to Remote as a URL.
	Forward func(w http.ResponseWriter, r *http.Request, inst Instance)

	// Dev serves any hostname: one the directory doesn't map becomes an instance with id [DevID] and Dev set.
	Dev bool

	HealthPath string // "" = DefaultHealthPath
	Logger     *slog.Logger
}

func (h *Handler) log() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// Ready reports whether the directory has loaded and every runtime is ready.
func (h *Handler) Ready() bool {
	if !h.Dir.Ready() {
		return false
	}
	for _, rt := range h.Runtimes {
		if !rt.Ready() {
			return false
		}
	}
	return true
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hp := h.HealthPath
	if hp == "" {
		hp = DefaultHealthPath
	}
	if r.URL.Path == hp {
		h.serveHealth(w)
		return
	}
	if h.Gate != nil && !h.Gate.AllowPeer(r) {
		http.Error(w, "forbidden: "+PeerAddr(r).String(), http.StatusForbidden)
		return
	}
	if !h.Ready() {
		http.Error(w, "starting", http.StatusServiceUnavailable)
		return
	}

	inst, status, msg := h.resolve(r)
	if status != 0 {
		http.Error(w, msg, status)
		return
	}
	if inst.Blocked != nil {
		status := inst.Blocked.Status
		if status == 0 {
			status = http.StatusForbidden
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(inst.Blocked.Message))
		return
	}

	if inst.Remote != "" {
		if h.Gate != nil {
			release, ok := h.Gate.Admit(w, r, inst)
			if !ok {
				return
			}
			defer release()
		}
		if h.Forward != nil {
			h.Forward(w, r, inst)
			return
		}
		h.proxyRemote(w, r, inst)
		return
	}

	rt := pick(h.Runtimes, inst)
	if rt == nil {
		http.Error(w, "no runtime on this server can run "+inst.ID+versionNote(inst), http.StatusServiceUnavailable)
		return
	}
	if pc, ok := rt.(Prechecker); ok && !pc.Precheck(w, r, inst) {
		return
	}
	if h.Gate != nil {
		release, ok := h.Gate.Admit(w, r, inst)
		if !ok {
			return
		}
		defer release() // long-lived responses (realtime) hold their admission until they end
	}
	rt.ServeInstance(w, r, inst)
}

func versionNote(inst Instance) string {
	if inst.Version == "" {
		return ""
	}
	return " (PocketBase " + inst.Version + ")"
}

// resolve maps the request's Host to an instance. In dev mode an unmapped host becomes a new instance whose id is
// the hostname ([DevID]). A non-zero status means the request can't be routed.
func (h *Handler) resolve(r *http.Request) (Instance, int, string) {
	host := NormalizeHost(r.Host)
	if inst, ok := h.Dir.Lookup(host); ok {
		return inst, 0, ""
	}
	if h.Dev {
		id := DevID(host)
		if id == "" {
			return Instance{}, http.StatusBadRequest, "hostname can't be used as an instance id: " + host
		}
		if inst, ok := h.Dir.Get(id); ok {
			return inst, 0, ""
		}
		inst := Instance{ID: id, Hosts: []string{host}, Dev: true}
		h.Dir.Put(inst)
		h.log().Info("edge: dev instance", "host", host, "id", id)
		return inst, 0, ""
	}
	return Instance{}, http.StatusNotFound, host + " not found"
}

// proxyRemote is the default forwarder: Remote is the other server's URL.
func (h *Handler) proxyRemote(w http.ResponseWriter, r *http.Request, inst Instance) {
	target, err := url.Parse(inst.Remote)
	if err != nil || target.Host == "" {
		http.Error(w, "bad remote for "+inst.ID, http.StatusBadGateway)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = forwardTransport
	proxy.ErrorLog = slog.NewLogLogger(h.log().Handler(), slog.LevelWarn)
	proxy.ServeHTTP(w, r)
}

// forwardTransport is the default forwarder's own transport: http.DefaultTransport may carry an egress policy that
// blocks private addresses, where other servers usually are.
var forwardTransport = &http.Transport{
	DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	MaxIdleConnsPerHost: 64,
	IdleConnTimeout:     90 * time.Second,
}

func (h *Handler) serveHealth(w http.ResponseWriter) {
	running := 0
	for _, rt := range h.Runtimes {
		if s, ok := rt.(Statser); ok {
			running += s.RunningCount()
		}
	}
	status := http.StatusOK
	body := map[string]any{"status": "ok", "instances": h.Dir.Len(), "running": running}
	if !h.Ready() {
		status = http.StatusServiceUnavailable
		body["status"] = "starting"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// PeerAddr is the address of the connecting peer (RemoteAddr), without port.
func PeerAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, _ := netip.ParseAddr(host)
	return a.Unmap()
}
