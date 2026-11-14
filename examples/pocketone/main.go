// Command pocketbase is the standard PocketOne binary: PocketBase's examples/base plus the PocketOne plugins that ship
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
	"ganigeorgiev.com/pocketbase/pocketbase/cmd"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/migratecmd"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/osutils"
	"github.com/dop251/goja"
	"github.com/pockethost/pocketone/ext/autotls"
	"github.com/pockethost/pocketone/ext/bulkmail"
	"github.com/pockethost/pocketone/ext/config"
	"github.com/pockethost/pocketone/ext/deploy"
	"github.com/pockethost/pocketone/ext/docker"
	"github.com/pockethost/pocketone/ext/jserrors"
	"github.com/pockethost/pocketone/ext/mailtrap"
	"github.com/pockethost/pocketone/ext/mcp"
	oauthserver "github.com/pockethost/pocketone/ext/oauth-server"
	"github.com/pockethost/pocketone/ext/pages"
	"github.com/pockethost/pocketone/ext/pitr"
	"github.com/pockethost/pocketone/ext/pluginkit"
	"github.com/pockethost/pocketone/ext/pocker"
	"github.com/pockethost/pocketone/ext/remotes"
	"github.com/pockethost/pocketone/ext/s3redirect"
	"github.com/pockethost/pocketone/ext/tokens"
	"github.com/pockethost/pocketone/ext/upgrade"
)

// PoweredBy is the X-Powered-By this binary sends on every response: "PocketOne 0.0.2" (see pocker.Version).
var PoweredBy = "PocketOne " + pocker.Version()

func main() {
	// pocketone.yaml (and the .env beside it): each plugin's section, defaults for the flags below (flags win)
	cfgOpts := config.FromArgs(os.Args[1:])
	cfg, err := config.Load(cfgOpts)
	if err != nil {
		log.Fatal(err)
	}
	if p, _ := config.Provider.Get(); p != "" && firstArg(os.Args[1:]) == "serve" {
		// a provider in pocketone.yaml: serve many instances (pocker or docker) instead of a single app
		if handled, err := serveMany(cfg); handled {
			if err != nil {
				log.Fatal(err)
			}
			return
		}
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
	app.RootCmd.PersistentFlags().StringVar(&cfgPath, "config", "", "the config file (default ./pocketone.yaml, else next to the executable)")
	app.RootCmd.PersistentFlags().StringVar(&envPath, "env-file", "", "secrets for $VAR references in the config file (default .env beside it); never passed to the app")
	app.RootCmd.AddCommand(config.NewCommand(func() config.Options { return cfgOpts }))
	app.RootCmd.AddCommand(docker.NewCommand(func() (docker.SectionConfig, func(string) string) {
		sc, _ := docker.Section.Get()
		return sc, cfg.ResolvePath
	}))

	// PocketOne plugins that ship in the standard binary and follow the plugin contract (pluginkit): Register
	// here, and BindJS in the jsvm below. Bulk mail: campaigns to the app's own users (Mail in the dashboard, Bulk
	// mail on Settings > Mail); nothing happens until someone starts a campaign. Defaults: 5/s, 5,000/day.
	defaultPlugins := []pluginkit.Plugin{
		bulkmail.New(bulkmail.Config{}),
		mailtrap.New(), // dev mode with SMTP off: mail goes to the inbox at http://127.0.0.1:8025, never sendmail
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
	defaultPlugins = append(defaultPlugins, autotls.NewApp(autoTLSConfig))

	// ---------------------------------------------------------------
	// Plugins and hooks:
	// ---------------------------------------------------------------

	// load jsvm (pb_hooks and pb_migrations); hook errors name pb_hooks/<file>:<line>:<col> (ext/jserrors)
	jsvm.MustRegister(app, jsvm.Config{
		MigrationsDir: migrationsDir,
		HooksDir:      hooksDir,
		HooksWatch:    hooksWatch,
		HooksPoolSize: hooksPool,
		OnInit: func(vm *goja.Runtime) {
			pluginkit.BindJS(app, vm, defaultPlugins...) // e.g. $bulkmail in pb_hooks
			mcp.BindJS(vm, app)                          // $mcp.addTool in pb_hooks
		},
		Extensions: []jsvm.Extension{jserrors.New()},
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
	pluginkit.MustRegister(app, defaultPlugins...)

	// migrate command (with js templates)
	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{
		TemplateLang: migratecmd.TemplateLangJS,
		Automigrate:  automigrate,
		Dir:          migrationsDir,
	})

	// upgrade command: latest PocketOne release from GitHub (pockethost/pocketone); keeps the old executable as
	// pocketone-<version>. Custom builds that publish their own releases set Owner/Repo/ArchivePrefix here.
	upgrade.MustRegister(app, app.RootCmd, upgrade.Config{})

	// static route to serves files from the provided public dir
	// (if publicDir exists and the route path is not already defined)
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Func: func(e *core.ServeEvent) error {
			// name the server that answers (a host or proxy in front passes it through)
			e.Router.BindFunc(func(re *core.RequestEvent) error {
				re.Response.Header().Set("X-Powered-By", PoweredBy)
				return re.Next()
			})
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

// firstArg is the first argument that isn't a flag (the command name), or "".
func firstArg(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config" || a == "--env-file":
			i++ // its value
		case len(a) > 0 && a[0] == '-':
		default:
			return a
		}
	}
	return ""
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
