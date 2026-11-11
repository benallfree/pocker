package pitr

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/pockethost/pocketbase/ext/pocker"
)

// The plugin under a multitenant pocker server:
//
//	srv.Plugins = append(srv.Plugins, pitr.New(pitr.Config{}))
//
// Process settings come from the config file's top-level `pitr:` key (fields set in Go win), per-instance settings
// from the instance's `plugins: {pitr: {enabled, retention}}`.
var (
	_ pocker.Plugin     = (*Plugin)(nil)
	_ pocker.Configurer = (*Plugin)(nil)
	_ pocker.AppIniter  = (*Plugin)(nil)
	_ pocker.Starter    = (*Plugin)(nil)
)

// Configure reads the config file's top-level `pitr:` key. Fields already set in Go are kept; the file fills the
// rest.
func (p *Plugin) Configure(doc *yaml.Node, _ pocker.Config) error {
	n := pocker.TopLevel(doc, Name)
	if n == nil {
		return nil
	}
	var c Config
	if err := n.Decode(&c); err != nil {
		return fmt.Errorf("pitr: %w", err)
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
	if p.cfg.Bucket == nil {
		p.cfg.Bucket = c.Bucket
	}
	return nil
}

// InitApp sets up an instance's app before it bootstraps. History goes to instances/<id>/ in the bucket (or the
// instance's own pb_data/litestream/). The instance may set `enabled` and `retention`; a bucket can only come from
// the server's config, so its credentials never sit in instance data.
func (p *Plugin) InitApp(inst pocker.Instance, app core.App) error {
	ac, err := instanceConfig(inst)
	if err != nil {
		return err
	}
	ac.Key = "instances/" + inst.ID + "/"
	return p.SetupApp(app, ac)
}

func instanceConfig(inst pocker.Instance) (AppConfig, error) {
	var ac AppConfig
	raw := inst.PluginConfig(Name)
	if raw == nil {
		return ac, nil
	}
	if ac2, ok := raw.(AppConfig); ok {
		return ac2, nil
	}
	if ac2, ok := raw.(*AppConfig); ok && ac2 != nil {
		return *ac2, nil
	}
	var probe map[string]any
	if err := pocker.DecodePluginConfig(raw, &probe); err != nil {
		return ac, fmt.Errorf("pitr: instance %s: %w", inst.ID, err)
	}
	for k := range probe {
		if k != "enabled" && k != "retention" {
			return ac, fmt.Errorf("pitr: instance %s: %q can't be set per instance (only enabled, retention)", inst.ID, k)
		}
	}
	if err := pocker.DecodePluginConfig(raw, &ac); err != nil {
		return ac, fmt.Errorf("pitr: instance %s: %w", inst.ID, err)
	}
	return ac, nil
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
