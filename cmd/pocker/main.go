// Command pocker serves many PocketBase instances from one process.
//
//	pocker serve [--config pocker.yaml] [--listen :8090] [--data ./pocker_data] [--instances ./instances.yaml]
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"ganigeorgiev.com/pocketbase/pocketbase/pocker"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: pocker serve [--config pocker.yaml] [--listen :8090] [--data DIR] [--instances FILE]")
		os.Exit(2)
	}

	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "pocker.yaml", "config file (optional; defaults apply when missing)")
	listen := fs.String("listen", "", "listen address (overrides config)")
	data := fs.String("data", "", "data root holding instance folders (overrides config)")
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
	if *data != "" {
		cfg.DataRoot = *data
	}
	if *instances != "" {
		cfg.InstancesFile = *instances
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &pocker.Server{Config: cfg}
	if err := srv.Run(ctx); err != nil {
		slog.Error("pocker: serve", "error", err)
		os.Exit(1)
	}
}
