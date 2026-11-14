package host

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pockethost/pocketone/ext/ceplugin"
	"github.com/pockethost/pocketone/ext/config"
)

type fakeProvider struct{ prepared *Host }

func (p *fakeProvider) Name() string { return "fake" }
func (p *fakeProvider) Prepare(h *Host) error {
	p.prepared = h
	h.Runtime = echoRT{}
	return nil
}

type echoRT struct{}

func (echoRT) Name() string                                            { return "echo" }
func (echoRT) Serves(Instance) bool                                    { return true }
func (echoRT) Ready() bool                                             { return true }
func (echoRT) ServeInstance(w http.ResponseWriter, _ *http.Request, _ Instance) {}

type allowAll struct{}

func (allowAll) AllowPeer(*http.Request) bool { return true }
func (allowAll) Admit(http.ResponseWriter, *http.Request, Instance) (func(), bool) {
	return func() {}, true
}

func loadConfig(t *testing.T, yaml string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "pocketone.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(config.Options{ConfigPath: p})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ceplugin.SetSectionSource(nil) })
	_ = c
}

func TestFromConfig(t *testing.T) {
	fp := &fakeProvider{}
	RegisterProvider(fp)
	gateOn := false
	RegisterSectionGate(SectionGate{Name: "testgate", New: func(dev bool) (Gate, bool, error) { return allowAll{}, gateOn, nil }})

	// no provider: the binary serves a single app
	loadConfig(t, "server: { http: 127.0.0.1:8090 }\n")
	if _, err := FromConfig(Options{}); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("no provider: %v", err)
	}

	// a provider this binary doesn't link
	loadConfig(t, "provider: nope\n")
	if _, err := FromConfig(Options{}); err == nil || !strings.Contains(err.Error(), "not linked") {
		t.Fatalf("unknown provider: %v", err)
	}

	// app: is single-app only
	loadConfig(t, "provider: fake\napp: { dir: ./data }\n")
	if _, err := FromConfig(Options{}); err == nil || !strings.Contains(err.Error(), "single app") {
		t.Fatalf("app with provider: %v", err)
	}

	// listeners: https main, http redirects, plain extra; instances from the section; gates
	gateOn = true
	loadConfig(t, `
provider: fake
server:
  https: ":443"
  http: ":80"
  plain: [":8080"]
  tls: { cert: c.pem, key: k.pem }
instances: { file: inst.yaml, dev: true }
`)
	h, err := FromConfig(Options{Resolve: func(s string) string { return "/etc/x/" + s }})
	if err != nil {
		t.Fatal(err)
	}
	l := h.Listeners
	if l.Listen != ":443" || l.TLS.HTTPListen != ":80" || len(l.PlainListen) != 1 || l.TLS.Cert != "/etc/x/c.pem" || l.TLS.Key != "/etc/x/k.pem" {
		t.Fatalf("listeners: %+v", l)
	}
	if src, ok := h.Source.(*YAMLSource); !ok || src.Path != "/etc/x/inst.yaml" || src.Required || !h.Dev {
		t.Fatalf("source: %+v dev %v", h.Source, h.Dev)
	}
	if len(h.Gates) != 1 || fp.prepared != h || h.Runtime == nil || h.Directory == nil {
		t.Fatalf("gates %d prepared %v runtime %v", len(h.Gates), fp.prepared == h, h.Runtime)
	}

	// http alone: plain HTTP, no redirect; a source from the binary wins over the section
	gateOn = false
	loadConfig(t, "provider: fake\nserver: { http: \":9000\" }\n")
	src := &YAMLSource{Path: "mine.yaml"}
	h, err = FromConfig(Options{Source: src})
	if err != nil {
		t.Fatal(err)
	}
	if h.Listeners.Listen != ":9000" || h.Listeners.TLS.HTTPListen != "" || h.Source != src || len(h.Gates) != 0 {
		t.Fatalf("http only: %+v source %v gates %d", h.Listeners, h.Source, len(h.Gates))
	}
}
