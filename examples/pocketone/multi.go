package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/pockethost/pocketone/ext/autotls"
	"github.com/pockethost/pocketone/ext/config"
	"github.com/pockethost/pocketone/ext/docker"
	"github.com/pockethost/pocketone/ext/edge"
	_ "github.com/pockethost/pocketone/ext/firewall" // turns on with a firewall / rate_limits section
	"github.com/pockethost/pocketone/ext/host"
	"github.com/pockethost/pocketone/ext/mailtrap"
	"github.com/pockethost/pocketone/ext/mothership/mirror"
	"github.com/pockethost/pocketone/ext/pocker"
	"github.com/pockethost/pocketone/ext/servertiming"
)

// serveMany runs the binary as a multi-instance host when pocketone.yaml names a provider: instances from the
// `instances` section, or mirrored from a community mothership (`mothership` section), routed by hostname, run by
// pocker (in-process) or docker (containers); other edges from the `edges` section and the mothership. It reports
// false when no provider is configured, so main serves a single app as stock PocketBase does.
func serveMany(cfg *config.Config) (handled bool, err error) {
	host.RegisterProvider(&pocker.Provider{Resolve: cfg.ResolvePath, Plugins: []pocker.Plugin{
		servertiming.New(), // outermost, so its header includes everything below
		autotls.New(),      // `autotls` section
		mailtrap.New(),     // dev instances with SMTP off: mail goes to the inbox at http://127.0.0.1:8025
	}})
	host.RegisterProvider(&docker.Provider{Resolve: cfg.ResolvePath})

	log := slog.Default()
	opts := host.Options{Resolve: cfg.ResolvePath, Logger: log}
	if p, _ := config.Provider.Get(); p == "pocker" {
		// pocker serves every instance in this process: it names itself (docker names each container's release)
		poweredBy := "Pocker " + pocker.Version()
		opts.Wrappers = append(opts.Wrappers, func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Powered-By", poweredBy)
				next.ServeHTTP(w, r)
			})
		})
	}
	edges, err := edge.FromSection(log)
	if err != nil {
		return true, err
	}
	m, err := mirror.FromSection(log)
	if err != nil {
		return true, err
	}
	var background []func(context.Context)
	if m != nil {
		opts.Source = m
		if edges != nil {
			edges = edge.MultiProvider{edges, m}
		} else {
			edges = m
		}
	}
	if edges != nil {
		opts.Forward = edge.NewRouter(edge.RouterConfig{Edges: edges, Logger: log}).Forward
		if r, ok := edges.(edge.Runner); ok {
			background = append(background, r.Run)
		}
	}
	h, err := host.FromConfig(opts)
	if errors.Is(err, host.ErrNoProvider) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	h.Background = append(h.Background, background...)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return true, h.Run(ctx)
}
