package pocker

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Limits are per-instance resource limits. Zero fields fall back to the process defaults ([Config]); an instance
// source sets them per instance (e.g. by plan).
//
//	limits:
//	  max_js_execution: 60s
//	  max_js_allocation: 128MiB
//	  max_query_duration: 60s
//	  http_send_max_timeout: 30s
//	  http_send_max_body: 20MiB
//	  quarantine: { strikes: 5, first_pause: 30s, max_pause: 15m }   # or: quarantine: off
type Limits struct {
	MaxJSExecution     time.Duration     `yaml:"max_js_execution,omitempty"`
	MaxJSAllocation    ByteSize          `yaml:"max_js_allocation,omitempty"`
	MaxCallStackSize   int               `yaml:"max_call_stack_size,omitempty"`
	MaxSleep           time.Duration     `yaml:"max_sleep,omitempty"`
	MaxQueryDuration   time.Duration     `yaml:"max_query_duration,omitempty"`
	HTTPSendMaxTimeout time.Duration     `yaml:"http_send_max_timeout,omitempty"`
	HTTPSendMaxBody    ByteSize          `yaml:"http_send_max_body,omitempty"`
	Quarantine         *QuarantinePolicy `yaml:"quarantine,omitempty"`

	// MaxConcurrentJS caps how many JS calls (hooks, routes, cron jobs) the instance runs at once; further calls
	// wait up to max_js_execution for a slot, then fail.
	MaxConcurrentJS int `yaml:"max_concurrent_js,omitempty"`

	// RateLimits are the firewall's request limits for the instance (see [RateLimits]).
	RateLimits *RateLimits `yaml:"rate_limits,omitempty"`
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

func pick(v, def int) int {
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
		IPHourly:           pick(r.IPHourly, def.IPHourly),
		InstanceHourly:     pick(r.InstanceHourly, def.InstanceHourly),
		IPConcurrent:       pick(r.IPConcurrent, def.IPConcurrent),
		InstanceConcurrent: pick(r.InstanceConcurrent, def.InstanceConcurrent),
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
		IPHourly:           pick(rt.IPHourly, dt.IPHourly),
		InstanceHourly:     pick(rt.InstanceHourly, dt.InstanceHourly),
		IPConcurrent:       pick(rt.IPConcurrent, dt.IPConcurrent),
		InstanceConcurrent: pick(rt.InstanceConcurrent, dt.InstanceConcurrent),
	}
	// a raised normal limit also lifts the trusted one (trusted is never lower)
	out.Trusted.IPHourly = max(out.Trusted.IPHourly, out.IPHourly)
	out.Trusted.InstanceHourly = max(out.Trusted.InstanceHourly, out.InstanceHourly)
	out.Trusted.IPConcurrent = max(out.Trusted.IPConcurrent, out.IPConcurrent)
	out.Trusted.InstanceConcurrent = max(out.Trusted.InstanceConcurrent, out.InstanceConcurrent)
	return out
}

// QuarantinePolicy decides when repeated memory strikes pause an instance. Pauses back off: FirstPause, doubling on
// each further quarantine up to MaxPause, and the backoff resets after ResetAfter without strikes.
type QuarantinePolicy struct {
	Off        bool          `yaml:"off,omitempty"` // never pause; only interrupt
	Strikes    int           `yaml:"strikes,omitempty"`
	Window     time.Duration `yaml:"window,omitempty"`
	FirstPause time.Duration `yaml:"first_pause,omitempty"`
	MaxPause   time.Duration `yaml:"max_pause,omitempty"`
	ResetAfter time.Duration `yaml:"reset_after,omitempty"`
}

// UnmarshalYAML also accepts `quarantine: off` (or false).
func (q *QuarantinePolicy) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		switch strings.ToLower(n.Value) {
		case "off", "false", "no":
			*q = QuarantinePolicy{Off: true}
			return nil
		case "on", "true", "yes", "":
			*q = QuarantinePolicy{}
			return nil
		}
		return fmt.Errorf("quarantine: expected off or a mapping, got %q", n.Value)
	}
	type plain QuarantinePolicy
	return n.Decode((*plain)(q))
}

// DefaultQuarantine is used where neither the instance nor the process sets a field.
var DefaultQuarantine = QuarantinePolicy{
	Strikes:    3,
	Window:     10 * time.Minute,
	FirstPause: 30 * time.Second,
	MaxPause:   15 * time.Minute,
	ResetAfter: time.Hour,
}

// merged returns q with zero fields taken from def.
func (q QuarantinePolicy) merged(def QuarantinePolicy) QuarantinePolicy {
	if q.Off || def.Off && q == (QuarantinePolicy{}) {
		return QuarantinePolicy{Off: true}
	}
	if q.Strikes <= 0 {
		q.Strikes = def.Strikes
	}
	if q.Window <= 0 {
		q.Window = def.Window
	}
	if q.FirstPause <= 0 {
		q.FirstPause = def.FirstPause
	}
	if q.MaxPause <= 0 {
		q.MaxPause = def.MaxPause
	}
	if q.ResetAfter <= 0 {
		q.ResetAfter = def.ResetAfter
	}
	return q
}

// pauseFor returns the pause for the n-th quarantine (0-based): FirstPause doubling up to MaxPause.
func (q QuarantinePolicy) pauseFor(n int) time.Duration {
	d := q.FirstPause
	for i := 0; i < n && d < q.MaxPause; i++ {
		d *= 2
	}
	if d > q.MaxPause {
		d = q.MaxPause
	}
	return d
}

// over returns l with zero fields taken from def.
func (l Limits) over(def Limits) Limits {
	if l.MaxJSExecution == 0 {
		l.MaxJSExecution = def.MaxJSExecution
	}
	if l.MaxJSAllocation == 0 {
		l.MaxJSAllocation = def.MaxJSAllocation
	}
	if l.MaxCallStackSize == 0 {
		l.MaxCallStackSize = def.MaxCallStackSize
	}
	if l.MaxSleep == 0 {
		l.MaxSleep = def.MaxSleep
	}
	if l.MaxQueryDuration == 0 {
		l.MaxQueryDuration = def.MaxQueryDuration
	}
	if l.HTTPSendMaxTimeout == 0 {
		l.HTTPSendMaxTimeout = def.HTTPSendMaxTimeout
	}
	if l.HTTPSendMaxBody == 0 {
		l.HTTPSendMaxBody = def.HTTPSendMaxBody
	}
	if l.MaxConcurrentJS == 0 {
		l.MaxConcurrentJS = def.MaxConcurrentJS
	}
	defR := DefaultRateLimits
	if def.RateLimits != nil {
		defR = def.RateLimits.merged(DefaultRateLimits)
	}
	rl := RateLimits{}
	if l.RateLimits != nil {
		rl = *l.RateLimits
	}
	rl = rl.merged(defR)
	l.RateLimits = &rl

	defQ := DefaultQuarantine
	if def.Quarantine != nil {
		defQ = def.Quarantine.merged(DefaultQuarantine)
	}
	q := QuarantinePolicy{}
	if l.Quarantine != nil {
		q = *l.Quarantine
	}
	q = q.merged(defQ)
	l.Quarantine = &q
	return l
}

// ByteSize is a size in bytes that also reads "64MiB", "10MB", "512KiB" or a plain number from YAML.
type ByteSize int64

func (b *ByteSize) UnmarshalYAML(n *yaml.Node) error {
	v, err := ParseByteSize(n.Value)
	if err != nil {
		return err
	}
	*b = v
	return nil
}

// ParseByteSize parses a plain byte count or a number with a B, KB, KiB, MB, MiB, GB or GiB suffix.
func ParseByteSize(s string) (ByteSize, error) {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix string
		mult   int64
	}{{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"KB", 1000}, {"MB", 1000 * 1000}, {"GB", 1000 * 1000 * 1000}, {"B", 1}}
	mult := int64(1)
	for _, u := range units {
		if strings.HasSuffix(strings.ToUpper(s), strings.ToUpper(u.suffix)) {
			s = strings.TrimSpace(s[:len(s)-len(u.suffix)])
			mult = u.mult
			break
		}
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return ByteSize(n * float64(mult)), nil
}
