package config

import (
	"time"

	"github.com/pockethost/pocketone/ext/ceplugin"
)

// ServerConfig is the `server` section: how the binary listens.
//
// Single-app mode: http, https and origins are the serve command's --http, --https and --origins defaults (flags
// still win). Multi-instance mode (a provider): https is the main address (TLS from tls or a TLS plugin), http then
// redirects plain HTTP to HTTPS (http alone serves plain HTTP), plain lists extra plain-HTTP addresses with no
// redirect (e.g. a private network other edges forward on), and shutdown_timeout bounds in-flight requests at stop.
//
//	server:
//	  https: ":443"
//	  http: ":80"
//	  plain: [":8080"]
//	  tls: { cert: /etc/certs/cert.pem, key: /etc/certs/key.pem }
type ServerConfig struct {
	HTTP            string        `yaml:"http"`
	HTTPS           string        `yaml:"https"`
	Origins         []string      `yaml:"origins"`
	Plain           []string      `yaml:"plain"`
	TLS             ServerTLS     `yaml:"tls"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// ServerTLS is a certificate pair, reloaded when the files change.
type ServerTLS struct {
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

// AppConfig is the `app` section: the one app of single-app mode (the root flags as config; flags still win). It
// doesn't apply when a runtime provider runs many instances (each has its own folder under the provider's root).
//
//	app:
//	  dir: ./pb_data
//	  dev: false
//	  encryption_env: PB_ENCRYPTION_KEY
//	  hooks_dir: ./pb_hooks
//	  migrations_dir: ./pb_migrations
//	  public_dir: ./pb_public
//	  env_allow: [STRIPE_KEY]       # .env names the app (and its JS hooks) may read; default none
type AppConfig struct {
	Dir           string        `yaml:"dir"`
	Dev           *bool         `yaml:"dev"`
	EncryptionEnv string        `yaml:"encryption_env"`
	QueryTimeout  time.Duration `yaml:"query_timeout"`
	HooksDir      string        `yaml:"hooks_dir"`
	HooksWatch    *bool         `yaml:"hooks_watch"`
	HooksPool     int           `yaml:"hooks_pool"`
	MigrationsDir string        `yaml:"migrations_dir"`
	Automigrate   *bool         `yaml:"automigrate"`
	PublicDir     string        `yaml:"public_dir"`
	IndexFallback *bool         `yaml:"index_fallback"`
	EnvAllow      []string      `yaml:"env_allow"`
}

// Server, App and Provider are the sections this package owns.
var (
	Server   = ceplugin.NewSectionFunc("server", func() ServerConfig { return ServerConfig{} }, "How the binary listens: http, https and CORS origins (single-app mode: the serve command's defaults).")
	App      = ceplugin.NewSectionFunc("app", func() AppConfig { return AppConfig{} }, "The single app: data dir, dev mode, encryption, hooks, migrations, public dir, and the .env names it may read (env_allow). Not used when a provider runs many instances.")
	Provider = ceplugin.NewSection("provider", "", "The runtime provider that runs many instances (pocker or docker). Omit for a single stock app.")
)
