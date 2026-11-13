// Command pocker serves many PocketBase instances from one process.
//
//	pocker serve [--dev] [--pbRoot ./pb_root] [--instances ./instances.yaml] [--listen :8090] [--config pocker.yaml]
//
// Production (default) serves only the hostnames mapped in the instances file. --dev also serves unmapped
// hostnames: the instance id is the hostname (lowercase, no port), its folder <pbRoot>/<hostname> created on first use.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/pockethost/pocketone/ext/autotls"
	"github.com/pockethost/pocketone/ext/edge"
	"github.com/pockethost/pocketone/ext/firewall"
	"github.com/pockethost/pocketone/ext/mailtrap"
	"github.com/pockethost/pocketone/ext/mothership/mirror"
	"github.com/pockethost/pocketone/ext/pocker"
	"github.com/pockethost/pocketone/ext/servertiming"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: pocker serve [--dev] [--pbRoot DIR] [--instances FILE] [--listen ADDR] [--config FILE]")
		os.Exit(2)
	}

	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "pocker.yaml", "config file (optional; defaults apply when missing)")
	listen := fs.String("listen", "", "listen address (overrides config)")
	pbRoot := fs.String("pbRoot", "", "folder holding one PocketBase folder per instance (default ./pb_root)")
	fs.StringVar(pbRoot, "pb_root", "", "alias of --pbRoot")
	dev := fs.Bool("dev", false, "also serve unmapped hostnames (id = the hostname, folder created on first use); PocketBase dev mode")
	instances := fs.String("instances", "", "instances YAML file (overrides config)")
	_ = fs.Parse(os.Args[2:])

	cfg, err := pocker.LoadConfig(*configPath)
	if err != nil {
		slog.Error("pocker: config", "error", err)
		os.Exit(1)
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *pbRoot != "" {
		cfg.PBRoot = *pbRoot
	}
	if *dev {
		cfg.Dev = true
	}
	if *instances != "" {
		cfg.InstancesFile = *instances
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &pocker.Server{Config: cfg, Firewall: &firewall.Firewall{}, Plugins: []pocker.Plugin{
		servertiming.New(), // outermost, so its header includes everything below
		autotls.New(),
		mailtrap.New(), // dev instances with SMTP off: mail goes to the inbox at http://127.0.0.1:8025
	}}
	// the fleet: an `edges:` section and/or a `mothership:` section (instances and the other edges from the
	// mothership; fixed edges.list entries go in front) route instances other edges own through edge.Router
	edges, err := edge.EdgesFromConfig(edge.TopLevel(cfg.Doc(), "edges"), slog.Default())
	if err != nil {
		slog.Error("pocker: edges", "error", err)
		os.Exit(1)
	}
	if m, err := mirror.FromConfig(cfg.Doc(), slog.Default()); err != nil {
		slog.Error("pocker: mothership", "error", err)
		os.Exit(1)
	} else if m != nil {
		srv.Source = m
		if edges != nil {
			edges = edge.MultiProvider{edges, m}
		} else {
			edges = m
		}
	}
	if edges != nil {
		srv.Forward = edge.NewRouter(edge.RouterConfig{Edges: edges, Logger: slog.Default()}).Forward
		if r, ok := edges.(edge.Runner); ok {
			srv.Background = append(srv.Background, r.Run)
		}
	}
	if err := srv.Run(ctx); err != nil {
		slog.Error("pocker: serve", "error", err)
		os.Exit(1)
	}
}
