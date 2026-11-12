package mirror

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/pockethost/pocketbase/ext/edge"
	"gopkg.in/yaml.v3"
)

// FileConfig is a config file's `mothership:` section. Values expand $VARS from the environment, so the login can
// live in .env. self falls back to `edges.self`.
//
//	mothership:
//	  url: http://10.88.0.10:8090
//	  email: $MOTHERSHIP_EMAIL
//	  password: $MOTHERSHIP_PASSWORD
//	  self: lax-1
//	  internal_url: http://10.88.0.5:8080   # registers this edge when the mothership has no record for it
//	  region: lax
//	  apex: example.com                     # <name>.example.com routes to each instance
//	  heartbeat: 15s
//	  resync: 10m
type FileConfig struct {
	URL         string        `yaml:"url"`
	Email       string        `yaml:"email"`
	Password    string        `yaml:"password"`
	Self        string        `yaml:"self"`
	InternalURL string        `yaml:"internal_url"`
	Region      string        `yaml:"region"`
	Apex        string        `yaml:"apex"`
	Heartbeat   time.Duration `yaml:"heartbeat"`
	Resync      time.Duration `yaml:"resync"`
}

// FromConfig returns a mirror for a config document's `mothership:` section, or nil when it has none.
// Extensions are added to the mirror's.
func FromConfig(doc *yaml.Node, log *slog.Logger, exts ...Extension) (*Mirror, error) {
	n := edge.TopLevel(doc, "mothership")
	if n == nil {
		return nil, nil
	}
	var fc FileConfig
	if err := n.Decode(&fc); err != nil {
		return nil, fmt.Errorf("mothership: %w", err)
	}
	if fc.Self == "" {
		if e := edge.TopLevel(doc, "edges"); e != nil {
			var s struct {
				Self string `yaml:"self"`
			}
			_ = e.Decode(&s)
			fc.Self = s.Self
		}
	}
	cfg := Config{
		URL: os.ExpandEnv(fc.URL), Email: os.ExpandEnv(fc.Email), Password: os.ExpandEnv(fc.Password),
		Self: os.ExpandEnv(fc.Self), InternalURL: os.ExpandEnv(fc.InternalURL), Region: os.ExpandEnv(fc.Region),
		Apex: os.ExpandEnv(fc.Apex), Heartbeat: fc.Heartbeat, Resync: fc.Resync, Extensions: exts, Logger: log,
	}
	if cfg.URL == "" || cfg.Self == "" {
		return nil, fmt.Errorf("mothership: url and self are required")
	}
	return New(cfg), nil
}
