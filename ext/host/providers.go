package host

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"

	"github.com/pockethost/pocketone/ext/ceplugin"
	"github.com/pockethost/pocketone/ext/config"
)

// Provider is a runtime provider: it turns a host into a multi-instance server by supplying its one runtime
// (ext/pocker in-process, ext/docker in containers). A binary registers the providers it links; the config's
// `provider` section picks one.
type Provider interface {
	// Name is the value of `provider:` that selects it ("pocker", "docker").
	Name() string
	// Prepare reads the provider's own config section and fills in h.Runtime (and may add wrappers, TLS, background
	// work, OnListen/OnShutdown). h's Source, Directory, Gates, Forward, Dev, Listeners and Logger are already set.
	Prepare(h *Host) error
}

var (
	provMu    sync.Mutex
	providers = map[string]Provider{}
)

// RegisterProvider makes a provider selectable by name; registering a name twice replaces the earlier one.
func RegisterProvider(p Provider) {
	provMu.Lock()
	defer provMu.Unlock()
	providers[p.Name()] = p
}

// Providers lists the registered provider names.
func Providers() []string {
	provMu.Lock()
	defer provMu.Unlock()
	out := make([]string, 0, len(providers))
	for n := range providers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// SectionGate is a gate configured from its own config sections (e.g. ext/firewall's `firewall` / `rate_limits`):
// linking its package registers it, and FromConfig turns it on when New reports it enabled.
type SectionGate struct {
	Name string
	New  func(dev bool) (g Gate, enabled bool, err error)
}

var (
	sgMu         sync.Mutex
	sectionGates []SectionGate
)

// RegisterSectionGate registers a gate configured from config sections.
func RegisterSectionGate(g SectionGate) {
	sgMu.Lock()
	defer sgMu.Unlock()
	sectionGates = append(sectionGates, g)
}

// InstancesConfig is the `instances` section: where a host's instances come from when the binary doesn't supply a
// source of its own (e.g. a control plane's mirror).
//
//	instances:
//	  file: instances.yaml   # instances: [{id, hosts, version, …}]
//	  dev: false             # also serve unknown hostnames (folder created on first use)
type InstancesConfig struct {
	File string `yaml:"file"`
	Dev  bool   `yaml:"dev"`
}

// Instances is the section key of InstancesConfig.
var Instances = ceplugin.NewSection("instances", InstancesConfig{File: "instances.yaml"}, "Where a multi-instance host's instances come from (a YAML file), and dev mode (serve unknown hostnames).")

// Options are what the binary adds to a host built from config.
type Options struct {
	Source     InstanceSource                                        // nil = the `instances` section's file
	Forward    func(w http.ResponseWriter, r *http.Request, inst Instance) // the fleet hook (ext/edge)
	Gates      []Gate                                                // after the section gates
	Wrappers   []func(http.Handler) http.Handler                     // outermost first
	Logger     *slog.Logger
	// Resolve makes a relative path from the config file relative to the file's folder (ext/config's
	// Config.ResolvePath); nil = paths as written.
	Resolve func(string) string
}

// ErrNoProvider means the config names no provider: the binary should serve a single app instead.
var ErrNoProvider = errors.New("no runtime provider configured")

// FromConfig builds a host from the loaded config sections: the provider named in `provider`, listeners from
// `server`, instances from `instances` (unless o.Source), and every section gate that is enabled. It returns
// ErrNoProvider when `provider` is empty, and an error when it names a provider this binary doesn't link.
func FromConfig(o Options) (*Host, error) {
	name, _ := config.Provider.Get()
	if name == "" {
		return nil, ErrNoProvider
	}
	provMu.Lock()
	p, ok := providers[name]
	provMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("provider %q is not linked in this binary (linked: %v)", name, Providers())
	}
	if app, found := config.App.Get(); found && (app.Dir != "" || app.HooksDir != "" || app.MigrationsDir != "" || app.PublicDir != "") {
		return nil, errors.New("app: applies to a single app; with a provider each instance has its own folder under the provider's root")
	}
	resolve := o.Resolve
	if resolve == nil {
		resolve = func(s string) string { return s }
	}
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	srv, _ := config.Server.Get()
	inst, _ := Instances.Get()
	h := &Host{Logger: log, Dev: inst.Dev, Forward: o.Forward, Wrappers: o.Wrappers}
	h.Listeners = Listeners{
		Listen:          srv.HTTPS,
		PlainListen:     srv.Plain,
		TLS:             TLSConfig{Cert: resolve(srv.TLS.Cert), Key: resolve(srv.TLS.Key)},
		ShutdownTimeout: srv.ShutdownTimeout,
	}
	switch {
	case srv.HTTPS != "" && srv.HTTP != "":
		h.Listeners.TLS.HTTPListen = srv.HTTP // plain HTTP redirects to HTTPS
	case srv.HTTPS == "":
		h.Listeners.Listen = srv.HTTP // plain HTTP only
	}
	if h.Listeners.Listen == "" {
		h.Listeners.Listen = "127.0.0.1:8090"
	}
	h.Source = o.Source
	if h.Source == nil {
		h.Source = &YAMLSource{Path: resolve(inst.File), Required: !inst.Dev}
	}
	sgMu.Lock()
	sgs := append([]SectionGate(nil), sectionGates...)
	sgMu.Unlock()
	sort.SliceStable(sgs, func(i, j int) bool { return sgs[i].Name < sgs[j].Name })
	for _, sg := range sgs {
		g, enabled, err := sg.New(inst.Dev)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", sg.Name, err)
		}
		if enabled {
			h.Gates = append(h.Gates, g)
		}
	}
	h.Gates = append(h.Gates, o.Gates...)
	h.Directory = NewDirectory(log)
	if err := p.Prepare(h); err != nil {
		return nil, fmt.Errorf("provider %s: %w", name, err)
	}
	if h.Runtime == nil {
		return nil, fmt.Errorf("provider %s set no runtime", name)
	}
	return h, nil
}
