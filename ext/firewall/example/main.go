// The firewall in front of a host: a config file's firewall / rate_limits sections turn it on (linking this package
// registers it), and every request is checked before a toy runtime answers.
//
//	go run ./example --config config.yaml --instances instances.yaml
//
// config.yaml:
//
//	firewall: { allow_cidrs: [127.0.0.1] }
//	rate_limits: { ip_hourly: 100 }
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/pockethost/pocketone/ext/firewall" // registers the firewall with the host
	"github.com/pockethost/pocketone/ext/host"
	"gopkg.in/yaml.v3"
)

type okRuntime struct{}

func (okRuntime) Name() string                   { return "ok" }
func (okRuntime) Serves(inst host.Instance) bool { return true }
func (okRuntime) Ready() bool                    { return true }
func (okRuntime) ServeInstance(w http.ResponseWriter, _ *http.Request, inst host.Instance) {
	fmt.Fprintf(w, "hello from %s\n", inst.ID)
}

func main() {
	config := flag.String("config", "config.yaml", "config file with firewall / rate_limits sections")
	instances := flag.String("instances", "instances.yaml", "instances file")
	listen := flag.String("listen", "127.0.0.1:8090", "listen address")
	flag.Parse()
	b, err := os.ReadFile(*config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var gates []host.Gate
	for _, g := range host.ConfiguredGates(&doc) {
		if err := g.Configure(&doc, false); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		gates = append(gates, g)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	h := &host.Host{Source: &host.YAMLSource{Path: *instances}, Runtime: okRuntime{}, Gates: gates, Listeners: host.Listeners{Listen: *listen}}
	if err := h.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
