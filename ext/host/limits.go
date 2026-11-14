package host

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
type Limits struct {
	MaxJSExecution     time.Duration `yaml:"max_js_execution,omitempty"`
	MaxJSAllocation    ByteSize      `yaml:"max_js_allocation,omitempty"`
	MaxCallStackSize   int           `yaml:"max_call_stack_size,omitempty"`
	MaxSleep           time.Duration `yaml:"max_sleep,omitempty"`
	MaxQueryDuration   time.Duration `yaml:"max_query_duration,omitempty"`
	HTTPSendMaxTimeout time.Duration `yaml:"http_send_max_timeout,omitempty"`
	HTTPSendMaxBody    ByteSize      `yaml:"http_send_max_body,omitempty"`

	// MaxConcurrentJS caps how many JS calls (hooks, routes, cron jobs) the instance runs at once; further calls
	// wait up to max_js_execution for a slot, then fail.
	MaxConcurrentJS int `yaml:"max_concurrent_js,omitempty"`
}

// over returns l with zero fields taken from def.
func (l Limits) Over(def Limits) Limits {
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
