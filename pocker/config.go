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
//	data_root: "./pocker_data"
//	instances_file: "./instances.yaml"
//	hooks_pool_size: 2
type Config struct {
	// Listen is the HTTP address (default ":8090").
	Listen string `yaml:"listen"`
	// DataRoot holds instance folders: <data_root>/<instance id> (default "./pocker_data").
	DataRoot string `yaml:"data_root"`
	// InstancesFile is read by the default YAML instance source (default "./instances.yaml").
	InstancesFile string `yaml:"instances_file"`
	// HooksPoolSize is the number of prewarmed JS runtimes per app (default 2; 0 = one runtime per hook run).
	HooksPoolSize *int `yaml:"hooks_pool_size"`
	// ShutdownTimeout bounds graceful shutdown of in-flight requests (default 10s).
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// DefaultConfig returns the defaults used when no config file (or field) is given.
func DefaultConfig() Config {
	pool := 2
	return Config{
		Listen:          ":8090",
		DataRoot:        "./pocker_data",
		InstancesFile:   "./instances.yaml",
		HooksPoolSize:   &pool,
		ShutdownTimeout: 10 * time.Second,
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
	if fromFile.DataRoot != "" {
		cfg.DataRoot = fromFile.DataRoot
	}
	if fromFile.InstancesFile != "" {
		cfg.InstancesFile = fromFile.InstancesFile
	}
	if fromFile.HooksPoolSize != nil {
		cfg.HooksPoolSize = fromFile.HooksPoolSize
	}
	if fromFile.ShutdownTimeout > 0 {
		cfg.ShutdownTimeout = fromFile.ShutdownTimeout
	}
	return cfg, nil
}
