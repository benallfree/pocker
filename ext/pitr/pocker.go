package pitr

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/pockethost/pocketone/ext/pluginkit"
	"github.com/pockethost/pocketone/ext/pocker"
)

// The plugin under a multitenant pocker server:
//
//	srv.Plugins = append(srv.Plugins, pitr.New(pitr.Config{}))
//
// Process settings come from pocketone.yaml's `pitr` section (fields set in Go win), per-instance settings
// from the instance's `plugins: {pitr: {enabled, retention}}` (pocker hosts it as a [pluginkit.Plugin]).
var (
	_ pocker.Plugin     = (*Plugin)(nil)
	_ pocker.Configurer = (*Plugin)(nil)
	_ pluginkit.Plugin  = (*Plugin)(nil)
	_ pocker.Starter    = (*Plugin)(nil)
)

// Configure reads pocketone.yaml's `pitr` section. Fields already set in Go are kept; the section fills the rest.
func (p *Plugin) Configure(pocker.Config) error {
	c, found := Section.Get()
	if !found {
		return nil
	}
	if p.cfg.Enabled == nil {
		p.cfg.Enabled = c.Enabled
	}
	if !p.retentionSet && c.Retention > 0 {
		p.cfg.Retention = c.Retention
	}
	if !p.intervalSet && c.SnapshotInterval > 0 {
		p.cfg.SnapshotInterval = c.SnapshotInterval
	}
	return nil
}

// settings is the shape of an instance's `plugins.pitr`. Only `enabled` and `retention` are read per app.
type settings struct {
	Enabled   *bool               `json:"enabled"`
	Retention *pluginkit.Duration `json:"retention"`
}

// Register implements [pluginkit.Plugin]: sets up app before it bootstraps. Where its history goes is decided per
// app: its own PITR S3 setting, else the server's storage (`litestream` kind), else its pb_data/litestream/.
func (p *Plugin) Register(app core.App, layers pluginkit.Config) error {
	var s settings
	if err := layers.Decode(&s); err != nil {
		return fmt.Errorf("pitr: %w", err)
	}
	ac := AppConfig{Enabled: s.Enabled}
	if s.Retention != nil {
		ac.Retention = time.Duration(*s.Retention)
	}
	return p.SetupApp(app, ac)
}

// Start waits for the server to stop, then for every app to flush and detach (the manager stops them), and closes
// the Litestream stores.
func (p *Plugin) Start(ctx context.Context, _ *pocker.Manager) {
	<-ctx.Done()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		n := len(p.apps)
		p.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.Close(cctx); err != nil && !errors.Is(err, context.Canceled) {
		p.log.Warn("pitr: close", "error", err)
	}
}

// Section is the key of the `pitr` section (pocketone.yaml): point-in-time restore defaults on a pocker host.
var Section = pluginkit.NewSection(Name, Config{}, "Point-in-time restore on a pocker host: on by default, retention, snapshot interval (instances may override).")
