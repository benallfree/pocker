package pocker

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is Pocker's process configuration. Every field has a default, so no config file is required.
//
//	listen: ":8090"
//	pb_root: "./pb_root"
//	instances_file: "./instances.yaml"
//	hooks_pool_size: 2
type Config struct {
	// Listen is the HTTP address (default ":8090").
	Listen string `yaml:"listen"`

	// PlainListen are extra plain-HTTP listeners for the same instances, without TLS or the HTTPS redirect, e.g. on
	// a private network where other servers forward requests (see [Instance.Remote]).
	PlainListen []string `yaml:"plain_listen"`

	// PBRoot holds one PocketBase folder per instance: <pb_root>/<instance id> (default "./pb_root").
	PBRoot string `yaml:"pb_root"`

	// Dev serves any hostname: a host the instance source doesn't map uses the hostname itself, in normal form, as
	// its id ([DevID]) and its folder is created on first use. Apps also run in PocketBase dev mode. Without Dev, only
	// hosts mapped by the instance source are served.
	Dev bool `yaml:"dev"`

	// InstancesFile is read by the default YAML instance source (default "./instances.yaml"). Required unless Dev or
	// a host application provides its own source.
	InstancesFile string `yaml:"instances_file"`

	// HooksPoolSize is the number of prewarmed JS runtimes per app (default 2; 0 = one runtime per hook run).
	HooksPoolSize *int `yaml:"hooks_pool_size"`

	// ShutdownTimeout bounds graceful shutdown of in-flight requests (default 10s).
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`

	// Limits are the defaults for every instance; an instance's own limits (from the instance source) override
	// them field by field. Keys: max_js_execution, max_js_allocation, max_call_stack_size, max_sleep,
	// max_query_duration, http_send_max_timeout, http_send_max_body, quarantine.
	Limits `yaml:",inline"`

	// EgressAllow lists private networks apps may still connect to (default none: public addresses only).
	EgressAllow []string `yaml:"egress_allow"`

	// TLS: certificate files for HTTPS, and an optional HTTP redirect listener. (Automatic certificates are the
	// autotls plugin, which reads its own keys under `tls:`.)
	TLS TLSConfig `yaml:"tls"`

	// MemoryLimit sets the Go runtime's soft memory limit (GOMEMLIMIT) in bytes (default 0 = leave as is).
	MemoryLimit int64 `yaml:"memory_limit"`

	// MemorySoftLimit is the live-heap level at which the memory watch starts interrupting long JS calls and
	// striking their instances (default 70% of memory_limit, or 1 GiB without one; negative disables the watch).
	MemorySoftLimit int64 `yaml:"memory_soft_limit"`

	// doc is the whole config file, for plugins that own their own keys (see [Configurer]).
	doc *yaml.Node
}

// Doc returns the parsed config file (nil when there was none), for plugins that read their own keys.
func (c Config) Doc() *yaml.Node { return c.doc }

// DefaultConfig returns the defaults used when no config file (or field) is given.
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

// LoadConfig reads a YAML config file over the defaults. A missing file is not an error.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		return cfg, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	var fromFile Config
	if err := yaml.Unmarshal(raw, &fromFile); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	if fromFile.Listen != "" {
		cfg.Listen = fromFile.Listen
	}
	if fromFile.PlainListen != nil {
		cfg.PlainListen = fromFile.PlainListen
	}
	if fromFile.PBRoot != "" {
		cfg.PBRoot = fromFile.PBRoot
	}
	cfg.Dev = cfg.Dev || fromFile.Dev
	if fromFile.InstancesFile != "" {
		cfg.InstancesFile = fromFile.InstancesFile
	}
	if fromFile.HooksPoolSize != nil {
		cfg.HooksPoolSize = fromFile.HooksPoolSize
	}
	if fromFile.ShutdownTimeout > 0 {
		cfg.ShutdownTimeout = fromFile.ShutdownTimeout
	}
	cfg.Limits = fromFile.Limits.over(cfg.Limits)
	cfg.TLS = fromFile.TLS
	if fromFile.EgressAllow != nil {
		cfg.EgressAllow = fromFile.EgressAllow
	}
	if fromFile.MemoryLimit > 0 {
		cfg.MemoryLimit = fromFile.MemoryLimit
	}
	if fromFile.MemorySoftLimit != 0 {
		cfg.MemorySoftLimit = fromFile.MemorySoftLimit
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err == nil && len(doc.Content) > 0 {
		cfg.doc = &doc
	}
	return cfg, nil
}

// DefaultLimits are the built-in per-instance limits.
func DefaultLimits() Limits {
	q := DefaultQuarantine
	return Limits{
		MaxJSExecution:     30 * time.Second,
		MaxJSAllocation:    64 << 20,
		MaxCallStackSize:   2000,
		MaxSleep:           10 * time.Second,
		MaxQueryDuration:   30 * time.Second,
		HTTPSendMaxTimeout: 60 * time.Second,
		HTTPSendMaxBody:    10 << 20,
		Quarantine:         &q,
		MaxConcurrentJS:    16,
	}
}
