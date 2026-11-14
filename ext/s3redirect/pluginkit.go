package s3redirect

import (
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/pockethost/pocketone/ext/pluginkit"
)

var _ pluginkit.Plugin = (*Plugin)(nil)

// Section is the key of the `s3redirect` section (pocketone.yaml): the process defaults, under the values set in Go.
var Section = pluginkit.NewSection(Name, Config{}, "Signed S3 redirects for file downloads: enabled, URL lifetime (instances may override).")

// settings is the shape of an instance's `plugins.s3redirect`.
type settings struct {
	Enabled *bool               `json:"enabled"`
	TTL     *pluginkit.Duration `json:"ttl"`
}

// applySection fills what Go left unset from pocketone.yaml's `s3redirect` section, once.
func (p *Plugin) applySection() {
	p.sectionOnce.Do(func() {
		c, found := Section.Get()
		if !found {
			return
		}
		if p.cfg.Enabled == nil {
			p.cfg.Enabled = c.Enabled
		}
		if !p.ttlSet && c.TTL > 0 {
			p.cfg.TTL = c.TTL
		}
	})
}

// Register implements [pluginkit.Plugin]: the redirect on app's file downloads, with app's settings over the
// defaults given to [New] and the `s3redirect` section.
func (p *Plugin) Register(app core.App, layers pluginkit.Config) error {
	p.applySection()
	var s settings
	if err := layers.Decode(&s); err != nil {
		return err
	}
	ac := AppConfig{Enabled: s.Enabled}
	if s.TTL != nil {
		ac.TTL = time.Duration(*s.TTL)
	}
	return p.SetupApp(app, ac)
}
