package mirror

import (
	"fmt"
	"github.com/pockethost/pocketone/ext/ceplugin"
	"log/slog"
	"time"

	"github.com/pockethost/pocketone/ext/edge"
)

// FileConfig is pocketone.yaml's `mothership` section (`$VAR` values come from the private .env through ext/config).
// self falls back to the `edges` section's self.
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

// Section is the key of the `mothership` section (pocketone.yaml): a community mothership this host mirrors its
// instances and the other edges from.
var Section = ceplugin.NewSection("mothership", FileConfig{}, "A community mothership to mirror instances and edges from: url, login, this edge's id, internal URL, region, apex.")

// FromSection returns a mirror for the `mothership` section, or nil when the config has none. Extensions are added to
// the mirror's.
func FromSection(log *slog.Logger, exts ...Extension) (*Mirror, error) {
	fc, found := Section.Get()
	if !found {
		return nil, nil
	}
	if fc.Self == "" {
		e, _ := edge.Section.Get()
		fc.Self = e.Self
	}
	cfg := Config{
		URL: fc.URL, Email: fc.Email, Password: fc.Password, Self: fc.Self, InternalURL: fc.InternalURL,
		Region: fc.Region, Apex: fc.Apex, Heartbeat: fc.Heartbeat, Resync: fc.Resync, Extensions: exts, Logger: log,
	}
	if cfg.URL == "" || cfg.Self == "" {
		return nil, fmt.Errorf("mothership: url and self are required")
	}
	return New(cfg), nil
}
