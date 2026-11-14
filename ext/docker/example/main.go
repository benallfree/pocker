// A minimal docker host: instances from a YAML file, each in its own container with the PocketBase version it asks
// for, routed by hostname on one listener.
//
//	go run ./example --instances instances.yaml --data ./instances --binaries ./bin --versions pb:0.23,p1:0.0
//
// instances.yaml:
//
//	instances:
//	  - { id: blog, hosts: [blog.localhost], version: "pb:0.23.*" }
//	  - { id: shop, hosts: [shop.localhost], version: "ce:0.0.*" }
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/pockethost/pocketone/ext/docker"
	"github.com/pockethost/pocketone/ext/host"
)

func main() {
	instances := flag.String("instances", "instances.yaml", "instances file")
	data := flag.String("data", "./instances", "folder with one folder per instance")
	binaries := flag.String("binaries", "./bin", "PocketBase binary cache")
	versions := flag.String("versions", "pb:0.23,p1:0.0", "minors to serve (latest patch each; fetched at start)")
	image := flag.String("image", "alpine:3.20", "base image")
	listen := flag.String("listen", "127.0.0.1:8090", "listen address")
	flag.Parse()

	b := &docker.Binaries{Root: *binaries}
	tracked := strings.Split(*versions, ",")
	if err := docker.RunUpdate(context.Background(), os.Stdout, docker.DefaultDiscovery(), b, tracked); err != nil {
		slog.Error("versions", "error", err)
		os.Exit(1)
	}
	rt, err := docker.New(docker.Config{Image: *image, DataRoot: *data, Binaries: b, Versions: docker.YAMLVersions{Tracked: tracked, Root: b.Root}})
	if err != nil {
		slog.Error("docker", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	h := &host.Host{Source: &host.YAMLSource{Path: *instances}, Runtime: rt, Listeners: host.Listeners{Listen: *listen}}
	if err := h.Run(ctx); err != nil {
		slog.Error("host", "error", err)
		os.Exit(1)
	}
}
