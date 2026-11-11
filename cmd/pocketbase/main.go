// Command pocketbase is the standard PocketBase CE binary: PocketBase's examples/base plus the CE plugins that ship
// by default (ext/pages, ext/tokens, ext/remotes, ext/oauth-server, ext/deploy). It's its own module so core's go.mod
// stays upstream's.
package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/migratecmd"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/update"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/osutils"
	"github.com/pockethost/pocketbase/ext/deploy"
	oauthserver "github.com/pockethost/pocketbase/ext/oauth-server"
	"github.com/pockethost/pocketbase/ext/pages"
	"github.com/pockethost/pocketbase/ext/remotes"
	"github.com/pockethost/pocketbase/ext/tokens"
)

func main() {
	app := pocketbase.New()

	// ---------------------------------------------------------------
	// Optional plugin flags:
	// ---------------------------------------------------------------

	var hooksDir string
	app.RootCmd.PersistentFlags().StringVar(
		&hooksDir,
		"hooksDir",
		"",
		"the directory with the JS app hooks",
	)

	var hooksWatch bool
	app.RootCmd.PersistentFlags().BoolVar(
		&hooksWatch,
		"hooksWatch",
		true,
		"auto restart the app on pb_hooks file change; it has no effect on Windows",
	)

	var hooksPool int
	app.RootCmd.PersistentFlags().IntVar(
		&hooksPool,
		"hooksPool",
		15,
		"the total prewarm goja.Runtime instances for the JS app hooks execution",
	)

	var migrationsDir string
	app.RootCmd.PersistentFlags().StringVar(
		&migrationsDir,
		"migrationsDir",
		"",
		"the directory with the user defined migrations",
	)

	var automigrate bool
	app.RootCmd.PersistentFlags().BoolVar(
		&automigrate,
		"automigrate",
		true,
		"enable/disable auto migrations",
	)

	var publicDir string
	app.RootCmd.PersistentFlags().StringVar(
		&publicDir,
		"publicDir",
		defaultPublicDir(),
		"the directory to serve static files",
	)

	var indexFallback bool
	app.RootCmd.PersistentFlags().BoolVar(
		&indexFallback,
		"indexFallback",
		true,
		"fallback the request to index.html on missing static path, e.g. when pretty urls are used with SPA",
	)

	var pagesDir string
	app.RootCmd.PersistentFlags().StringVar(
		&pagesDir,
		"pagesDir",
		"",
		"the directory with the PocketPages app (default pb_pages next to pb_data); served when it exists",
	)

	app.RootCmd.ParseFlags(os.Args[1:])

	// ---------------------------------------------------------------
	// Plugins and hooks:
	// ---------------------------------------------------------------

	// load jsvm (pb_hooks and pb_migrations)
	jsvm.MustRegister(app, jsvm.Config{
		MigrationsDir: migrationsDir,
		HooksDir:      hooksDir,
		HooksWatch:    hooksWatch,
		HooksPoolSize: hooksPool,
	})

	// PocketPages: serves pb_pages when the folder exists (page code runs in the jsvm above)
	pages.MustRegister(app, pages.Config{Dir: pagesDir})

	// API tokens (pbt_...), Settings → API tokens, the token command
	tokens.MustRegister(app, tokens.Config{})

	// remotes: remote/login/logout/link commands and GET /_pocker/whoami
	remotes.MustRegister(app, remotes.Config{})

	// OAuth server: browser sign-in for `pocketbase login` (superusers) and OAuth clients
	oauthserver.MustRegister(app, oauthserver.Config{})

	// deploys: deploy/dev/pull/logs commands and the deploy API (/_pocker/deploy/v1, superusers)
	deploy.MustRegister(app, deploy.Config{})

	// migrate command (with js templates)
	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{
		TemplateLang: migratecmd.TemplateLangJS,
		Automigrate:  automigrate,
		Dir:          migrationsDir,
	})

	// update command
	update.MustRegister(app, app.RootCmd, update.Config{
		BaseURL:    "https://ganigeorgiev.com",
		Project:    "pocketbase",
		Repository: "pocketbase",
	})

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

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

// the default pb_public dir location is relative to the executable
func defaultPublicDir() string {
	if osutils.IsProbablyGoRun() {
		return "./pb_public"
	}

	return filepath.Join(os.Args[0], "../pb_public")
}
