package docker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/pockethost/pocketone/ext/ceplugin"
	"github.com/pockethost/pocketone/ext/host"
)

// SectionConfig is the `docker` section (pocketone.yaml, ext/config): settings for running each instance in its
// own container. Listeners, instances and the firewall are the host's sections.
//
//	docker:
//	  image: alpine:3.20                    # base image the binaries run in
//	  binaries: /var/lib/pocketbase         # cache: <binaries>/<version>/linux_amd64/pocketbase; PocketOne under p1/
//	  versions: ["pb:0.36", "p1:0.0"]       # minors this host serves (latest patch each, via docker update)
//	  root: /var/lib/instances              # one folder per instance: <root>/<id>
//	  default_version: "p1:0.0.*"           # for instances without one (pb: upstream, p1: PocketOne)
//	  idle_ttl: 10m
//	  max_concurrent_starts: 4
//	  pids_limit: 256
//	  nofile: [4096, 8192]
//	  cap_drop: [ALL]
//	  no_new_privileges: true
//	  adopt_image: ""                       # also adopt unlabeled containers of this image (migrations)
type SectionConfig struct {
	Socket              string        `yaml:"socket"`
	Image               string        `yaml:"image"`
	Binaries            string        `yaml:"binaries"`
	Versions            []string      `yaml:"versions"`
	Root                string        `yaml:"root"`
	Home                string        `yaml:"home"`
	DefaultVersion      string        `yaml:"default_version"`
	IdleTTL             time.Duration `yaml:"idle_ttl"`
	MaxConcurrentStarts int           `yaml:"max_concurrent_starts"`
	StartTimeout        time.Duration `yaml:"start_timeout"`
	NanoCPUs            int64         `yaml:"nano_cpus"`
	PidsLimit           int64         `yaml:"pids_limit"`
	Nofile              []int64       `yaml:"nofile"`
	CapDrop             []string      `yaml:"cap_drop"`
	NoNewPrivileges     bool          `yaml:"no_new_privileges"`
	Network             string        `yaml:"network"`
	AdoptImage          string        `yaml:"adopt_image"`
}

// Section is the key of the `docker` section.
var Section = ceplugin.NewSectionFunc("docker", func() SectionConfig { return SectionConfig{} },
	"Runtime provider docker: each instance in its own container with the PocketBase (pb:) or PocketOne (p1:) binary of its version (image, binary cache and manifest, instance root, default version, idle stop, limits).")

// ConfigFromSection builds a Config from a `docker` section. resolve makes relative paths absolute (nil = as is).
func ConfigFromSection(sc SectionConfig, resolve func(string) string, log *slog.Logger) (Config, error) {
	if resolve == nil {
		resolve = func(s string) string { return s }
	}
	if sc.Image == "" || sc.Binaries == "" || sc.Root == "" {
		return Config{}, errors.New("docker: image, binaries and root are required")
	}
	b := &Binaries{Root: resolve(sc.Binaries)}
	cfg := Config{
		Socket: sc.Socket, Image: sc.Image, DataRoot: resolve(sc.Root), Home: sc.Home, Binaries: b,
		Versions:       YAMLVersions{Tracked: sc.Versions, Root: b.Root},
		DefaultVersion: sc.DefaultVersion, IdleTTL: sc.IdleTTL, MaxConcurrentStarts: sc.MaxConcurrentStarts,
		StartTimeout: sc.StartTimeout, NanoCPUs: sc.NanoCPUs, PidsLimit: sc.PidsLimit,
		CapDrop: sc.CapDrop, NoNewPrivileges: sc.NoNewPrivileges, Network: sc.Network, AdoptImage: sc.AdoptImage,
		Logger: log,
	}
	if len(sc.Nofile) > 0 {
		cfg.NofileSoft, cfg.NofileHard = sc.Nofile[0], sc.Nofile[len(sc.Nofile)-1]
	}
	return cfg, nil
}

// Provider is docker as a runtime provider for ext/host (`provider: docker`). Its fields are the host
// application's extensions.
type Provider struct {
	// Container adds host-specific mounts, environment and labels to each container (see [Config.Prepare]).
	Container func(ctx context.Context, inst host.Instance, version string, spec *Spec) error
	// Version, if set, rewrites an instance's version before the binary is picked (e.g. a host's rule for which
	// release line its instances run).
	Version func(inst host.Instance) string
	// OnLog receives container output (see [Config.OnLog]).
	OnLog func(instanceID string, stderr bool, line string)
	// Resolve makes relative paths in the section absolute (e.g. ext/config's Config.ResolvePath).
	Resolve func(string) string
	// Versions, if set, replaces the section's `versions` list (e.g. a fleet's list from a mothership).
	Versions VersionsSource
}

var _ host.Provider = (*Provider)(nil)

// Name implements host.Provider.
func (p *Provider) Name() string { return "docker" }

// Prepare implements host.Provider: it reads the `docker` section and makes h run each instance in a container.
func (p *Provider) Prepare(h *host.Host) error {
	sc, _ := Section.Get()
	cfg, err := ConfigFromSection(sc, p.Resolve, h.Logger)
	if err != nil {
		return err
	}
	cfg.Prepare, cfg.OnLog, cfg.Version = p.Container, p.OnLog, p.Version
	if p.Versions != nil {
		cfg.Versions = p.Versions
	}
	rt, err := New(cfg)
	if err != nil {
		return err
	}
	h.Runtime = rt
	return nil
}
