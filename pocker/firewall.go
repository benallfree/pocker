package pocker

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FirewallConfig is the process-wide part of the firewall (pocker.yaml `firewall:`). Per-instance rate limits,
// trusted clients and CORS origins live in [Limits].
//
//	firewall:
//	  allow_cidrs: []                 # peers allowed to connect (empty = everyone)
//	  trusted_proxies: []             # peers whose client-IP headers are believed
//	  client_ip_headers: [CF-Connecting-IP, True-Client-IP, X-Forwarded-For, X-Real-IP]
//	  trusted_clients: []             # client IPs that get the trusted rate limits on every instance
//	  rate_limit_header_prefix: X-RateLimit
type FirewallConfig struct {
	AllowCIDRs            []string `yaml:"allow_cidrs"`
	TrustedProxies        []string `yaml:"trusted_proxies"`
	ClientIPHeaders       []string `yaml:"client_ip_headers"`
	TrustedClients        []string `yaml:"trusted_clients"`
	RateLimitHeaderPrefix string   `yaml:"rate_limit_header_prefix"`
}

// DefaultClientIPHeaders are read, in order, from trusted proxies.
var DefaultClientIPHeaders = []string{"CF-Connecting-IP", "True-Client-IP", "X-Forwarded-For", "X-Real-IP"}

// Firewall checks the connecting peer, works out the client IP, and applies per-instance rate limits.
type Firewall struct {
	allow     []netip.Prefix
	proxies   []netip.Prefix
	headers   []string
	trusted   []netip.Prefix
	prefix    string
	exposeAll string

	mu         sync.Mutex
	hourly     map[string]*hourlyBucket
	concurrent map[string]int64
	lastSweep  time.Time
}

type hourlyBucket struct {
	used  int64 // micro-points (1 request = 10)
	reset time.Time
}

const microPerRequest = 10

// NewFirewall builds a firewall from its config.
func NewFirewall(cfg FirewallConfig) (*Firewall, error) {
	f := &Firewall{
		headers:    cfg.ClientIPHeaders,
		prefix:     cfg.RateLimitHeaderPrefix,
		hourly:     map[string]*hourlyBucket{},
		concurrent: map[string]int64{},
	}
	var err error
	if f.allow, err = parsePrefixes("firewall.allow_cidrs", cfg.AllowCIDRs); err != nil {
		return nil, err
	}
	if f.proxies, err = parsePrefixes("firewall.trusted_proxies", cfg.TrustedProxies); err != nil {
		return nil, err
	}
	if f.trusted, err = parsePrefixes("firewall.trusted_clients", cfg.TrustedClients); err != nil {
		return nil, err
	}
	if f.headers == nil {
		f.headers = DefaultClientIPHeaders
	}
	if f.prefix == "" {
		f.prefix = "X-RateLimit"
	}
	var names []string
	for _, n := range rateHeaderNames {
		names = append(names, f.prefix+"-"+n)
	}
	f.exposeAll = strings.Join(names, ", ")
	return f, nil
}

var rateHeaderNames = []string{
	"Ip-Hourly-Limit", "Ip-Hourly-Remaining", "Ip-Hourly-Reset",
	"Instance-Hourly-Limit", "Instance-Hourly-Remaining", "Instance-Hourly-Reset",
	"Ip-Concurrent-Limit", "Ip-Concurrent-Remaining",
	"Instance-Concurrent-Limit", "Instance-Concurrent-Remaining",
}

func parsePrefixes(field string, list []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not an IP or CIDR", field, s)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

func containsAddr(list []netip.Prefix, a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range list {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func peerAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, _ := netip.ParseAddr(host)
	return a.Unmap()
}

// AllowPeer reports whether the connecting peer may talk to Pocker at all.
func (f *Firewall) AllowPeer(r *http.Request) bool {
	return len(f.allow) == 0 || containsAddr(f.allow, peerAddr(r))
}

// ClientIP is the request's client: from the first configured header when the peer is a trusted proxy, otherwise
// the peer itself.
func (f *Firewall) ClientIP(r *http.Request) netip.Addr {
	peer := peerAddr(r)
	if !containsAddr(f.proxies, peer) {
		return peer
	}
	for _, h := range f.headers {
		v := r.Header.Get(h)
		if v == "" {
			continue
		}
		if strings.EqualFold(h, "X-Forwarded-For") {
			v = strings.Split(v, ",")[0]
		}
		if a, err := netip.ParseAddr(strings.TrimSpace(v)); err == nil {
			return a.Unmap()
		}
	}
	return peer
}

// rateCheck consumes the request's weight from the instance's buckets. When a limit is exceeded it writes the 429 and
// returns ok=false; otherwise it returns a release function for the concurrency slots (call when the response ends).
func (f *Firewall) rateCheck(w http.ResponseWriter, r *http.Request, inst Instance, lim Limits) (release func(), ok bool) {
	rl := lim.RateLimits
	if rl == nil || rl.Off {
		return func() {}, true
	}
	client := f.ClientIP(r)
	trusted := containsAddr(f.trusted, client)
	if !trusted && len(lim.TrustedClients) > 0 {
		if inst, err := parsePrefixes("trusted_clients", lim.TrustedClients); err == nil {
			trusted = containsAddr(inst, client)
		}
	}
	set := RateLimitSet{IPHourly: rl.IPHourly, InstanceHourly: rl.InstanceHourly, IPConcurrent: rl.IPConcurrent, InstanceConcurrent: rl.InstanceConcurrent}
	if trusted && rl.Trusted != nil {
		set = *rl.Trusted
	}

	weight := int64(microPerRequest)
	if r.URL.Path == "/api/files" || strings.HasPrefix(r.URL.Path, "/api/files/") {
		weight = int64(math.Max(1, math.Round(rl.FileWeight*microPerRequest)))
	}

	ipKey := client.String() + "|" + inst.ID
	instKey := inst.ID
	now := time.Now()
	h := w.Header()
	expose := func() {
		if prev := h.Get("Access-Control-Expose-Headers"); prev != "" {
			h.Set("Access-Control-Expose-Headers", f.exposeAll+", "+prev)
		} else {
			h.Set("Access-Control-Expose-Headers", f.exposeAll)
		}
	}
	setHourly := func(kind string, limit int, b *hourlyBucket) {
		h.Set(f.prefix+"-"+kind+"-Hourly-Limit", strconv.Itoa(limit))
		h.Set(f.prefix+"-"+kind+"-Hourly-Remaining", strconv.FormatInt(max(0, int64(limit)*microPerRequest-b.used)/microPerRequest, 10))
		h.Set(f.prefix+"-"+kind+"-Hourly-Reset", strconv.FormatInt(int64(math.Ceil(float64(b.reset.UnixMilli())/1000)), 10))
	}
	setConc := func(kind string, limit int, used int64) {
		h.Set(f.prefix+"-"+kind+"-Concurrent-Limit", strconv.Itoa(limit))
		h.Set(f.prefix+"-"+kind+"-Concurrent-Remaining", strconv.FormatInt(max(0, int64(limit)*microPerRequest-used)/microPerRequest, 10))
	}
	retryIn := func(b *hourlyBucket) int { return int(math.Ceil(time.Until(b.reset).Seconds())) }
	who := func() string {
		if peer := peerAddr(r); peer != client {
			return fmt.Sprintf("IP %s (connecting IP %s)", client, peer)
		}
		return "IP " + client.String()
	}

	f.mu.Lock()
	f.sweepLocked(now)
	ipH := f.bucketLocked(ipKey, now)
	ipH.used += weight
	if ipH.used > int64(set.IPHourly)*microPerRequest {
		setHourly("Ip", set.IPHourly, ipH)
		f.mu.Unlock()
		expose()
		h.Set("Retry-After", strconv.Itoa(retryIn(ipH)))
		http.Error(w, fmt.Sprintf("Too Many Requests: per-IP hourly limit of %d requests/hour exceeded for %s on instance %s; retry after %d seconds",
			set.IPHourly, who(), inst.ID, retryIn(ipH)), http.StatusTooManyRequests)
		return nil, false
	}
	instH := f.bucketLocked("i|"+instKey, now)
	instH.used += weight
	if instH.used > int64(set.InstanceHourly)*microPerRequest {
		setHourly("Ip", set.IPHourly, ipH)
		setHourly("Instance", set.InstanceHourly, instH)
		f.mu.Unlock()
		expose()
		h.Set("Retry-After", strconv.Itoa(retryIn(instH)))
		http.Error(w, fmt.Sprintf("Too Many Requests: per-instance hourly limit of %d requests/hour exceeded for instance %s; retry after %d seconds",
			set.InstanceHourly, inst.ID, retryIn(instH)), http.StatusTooManyRequests)
		return nil, false
	}
	if f.concurrent[ipKey]+weight > int64(set.IPConcurrent)*microPerRequest {
		setHourly("Ip", set.IPHourly, ipH)
		setHourly("Instance", set.InstanceHourly, instH)
		setConc("Ip", set.IPConcurrent, f.concurrent[ipKey])
		f.mu.Unlock()
		expose()
		h.Set("Retry-After", "1")
		http.Error(w, fmt.Sprintf("Too Many Requests: per-IP concurrent limit of %d concurrent requests exceeded for %s on instance %s",
			set.IPConcurrent, who(), inst.ID), http.StatusTooManyRequests)
		return nil, false
	}
	if f.concurrent[instKey]+weight > int64(set.InstanceConcurrent)*microPerRequest {
		setHourly("Ip", set.IPHourly, ipH)
		setHourly("Instance", set.InstanceHourly, instH)
		setConc("Ip", set.IPConcurrent, f.concurrent[ipKey])
		setConc("Instance", set.InstanceConcurrent, f.concurrent[instKey])
		f.mu.Unlock()
		expose()
		h.Set("Retry-After", "1")
		http.Error(w, fmt.Sprintf("Too Many Requests: per-instance concurrent limit of %d concurrent requests exceeded for instance %s",
			set.InstanceConcurrent, inst.ID), http.StatusTooManyRequests)
		return nil, false
	}
	f.concurrent[ipKey] += weight
	f.concurrent[instKey] += weight
	setHourly("Ip", set.IPHourly, ipH)
	setHourly("Instance", set.InstanceHourly, instH)
	setConc("Ip", set.IPConcurrent, f.concurrent[ipKey])
	setConc("Instance", set.InstanceConcurrent, f.concurrent[instKey])
	f.mu.Unlock()
	expose()

	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			for _, k := range []string{ipKey, instKey} {
				if f.concurrent[k] -= weight; f.concurrent[k] <= 0 {
					delete(f.concurrent, k)
				}
			}
			f.mu.Unlock()
		})
	}, true
}

func (f *Firewall) bucketLocked(key string, now time.Time) *hourlyBucket {
	b := f.hourly[key]
	if b == nil || !now.Before(b.reset) {
		b = &hourlyBucket{reset: now.Add(time.Hour)}
		f.hourly[key] = b
	}
	return b
}

// sweepLocked drops expired hourly buckets once a minute.
func (f *Firewall) sweepLocked(now time.Time) {
	if now.Sub(f.lastSweep) < time.Minute {
		return
	}
	f.lastSweep = now
	for k, b := range f.hourly {
		if !now.Before(b.reset) {
			delete(f.hourly, k)
		}
	}
}
