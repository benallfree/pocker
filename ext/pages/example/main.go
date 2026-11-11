// Command example runs one PocketBase app that serves the demo in ./pb_pages (EJS, Markdown, JSX/TSX, auth,
// ctx.pb(), SSE, a JSON route, +404). ./pb_migrations creates a "notes" collection
// with three seed notes. From this folder:
//
//	go run . serve --dir=./pb_data
//
// Then open http://127.0.0.1:8090. Edit anything in pb_pages and reload; no restart needed.
//
// Logins (ext/devusers, local testing only): dashboard admin@example.com / password, app user user@example.com /
// password.
package main

import (
	"log"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/pockethost/pocketbase/ext/devusers"
	"github.com/pockethost/pocketbase/ext/pages"
)

func main() {
	app := pocketbase.New()
	devusers.Register(app) // local testing: superuser admin@example.com and user user@example.com, password "password"

	jsvm.MustRegister(app, jsvm.Config{}) // page code runs in the app's JS VM
	pages.MustRegister(app, pages.Config{})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
