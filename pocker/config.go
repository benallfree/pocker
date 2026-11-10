package pocker

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is Pocker's process configuration. Every field has a default, so no config file is required.
//
//	listen: ":8090"
//	pb_root: "./pb_root"
//	instances_file: "./instances.yaml"
//	trusted_proxies: ["127.0.0.1/32", "::1/128"]
//	hooks_pool_size: 2
type Config struct {
	// Listen is the HTTP address (default ":8090").
	Listen string `yaml:"listen"`

	// PBRoot holds one PocketBase folder per instance: <pb_root>/<instance id> (default "./pb_root").
	PBRoot string `yaml:"pb_root"`

	// Dev serves any hostname: the instance id is the hostname (or the X-PocketBase-Id header) and its folder is
	// created on first use. Apps also run in PocketBase dev mode. Without Dev, only instances from the instance
	// source are served.
	Dev bool `yaml:"dev"`

	// InstancesFile is read by the default YAML instance source (default "./instances.yaml"). Required unless Dev or
	// a host application provides its own source.
	InstancesFile string `yaml:"instances_file"`

	// TrustedProxies are the networks whose X-PocketBase-Id header is honoured (default loopback only). The header
	// is stripped from every other request.
	TrustedProxies []string `yaml:"trusted_proxies"`

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
		PBRoot:          "./pb_root",
		InstancesFile:   "./instances.yaml",
		TrustedProxies:  []string{"127.0.0.1/32", "::1/128"},
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
	if fromFile.PBRoot != "" {
		cfg.PBRoot = fromFile.PBRoot
	}
	cfg.Dev = cfg.Dev || fromFile.Dev
	if fromFile.InstancesFile != "" {
		cfg.InstancesFile = fromFile.InstancesFile
	}
	if fromFile.TrustedProxies != nil {
		cfg.TrustedProxies = fromFile.TrustedProxies
	}
	if fromFile.HooksPoolSize != nil {
		cfg.HooksPoolSize = fromFile.HooksPoolSize
	}
	if fromFile.ShutdownTimeout > 0 {
		cfg.ShutdownTimeout = fromFile.ShutdownTimeout
	}
	return cfg, nil
}

// parseCIDRs turns the trusted proxy list into networks; a bare IP is treated as a single host.
func parseCIDRs(list []string) ([]*net.IPNet, error) {
	nets := make([]*net.IPNet, 0, len(list))
	for _, s := range list {
		if _, n, err := net.ParseCIDR(s); err == nil {
			nets = append(nets, n)
			continue
		}
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, fmt.Errorf("trusted_proxies: %q is not an IP or CIDR", s)
		}
		bits := 128
		if ip.To4() != nil {
			ip, bits = ip.To4(), 32
		}
		nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	return nets, nil
}
