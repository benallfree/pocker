// Command example runs one PocketBase app with bulk mail: a "Mail" page in the dashboard, campaigns to the app's
// own users, and $bulkmail in JS hooks.
//
// Run it in dev mode: while SMTP is off, PocketOne's dev mail trap catches every mail (bulk mail included) and
// shows it at http://127.0.0.1:8025, with nothing else to install:
//
//	go run ./example serve --dev
//
// Logins (ext/devusers, local testing only): dashboard admin@example.com / password, app user user@example.com / password.
package main

import (
	"log"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/dop251/goja"
	"github.com/pockethost/pocketone/ext/bulkmail"
	"github.com/pockethost/pocketone/ext/devusers"
	"github.com/pockethost/pocketone/ext/pluginkit"
)

func main() {
	app := pocketbase.New()
	devusers.Register(app) // local testing: superuser admin@example.com and user user@example.com, password "password"

	registerDemoView(app) // a "newsletter_audience" view to use as a campaign audience (demo.go)

	// the same plugin value pocker would host on every instance (srv.Plugins = append(srv.Plugins, mail))
	mail := bulkmail.New(bulkmail.Config{
		Rate:  5,    // messages per second
		Daily: 5000, // messages per day; bulkmail.NoBulkMail forbids sending
	})
	pluginkit.MustRegister(app, mail)

	jsvm.MustRegister(app, jsvm.Config{
		OnInit: func(vm *goja.Runtime) { pluginkit.BindJS(app, vm, mail) }, // $bulkmail in JS hooks
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
