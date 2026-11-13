package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/pockethost/pocketone/ext/config"
	"github.com/pockethost/pocketone/ext/docker"
	"github.com/pockethost/pocketone/ext/edge"
	_ "github.com/pockethost/pocketone/ext/firewall" // turns on with a firewall / rate_limits section
	"github.com/pockethost/pocketone/ext/host"
	"github.com/pockethost/pocketone/ext/pocker"
)

// serveMany runs the binary as a multi-instance host when pocketbase.yaml names a provider: instances from the
// `instances` section (or a fleet), routed by hostname, run by pocker (in-process) or docker (containers). It
// reports false when no provider is configured, so main serves a single app as stock PocketBase does.
func serveMany(cfg *config.Config) (handled bool, err error) {
	host.RegisterProvider(&pocker.Provider{Resolve: cfg.ResolvePath})
	host.RegisterProvider(&docker.Provider{Resolve: cfg.ResolvePath})

	opts := host.Options{Resolve: cfg.ResolvePath, Logger: slog.Default()}
	edges, err := edge.FromSection(slog.Default())
	if err != nil {
		return true, err
	}
	if edges != nil {
		opts.Forward = edge.NewRouter(edge.RouterConfig{Edges: edges, Logger: slog.Default()}).Forward
	}
	h, err := host.FromConfig(opts)
	if errors.Is(err, host.ErrNoProvider) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return true, h.Run(ctx)
}
