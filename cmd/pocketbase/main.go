// Command pocketbase is the standard PocketBase CE binary: PocketBase's examples/base plus the CE plugins that ship
// by default (ext/pages, ext/s3redirect, ext/bulkmail, ext/tokens, ext/remotes, ext/oauth-server, ext/deploy, ext/mcp,
// ext/pitr, ext/autotls). It's its
// own module so core's go.mod stays upstream's.
package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/ceplugin"
	"ganigeorgiev.com/pocketbase/pocketbase/cmd"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/ghupdate"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/migratecmd"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/osutils"
	"github.com/dop251/goja"
	"github.com/pockethost/pocketbase/ext/autotls"
	"github.com/pockethost/pocketbase/ext/bulkmail"
	"github.com/pockethost/pocketbase/ext/config"
	"github.com/pockethost/pocketbase/ext/deploy"
	"github.com/pockethost/pocketbase/ext/mcp"
	oauthserver "github.com/pockethost/pocketbase/ext/oauth-server"
	"github.com/pockethost/pocketbase/ext/pages"
	"github.com/pockethost/pocketbase/ext/pitr"
	"github.com/pockethost/pocketbase/ext/remotes"
	"github.com/pockethost/pocketbase/ext/s3redirect"
	"github.com/pockethost/pocketbase/ext/tokens"
)

func main() {
	// pocketbase.yaml (and the .env beside it): each plugin's section, defaults for the flags below (flags win)
	cfgOpts := config.FromArgs(os.Args[1:])
	cfg, err := config.Load(cfgOpts)
	if err != nil {
		log.Fatal(err)
	}
	if p, _ := config.Provider.Get(); p != "" {
		log.Fatalf("provider %q: %v (it serves a single app; omit provider:)", p, config.ErrNoProvider)
	}
	appCfg, _ := config.App.Get()
	if err := cfg.AllowEnv(appCfg.EnvAllow); err != nil { // only the .env names the app may read
		log.Fatal(err)
	}

	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDev:           or(appCfg.Dev, osutils.IsProbablyGoRun()),
		DefaultDataDir:       cfg.ResolvePath(appCfg.Dir),
		DefaultEncryptionEnv: appCfg.EncryptionEnv,
		DefaultQueryTimeout:  appCfg.QueryTimeout,
	})
	var cfgPath, envPath string
	app.RootCmd.PersistentFlags().StringVar(&cfgPath, "config", "", "the config file (default ./pocketbase.yaml, else next to the executable)")
	app.RootCmd.PersistentFlags().StringVar(&envPath, "env-file", "", "secrets for $VAR references in the config file (default .env beside it); never passed to the app")
	app.RootCmd.AddCommand(config.NewCommand(func() config.Options { return cfgOpts }))

	// PocketBase CE plugins that ship in the standard binary and follow the plugin contract (ceplugin): Register
	// here, and BindJS in the jsvm below. Bulk mail: campaigns to the app's own users (Mail in the dashboard, Bulk
	// mail on Settings > Mail); nothing happens until someone starts a campaign. Defaults: 5/s, 5,000/day.
	cePlugins := []ceplugin.Plugin{
		bulkmail.New(bulkmail.Config{}),
	}

	// ---------------------------------------------------------------
	// Optional plugin flags:
	// ---------------------------------------------------------------

	var hooksDir string
	app.RootCmd.PersistentFlags().StringVar(
		&hooksDir,
		"hooksDir",
		cfg.ResolvePath(appCfg.HooksDir),
		"the directory with the JS app hooks",
	)

	var hooksWatch bool
	app.RootCmd.PersistentFlags().BoolVar(
		&hooksWatch,
		"hooksWatch",
		or(appCfg.HooksWatch, true),
		"auto restart the app on pb_hooks file change; it has no effect on Windows",
	)

	var hooksPool int
	app.RootCmd.PersistentFlags().IntVar(
		&hooksPool,
		"hooksPool",
		orInt(appCfg.HooksPool, 15),
		"the total prewarm goja.Runtime instances for the JS app hooks execution",
	)

	var migrationsDir string
	app.RootCmd.PersistentFlags().StringVar(
		&migrationsDir,
		"migrationsDir",
		cfg.ResolvePath(appCfg.MigrationsDir),
		"the directory with the user defined migrations",
	)

	var automigrate bool
	app.RootCmd.PersistentFlags().BoolVar(
		&automigrate,
		"automigrate",
		or(appCfg.Automigrate, true),
		"enable/disable auto migrations",
	)

	var publicDir string
	app.RootCmd.PersistentFlags().StringVar(
		&publicDir,
		"publicDir",
		orStr(cfg.ResolvePath(appCfg.PublicDir), defaultPublicDir()),
		"the directory to serve static files",
	)

	var indexFallback bool
	app.RootCmd.PersistentFlags().BoolVar(
		&indexFallback,
		"indexFallback",
		or(appCfg.IndexFallback, true),
		"fallback the request to index.html on missing static path, e.g. when pretty urls are used with SPA",
	)

	var pagesDir string
	app.RootCmd.PersistentFlags().StringVar(
		&pagesDir,
		"pagesDir",
		"",
		"the directory with the PocketPages app (default pb_pages next to pb_data); served when it exists",
	)

	var s3Redirect bool
	app.RootCmd.PersistentFlags().BoolVar(
		&s3Redirect,
		"s3Redirect",
		true,
		"answer file downloads with a short-lived signed redirect to the bucket when S3 file storage is on",
	)

	var s3RedirectTTL time.Duration
	app.RootCmd.PersistentFlags().DurationVar(
		&s3RedirectTTL,
		"s3RedirectTTL",
		s3redirect.DefaultTTL,
		"how long each signed file download URL stays valid",
	)

	var pitrEnabled bool
	app.RootCmd.PersistentFlags().BoolVar(
		&pitrEnabled,
		"pitr",
		true,
		"point-in-time restore: replicate data.db into pb_data/litestream/ while serving",
	)

	var pitrRetention time.Duration
	app.RootCmd.PersistentFlags().DurationVar(
		&pitrRetention,
		"pitrRetention",
		72*time.Hour,
		"how far back a point-in-time restore can go",
	)

	var autoTLS bool
	app.RootCmd.PersistentFlags().BoolVar(
		&autoTLS,
		"autotls",
		false,
		"HTTPS certificates through CertMagic instead of the built-in autocert (with serve <domain> or --https):\nother CAs, extra domains, wildcards through DNS-01",
	)

	var autoTLSEmail string
	app.RootCmd.PersistentFlags().StringVar(
		&autoTLSEmail,
		"autotlsEmail",
		"",
		"ACME account email for --autotls (expiry notices from the CA)",
	)

	var autoTLSCA string
	app.RootCmd.PersistentFlags().StringVar(
		&autoTLSCA,
		"autotlsCA",
		"",
		"certificate authority for --autotls: letsencrypt (default), letsencrypt-staging, zerossl, or an ACME directory URL",
	)

	var autoTLSDomains []string
	app.RootCmd.PersistentFlags().StringSliceVar(
		&autoTLSDomains,
		"autotlsDomains",
		nil,
		"extra domains for --autotls, obtained at start (comma separated; wildcards like *.example.com need --autotlsDNS)",
	)

	var autoTLSDNS string
	app.RootCmd.PersistentFlags().StringVar(
		&autoTLSDNS,
		"autotlsDNS",
		"",
		"DNS provider for DNS-01 challenges with --autotls (supported: "+strings.Join(autotls.SupportedDNSProviders, ", ")+")",
	)

	var autoTLSDNSTokenEnv string
	app.RootCmd.PersistentFlags().StringVar(
		&autoTLSDNSTokenEnv,
		"autotlsDNSTokenEnv",
		"CF_API_TOKEN",
		"environment variable holding the DNS provider's API token for --autotlsDNS",
	)

	app.RootCmd.ParseFlags(os.Args[1:])

	autoTLSConfig := autotls.Config{Auto: autoTLS, Email: autoTLSEmail, CA: autoTLSCA, Domains: autoTLSDomains}
	if autoTLSDNS != "" {
		autoTLSConfig.DNS = &autotls.DNSConfig{Provider: autoTLSDNS, APITokenEnv: autoTLSDNSTokenEnv}
	}
	// Auto TLS: off unless --autotls; then CertMagic serves the HTTPS certificates of `serve <domain>`
	cePlugins = append(cePlugins, autotls.NewApp(autoTLSConfig))

	// ---------------------------------------------------------------
	// Plugins and hooks:
	// ---------------------------------------------------------------

	// load jsvm (pb_hooks and pb_migrations)
	jsvm.MustRegister(app, jsvm.Config{
		MigrationsDir: migrationsDir,
		HooksDir:      hooksDir,
		HooksWatch:    hooksWatch,
		HooksPoolSize: hooksPool,
		OnInit: func(vm *goja.Runtime) {
			ceplugin.BindJS(app, vm, cePlugins...) // e.g. $bulkmail in pb_hooks
			mcp.BindJS(vm, app)                    // $mcp.addTool in pb_hooks
		},
	})

	// PocketPages: serves pb_pages when the folder exists (page code runs in the jsvm above)
	pages.MustRegister(app, pages.Config{Dir: pagesDir})
	pages.RegisterCommand(app.RootCmd) // `pocketbase pages create <name> [--starter kit]`, `pocketbase pages starters`

	// S3 redirects: with S3 file storage on, downloads redirect to a signed bucket URL after the access check
	s3redirect.MustRegister(app, s3redirect.Config{Enabled: &s3Redirect, TTL: s3RedirectTTL})

	// API tokens (pbt_...), Settings → API tokens, the token command
	tokens.MustRegister(app, tokens.Config{})

	// remotes: remote/login/logout/link commands and GET /_pocker/whoami
	remotes.MustRegister(app, remotes.Config{})

	// OAuth server: browser sign-in for `pocketbase login` (superusers) and OAuth clients
	oauthserver.MustRegister(app, oauthserver.Config{})

	// deploys: deploy/dev/pull/logs commands and the deploy API (/_pocker/deploy/v1, superusers)
	deploy.MustRegister(app, deploy.Config{})

	// MCP endpoint at /mcp (built-in pb_* tools plus $mcp tools from pb_hooks); OAuth sign-in through oauth-server
	mcp.MustRegister(app, mcp.Config{})

	// point-in-time restore: on by default (--pitr=false turns it off); history in pb_data/litestream/, restore from
	// Settings → Backups or `pocketbase pitr restore`
	pitr.MustRegister(app, pitr.Config{Enabled: &pitrEnabled, Retention: pitrRetention})
	ceplugin.MustRegister(app, cePlugins...)

	// migrate command (with js templates)
	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{
		TemplateLang: migratecmd.TemplateLangJS,
		Automigrate:  automigrate,
		Dir:          migrationsDir,
	})

	// update command: PocketBase CE releases on GitHub (pockethost/pocketbase), not upstream.
	// Custom builds: set ghupdate.Config{Owner, Repo, BaseURL} or -ldflags "-X .../plugins/ghupdate.DefaultOwner=...".
	ghupdate.MustRegister(app, app.RootCmd, ghupdate.Config{})

	// static route to serves files from the provided public dir
	// (if publicDir exists and the route path is not already defined)
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Func: func(e *core.ServeEvent) error {
			if !e.Router.HasRoute(http.MethodGet, "/{path...}") {
				e.Router.GET("/{path...}", apis.Static(os.DirFS(publicDir), indexFallback))
			}

			return e.Next()
		},
		Priority: 999, // execute as latest as possible to allow users to provide their own route
	})

	// system commands (what app.Start registers), with the server section as the serve command's defaults
	serveCmd := cmd.NewServeCommand(app, true)
	srv, _ := config.Server.Get()
	for flag, v := range map[string]string{"http": srv.HTTP, "https": srv.HTTPS, "origins": strings.Join(srv.Origins, ",")} {
		if v != "" {
			if err := serveCmd.PersistentFlags().Set(flag, v); err != nil {
				log.Fatalf("server.%s: %v", flag, err)
			}
		}
	}
	app.RootCmd.AddCommand(cmd.NewSuperuserCommand(app), serveCmd)
	if err := app.Execute(); err != nil {
		log.Fatal(err)
	}
}

func or(v *bool, def bool) bool {
	if v != nil {
		return *v
	}
	return def
}

func orInt(v, def int) int {
	if v != 0 {
		return v
	}
	return def
}

func orStr(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// the default pb_public dir location is relative to the executable
func defaultPublicDir() string {
	if osutils.IsProbablyGoRun() {
		return "./pb_public"
	}

	return filepath.Join(os.Args[0], "../pb_public")
}
