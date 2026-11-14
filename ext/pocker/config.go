package pocker

import (
	"time"
)

// Config is Pocker's process configuration, set in Go (a host application) or from the `pocker` section through
// [Provider]. Every field has a default ([DefaultConfig]).
type Config struct {
	// Listen is the HTTP address (default ":8090").
	Listen string

	// PlainListen are extra plain-HTTP listeners for the same instances, without TLS or the HTTPS redirect, e.g. on
	// a private network where other servers forward requests (see [Instance.Remote]).
	PlainListen []string

	// PBRoot holds one PocketBase folder per instance: <pb_root>/<instance id> (default "./pb_root").
	PBRoot string

	// Dev serves any hostname: a host the instance source doesn't map uses the hostname itself, in normal form, as
	// its id ([DevID]) and its folder is created on first use. Apps also run in PocketBase dev mode. Without Dev, only
	// hosts mapped by the instance source are served.
	Dev bool

	// InstancesFile is read by the default YAML instance source (default "./instances.yaml"). Required unless Dev or
	// a host application provides its own source.
	InstancesFile string

	// HooksPoolSize is the number of prewarmed JS runtimes per app (default 2; 0 = one runtime per hook run).
	HooksPoolSize *int

	// ShutdownTimeout bounds graceful shutdown of in-flight requests (default 10s).
	ShutdownTimeout time.Duration

	// Limits are the defaults for every instance; an instance's own limits (from the instance source) override
	// them field by field. Keys: max_js_execution, max_js_allocation, max_call_stack_size, max_sleep,
	// max_query_duration, http_send_max_timeout, http_send_max_body.
	Limits

	// EgressAllow lists private networks apps may still connect to (default none: public addresses only).
	EgressAllow []string

	// TLS: certificate files for HTTPS, and an optional HTTP redirect listener. (Automatic certificates are the
	// autotls plugin, with its own `autotls` section.)
	TLS TLSConfig
}

// DefaultConfig returns the defaults.
func DefaultConfig() Config {
	pool := 2
	return Config{
		Listen:          ":8090",
		PBRoot:          "./pb_root",
		InstancesFile:   "./instances.yaml",
		HooksPoolSize:   &pool,
		ShutdownTimeout: 10 * time.Second,
		Limits:          DefaultLimits(),
	}
}

// DefaultLimits are the built-in per-instance limits.
func DefaultLimits() Limits {
	return Limits{
		MaxJSExecution:     30 * time.Second,
		MaxJSAllocation:    64 << 20,
		MaxCallStackSize:   2000,
		MaxSleep:           10 * time.Second,
		MaxQueryDuration:   30 * time.Second,
		HTTPSendMaxTimeout: 60 * time.Second,
		HTTPSendMaxBody:    10 << 20,
		MaxConcurrentJS:    16,
	}
}
