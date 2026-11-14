// Package firewall is PocketOne's request firewall for a multi-instance host (ext/host): an allowed-peers list, trusted
// proxies and client IPs, and per-IP and per-instance rate limits (hourly budgets and concurrency caps, with higher
// limits for trusted clients). It is a host [host.Gate]; settings come from the config's `firewall:` and
// `rate_limits:` sections and, per instance, `plugins.firewall`. It works with or without ext/edge.
package firewall

import (
	"fmt"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pockethost/pocketone/ext/ceplugin"

	"github.com/pockethost/pocketone/ext/host"

	"gopkg.in/yaml.v3"
)

// Config is the process-wide part of the firewall (the config file `firewall:`). Rate limits are `rate_limits:` (the
// defaults, see [RateLimits]) and, per instance, `plugins.firewall` (see [InstanceConfig]).
//
//	firewall:
//	  allow_cidrs: []                 # peers allowed to connect (empty = everyone)
//	  trusted_proxies: []             # peers whose client-IP headers are believed
//	  client_ip_headers: [CF-Connecting-IP, True-Client-IP, X-Forwarded-For, X-Real-IP]
//	  trusted_clients: []             # client IPs that get the trusted rate limits on every instance
//	  trusted_client_headers: [X-Client-IP] # a trusted client (e.g. an SSR proxy) names its end user here
//	  rate_limit_header_prefix: X-RateLimit
type Config struct {
	AllowCIDRs            []string `yaml:"allow_cidrs"`
	TrustedProxies        []string `yaml:"trusted_proxies"`
	ClientIPHeaders       []string `yaml:"client_ip_headers"`
	TrustedClients        []string `yaml:"trusted_clients"`
	TrustedClientHeaders  []string `yaml:"trusted_client_headers"`
	RateLimitHeaderPrefix string   `yaml:"rate_limit_header_prefix"`
}

// DefaultTrustedClientHeaders are read, in order, from trusted clients (process-wide or the instance's trusted_ips):
// a server-side proxy or SSR app sends its end user's IP there, so each user gets their own per-IP buckets (with the
// trusted limits) instead of all sharing the proxy's.
var DefaultTrustedClientHeaders = []string{"X-Client-IP"}

// DefaultClientIPHeaders are read, in order, from trusted proxies.
var DefaultClientIPHeaders = []string{"CF-Connecting-IP", "True-Client-IP", "X-Forwarded-For", "X-Real-IP"}

// InstanceConfig is an instance's firewall settings (`plugins.firewall`): its own rate limits over the defaults,
// and client IPs/CIDRs that get the trusted limits on this instance (e.g. the owner's own list).
type InstanceConfig struct {
	RateLimits *RateLimits `yaml:"rate_limits,omitempty"`
	TrustedIPs []string    `yaml:"trusted_ips,omitempty"`
}

// Firewall checks the connecting peer, works out the client IP, and applies per-instance rate limits. It is a
// [host.Gate]. The zero value allows every peer and applies [DefaultRateLimits] once configured.
type Firewall struct {
	// Config and RateLimits may be set before the server starts; the config file adds to them.
	Config     Config
	RateLimits *RateLimits

	// Adjust, if set, runs after the config file is read, e.g. for a host application that adds settings from its
	// own environment.
	Adjust func(cfg *Config, defaults **RateLimits)

	once     sync.Once
	setupErr error
	defaults RateLimits // effective defaults for every instance

	allow     []netip.Prefix
	proxies   []netip.Prefix
	headers   []string
	endUser   []string // trusted_client_headers
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

// NewFirewall returns a ready firewall for cfg with the default rate limits, for direct use without a server.
func NewFirewall(cfg Config) (*Firewall, error) {
	f := &Firewall{Config: cfg}
	return f, f.setup()
}

// Name is the firewall's key under an instance's `plugins:`.
func (f *Firewall) Name() string { return "firewall" }

// Configure reads `firewall:` and `rate_limits:` from the config file. List fields add to what was set before;
// other fields fill in what wasn't. In dev mode, rate limiting is off unless rate_limits is set somewhere.
func (f *Firewall) Configure(doc *yaml.Node, dev bool) error {
	var c Config
	n := host.TopLevel(doc, "firewall")
	if n != nil {
		if err := n.Decode(&c); err != nil {
			return fmt.Errorf("firewall: %w", err)
		}
	}
	var rl RateLimits
	m := host.TopLevel(doc, "rate_limits")
	if m != nil {
		if err := m.Decode(&rl); err != nil {
			return fmt.Errorf("rate_limits: %w", err)
		}
	}
	return f.apply(c, n != nil, rl, m != nil, dev)
}

// apply adds the config file's sections to what was set before (lists add, other fields fill in), runs Adjust and
// builds the firewall.
func (f *Firewall) apply(c Config, haveConfig bool, rl RateLimits, haveLimits bool, dev bool) error {
	if haveConfig {
		f.Config.AllowCIDRs = append(c.AllowCIDRs, f.Config.AllowCIDRs...)
		f.Config.TrustedProxies = append(c.TrustedProxies, f.Config.TrustedProxies...)
		f.Config.TrustedClients = append(c.TrustedClients, f.Config.TrustedClients...)
		if f.Config.ClientIPHeaders == nil {
			f.Config.ClientIPHeaders = c.ClientIPHeaders
		}
		f.Config.TrustedClientHeaders = append(c.TrustedClientHeaders, f.Config.TrustedClientHeaders...)
		if f.Config.RateLimitHeaderPrefix == "" {
			f.Config.RateLimitHeaderPrefix = c.RateLimitHeaderPrefix
		}
	}
	if haveLimits && f.RateLimits == nil {
		f.RateLimits = &rl
	}
	if f.Adjust != nil {
		f.Adjust(&f.Config, &f.RateLimits)
	}
	if dev && f.RateLimits == nil {
		f.RateLimits = &RateLimits{Off: true} // local development: no request budgets unless configured
	}
	return f.setup()
}

// setup builds the firewall from Config and RateLimits, once.
func (f *Firewall) setup() error {
	f.once.Do(func() { f.setupErr = f.build() })
	return f.setupErr
}

func (f *Firewall) build() error {
	cfg := f.Config
	f.headers = cfg.ClientIPHeaders
	f.endUser = cfg.TrustedClientHeaders
	f.prefix = cfg.RateLimitHeaderPrefix
	f.hourly = map[string]*hourlyBucket{}
	f.concurrent = map[string]int64{}
	f.defaults = DefaultRateLimits
	if f.RateLimits != nil {
		f.defaults = f.RateLimits.merged(DefaultRateLimits)
	}
	var err error
	if f.allow, err = parsePrefixes("firewall.allow_cidrs", cfg.AllowCIDRs); err != nil {
		return err
	}
	if f.proxies, err = parsePrefixes("firewall.trusted_proxies", cfg.TrustedProxies); err != nil {
		return err
	}
	if f.trusted, err = parsePrefixes("firewall.trusted_clients", cfg.TrustedClients); err != nil {
		return err
	}
	if f.headers == nil {
		f.headers = DefaultClientIPHeaders
	}
	if len(f.endUser) == 0 {
		f.endUser = DefaultTrustedClientHeaders
	}
	if f.prefix == "" {
		f.prefix = "X-RateLimit"
	}
	var names []string
	for _, n := range rateHeaderNames {
		names = append(names, f.prefix+"-"+n)
	}
	f.exposeAll = strings.Join(names, ", ")
	return nil
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

// AllowPeer reports whether the connecting peer may talk to the server at all.
func (f *Firewall) AllowPeer(r *http.Request) bool {
	if f.setup() != nil {
		return false
	}
	return len(f.allow) == 0 || containsAddr(f.allow, host.PeerAddr(r))
}

// Admit applies the instance's rate limits (its `plugins.firewall` over the defaults). Over a limit it answers 429
// itself; otherwise the returned function frees the concurrency slots when the response ends.
func (f *Firewall) Admit(w http.ResponseWriter, r *http.Request, inst host.Instance) (func(), bool) {
	if err := f.setup(); err != nil {
		http.Error(w, "firewall misconfigured", http.StatusInternalServerError)
		return nil, false
	}
	var ic InstanceConfig
	if err := host.DecodePluginConfig(inst.PluginConfig("firewall"), &ic); err != nil {
		ic = InstanceConfig{}
	}
	rl := RateLimits{}
	if ic.RateLimits != nil {
		rl = *ic.RateLimits
	}
	rl = rl.merged(f.defaults)
	return f.rateCheck(w, r, inst.ID, rl, ic.TrustedIPs)
}

// ClientIP is the request's client: from the first configured header when the peer is a trusted proxy, otherwise
// the peer itself.
func (f *Firewall) ClientIP(r *http.Request) netip.Addr {
	_ = f.setup()
	peer := host.PeerAddr(r)
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

// endUserIP is the end user a trusted client names in a trusted_client_headers header (first valid IP), else client.
func (f *Firewall) endUserIP(r *http.Request, client netip.Addr) netip.Addr {
	for _, h := range f.endUser {
		v := strings.TrimSpace(strings.Split(r.Header.Get(h), ",")[0])
		if v == "" {
			continue
		}
		if a, err := netip.ParseAddr(v); err == nil {
			return a.Unmap()
		}
	}
	return client
}

// rateCheck consumes the request's weight from the instance's buckets. When a limit is exceeded it writes the 429 and
// returns ok=false; otherwise it returns a release function for the concurrency slots (call when the response ends).
func (f *Firewall) rateCheck(w http.ResponseWriter, r *http.Request, instID string, rl RateLimits, trustedIPs []string) (release func(), ok bool) {
	if rl.Off {
		return func() {}, true
	}
	client := f.ClientIP(r)
	trusted := containsAddr(f.trusted, client)
	if !trusted && len(trustedIPs) > 0 {
		if list, err := parsePrefixes("plugins.firewall.trusted_ips", trustedIPs); err == nil {
			trusted = containsAddr(list, client)
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

	// a trusted client may name its end user: per-IP buckets are then that user's (still with the trusted limits)
	user := client
	if trusted {
		user = f.endUserIP(r, client)
	}
	ipKey := user.String() + "|" + instID
	instKey := instID
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
		if user != client {
			return fmt.Sprintf("IP %s (via trusted client %s)", user, client)
		}
		if peer := host.PeerAddr(r); peer != client {
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
			set.IPHourly, who(), instID, retryIn(ipH)), http.StatusTooManyRequests)
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
			set.InstanceHourly, instID, retryIn(instH)), http.StatusTooManyRequests)
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
			set.IPConcurrent, who(), instID), http.StatusTooManyRequests)
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
			set.InstanceConcurrent, instID), http.StatusTooManyRequests)
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

// RateLimits are request limits per client IP and per instance: an hourly budget and a concurrency cap, each with a
// higher value for trusted clients. Requests to /api/files cost FileWeight of a request.
type RateLimits struct {
	IPHourly           int     `yaml:"ip_hourly,omitempty"`
	InstanceHourly     int     `yaml:"instance_hourly,omitempty"`
	IPConcurrent       int     `yaml:"ip_concurrent,omitempty"`
	InstanceConcurrent int     `yaml:"instance_concurrent,omitempty"`
	FileWeight         float64 `yaml:"file_weight,omitempty"`
	// Trusted are the limits for trusted clients; zero fields fall back to the defaults' trusted values.
	Trusted *RateLimitSet `yaml:"trusted,omitempty"`
	// Off disables rate limiting for the instance.
	Off bool `yaml:"off,omitempty"`
}

// RateLimitSet is one set of the four limits.
type RateLimitSet struct {
	IPHourly           int `yaml:"ip_hourly,omitempty"`
	InstanceHourly     int `yaml:"instance_hourly,omitempty"`
	IPConcurrent       int `yaml:"ip_concurrent,omitempty"`
	InstanceConcurrent int `yaml:"instance_concurrent,omitempty"`
}

// DefaultRateLimits match the PocketHost edge firewall.
var DefaultRateLimits = RateLimits{
	IPHourly: 1000, InstanceHourly: 10000, IPConcurrent: 15, InstanceConcurrent: 250, FileWeight: 0.1,
	Trusted: &RateLimitSet{IPHourly: 5000, InstanceHourly: 20000, IPConcurrent: 50, InstanceConcurrent: 250},
}

func pickLimit(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

// merged returns r with zero fields taken from def.
func (r RateLimits) merged(def RateLimits) RateLimits {
	if r.Off || def.Off && r == (RateLimits{}) {
		return RateLimits{Off: true}
	}
	out := RateLimits{
		IPHourly:           pickLimit(r.IPHourly, def.IPHourly),
		InstanceHourly:     pickLimit(r.InstanceHourly, def.InstanceHourly),
		IPConcurrent:       pickLimit(r.IPConcurrent, def.IPConcurrent),
		InstanceConcurrent: pickLimit(r.InstanceConcurrent, def.InstanceConcurrent),
		FileWeight:         r.FileWeight,
	}
	if out.FileWeight <= 0 {
		out.FileWeight = def.FileWeight
	}
	dt := RateLimitSet{}
	if def.Trusted != nil {
		dt = *def.Trusted
	}
	rt := RateLimitSet{}
	if r.Trusted != nil {
		rt = *r.Trusted
	}
	out.Trusted = &RateLimitSet{
		IPHourly:           pickLimit(rt.IPHourly, dt.IPHourly),
		InstanceHourly:     pickLimit(rt.InstanceHourly, dt.InstanceHourly),
		IPConcurrent:       pickLimit(rt.IPConcurrent, dt.IPConcurrent),
		InstanceConcurrent: pickLimit(rt.InstanceConcurrent, dt.InstanceConcurrent),
	}
	// a raised normal limit also lifts the trusted one (trusted is never lower)
	out.Trusted.IPHourly = max(out.Trusted.IPHourly, out.IPHourly)
	out.Trusted.InstanceHourly = max(out.Trusted.InstanceHourly, out.InstanceHourly)
	out.Trusted.IPConcurrent = max(out.Trusted.IPConcurrent, out.IPConcurrent)
	out.Trusted.InstanceConcurrent = max(out.Trusted.InstanceConcurrent, out.InstanceConcurrent)
	return out
}

// Section and RateLimitsSection are the firewall's pocketbase.yaml sections (ext/config).
var (
	Section           = ceplugin.NewSection("firewall", Config{}, "Peers allowed to connect, trusted proxies and client-IP headers, trusted clients, the rate-limit header prefix. Turns the firewall on.")
	RateLimitsSection = ceplugin.NewSection("rate_limits", RateLimits{}, "Default rate limits for every instance (per instance: plugins.firewall.rate_limits). Turns the firewall on.")
)

// SectionAdjust, if set, runs on a firewall built from config sections, like [Firewall.Adjust] (e.g. a host
// application adding settings of its own).
var SectionAdjust func(cfg *Config, defaults **RateLimits)

// FromSections builds a firewall from the `firewall` and `rate_limits` sections; enabled is false when neither is in
// the config file.
func FromSections(dev bool) (fw *Firewall, enabled bool, err error) {
	c, haveConfig := Section.Get()
	rl, haveLimits := RateLimitsSection.Get()
	if !haveConfig && !haveLimits {
		return nil, false, nil
	}
	fw = &Firewall{Adjust: SectionAdjust}
	return fw, true, fw.apply(c, haveConfig, rl, haveLimits, dev)
}

// Linking this package makes the firewall available to every host (ext/host): it turns on when the config has a
// `firewall` or `rate_limits` section (pocketbase.yaml through ext/config, or a pocker.yaml document).
func init() {
	host.RegisterGate("firewall", []string{"firewall", "rate_limits"}, func() host.ConfigurableGate { return &Firewall{} })
	host.RegisterSectionGate(host.SectionGate{Name: "firewall", New: func(dev bool) (host.Gate, bool, error) {
		fw, enabled, err := FromSections(dev)
		if fw == nil {
			return nil, enabled, err
		}
		return fw, enabled, err
	}})
}

var _ host.ConfigurableGate = (*Firewall)(nil)
