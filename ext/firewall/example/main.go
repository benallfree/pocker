// The firewall in front of a host: every request is checked (allowed peers, rate limits) before a toy runtime
// answers. In a PocketOne binary the firewall turns on from pocketbase.yaml's firewall / rate_limits sections; here
// it is built in Go.
//
//	go run ./example --instances instances.yaml
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/pockethost/pocketone/ext/firewall"
	"github.com/pockethost/pocketone/ext/host"
)

type okRuntime struct{}

func (okRuntime) Name() string                   { return "ok" }
func (okRuntime) Serves(inst host.Instance) bool { return true }
func (okRuntime) Ready() bool                    { return true }
func (okRuntime) ServeInstance(w http.ResponseWriter, _ *http.Request, inst host.Instance) {
	fmt.Fprintf(w, "hello from %s\n", inst.ID)
}

func main() {
	instances := flag.String("instances", "instances.yaml", "instances file")
	listen := flag.String("listen", "127.0.0.1:8090", "listen address")
	flag.Parse()
	fw := &firewall.Firewall{
		Config:     firewall.Config{AllowCIDRs: []string{"127.0.0.1"}},
		RateLimits: &firewall.RateLimits{IPHourly: 100},
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	h := &host.Host{Source: &host.YAMLSource{Path: *instances}, Runtime: okRuntime{}, Gates: []host.Gate{fw}, Listeners: host.Listeners{Listen: *listen}}
	if err := h.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
