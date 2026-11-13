package config

import (
	"time"

	"github.com/pockethost/pocketone/ext/ceplugin"
)

// ServerConfig is the `server` section: how the binary listens. In single-app mode these are the serve command's
// --http, --https and --origins defaults (flags still win).
//
//	server:
//	  http: 127.0.0.1:8090
//	  https: ""
//	  origins: ["*"]
type ServerConfig struct {
	HTTP    string   `yaml:"http"`
	HTTPS   string   `yaml:"https"`
	Origins []string `yaml:"origins"`
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
