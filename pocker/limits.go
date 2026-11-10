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
