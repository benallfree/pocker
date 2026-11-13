// A minimal host: instances from a YAML file, routed by hostname, served by a toy runtime that answers with the
// instance it resolved. Real hosts plug in a provider instead (ext/pocker in-process, ext/docker in containers).
//
//	go run ./example --instances instances.yaml
//	curl -H 'Host: blog.localhost' localhost:8090/
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/pockethost/pocketone/ext/host"
)

type echoRuntime struct{}

func (echoRuntime) Name() string                   { return "echo" }
func (echoRuntime) Serves(inst host.Instance) bool { return true }
func (echoRuntime) Ready() bool                    { return true }
func (echoRuntime) ServeInstance(w http.ResponseWriter, r *http.Request, inst host.Instance) {
	fmt.Fprintf(w, "instance %s (%s%s)\n", inst.ID, r.Host, r.URL.Path)
}

func main() {
	instances := flag.String("instances", "instances.yaml", "instances file: instances: [{id: blog, hosts: [blog.localhost]}]")
	listen := flag.String("listen", "127.0.0.1:8090", "listen address")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	h := &host.Host{Source: &host.YAMLSource{Path: *instances}, Runtime: echoRuntime{}, Listeners: host.Listeners{Listen: *listen}}
	if err := h.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
