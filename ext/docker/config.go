package docker

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/pockethost/pocketone/ext/host"
	"gopkg.in/yaml.v3"
)

// FileConfig is a config file's `docker:` section.
//
//	docker:
//	  image: alpine:3.20                    # base image the binaries run in
//	  binaries: /var/lib/pocketbase         # binary cache (<binaries>/<version>/linux_amd64/pocketbase; PocketOne under ce/)
//	  manifest: /etc/pocketbase/versions.json  # releases it may download (optional)
//	  data_root: /var/lib/instances         # default: pb_root
//	  default_version: "pb:0.36.*"          # for instances without one (pb: upstream, ce: PocketOne)
//	  idle_ttl: 10m
//	  max_concurrent_starts: 4
//	  memory: 512MiB
//	  pids_limit: 256
//	  nofile: [4096, 8192]
//	  cap_drop: [ALL]
//	  no_new_privileges: true
//	  adopt_image: ""                       # also adopt unlabeled containers of this image (migrations)
type FileConfig struct {
	Socket              string        `yaml:"socket"`
	Image               string        `yaml:"image"`
	Binaries            string        `yaml:"binaries"`
	Manifest            string        `yaml:"manifest"`
	DataRoot            string        `yaml:"data_root"`
	Home                string        `yaml:"home"`
	DefaultVersion      string        `yaml:"default_version"`
	IdleTTL             time.Duration `yaml:"idle_ttl"`
	MaxConcurrentStarts int           `yaml:"max_concurrent_starts"`
	StartTimeout        time.Duration `yaml:"start_timeout"`
	Memory              host.ByteSize `yaml:"memory"`
	NanoCPUs            int64         `yaml:"nano_cpus"`
	PidsLimit           int64         `yaml:"pids_limit"`
	Nofile              []int64       `yaml:"nofile"`
	CapDrop             []string      `yaml:"cap_drop"`
	NoNewPrivileges     bool          `yaml:"no_new_privileges"`
	Network             string        `yaml:"network"`
	AdoptImage          string        `yaml:"adopt_image"`
}

// FromConfig returns the Config for a config document's `docker:` section (ok false when it has none). pbRoot is
// the default data root.
func FromConfig(doc *yaml.Node, pbRoot string, log *slog.Logger) (cfg Config, ok bool, err error) {
	n := host.TopLevel(doc, "docker")
	if n == nil {
		return Config{}, false, nil
	}
	var fc FileConfig
	if err := n.Decode(&fc); err != nil {
		return Config{}, false, fmt.Errorf("docker: %w", err)
	}
	if fc.Image == "" || fc.Binaries == "" {
		return Config{}, false, fmt.Errorf("docker: image and binaries are required")
	}
	b := &Binaries{Root: fc.Binaries}
	if fc.Manifest != "" {
		if b.Manifest, err = LoadManifest(fc.Manifest); err != nil {
			return Config{}, false, fmt.Errorf("docker: %w", err)
		}
	}
	if fc.DataRoot == "" {
		fc.DataRoot = pbRoot
	}
	cfg = Config{
		Socket: fc.Socket, Image: fc.Image, DataRoot: fc.DataRoot, Home: fc.Home, Binaries: b,
		DefaultVersion: fc.DefaultVersion, IdleTTL: fc.IdleTTL, MaxConcurrentStarts: fc.MaxConcurrentStarts,
		StartTimeout: fc.StartTimeout, Memory: int64(fc.Memory), NanoCPUs: fc.NanoCPUs, PidsLimit: fc.PidsLimit,
		CapDrop: fc.CapDrop, NoNewPrivileges: fc.NoNewPrivileges, Network: fc.Network, AdoptImage: fc.AdoptImage,
		Logger: log,
	}
	if len(fc.Nofile) > 0 {
		cfg.NofileSoft, cfg.NofileHard = fc.Nofile[0], fc.Nofile[len(fc.Nofile)-1]
	}
	return cfg, true, nil
}
