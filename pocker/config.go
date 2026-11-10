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

	// MaxJSExecution interrupts any single JS call (hook, route, cron job, migration) that runs longer (default 30s).
	MaxJSExecution time.Duration `yaml:"max_js_execution"`

	// MaxQueryDuration interrupts any SQL statement without its own deadline (raw SQL from JS) (default 30s).
	MaxQueryDuration time.Duration `yaml:"max_query_duration"`
}

// DefaultConfig returns the defaults used when no config file (or field) is given.
func DefaultConfig() Config {
	pool := 2
	return Config{
		Listen:           ":8090",
		PBRoot:           "./pb_root",
		InstancesFile:    "./instances.yaml",
		HooksPoolSize:    &pool,
		ShutdownTimeout:  10 * time.Second,
		MaxJSExecution:   30 * time.Second,
		MaxQueryDuration: 30 * time.Second,
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
	if fromFile.MaxJSExecution > 0 {
		cfg.MaxJSExecution = fromFile.MaxJSExecution
	}
	if fromFile.MaxQueryDuration > 0 {
		cfg.MaxQueryDuration = fromFile.MaxQueryDuration
	}
	return cfg, nil
}
