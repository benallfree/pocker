// Command example runs one PocketBase app with bulk mail: a "Mail" page in the dashboard, campaigns to the app's
// own users, and $bulkmail in JS hooks.
//
// While SMTP is off (Settings > Mail), every mail it sends is saved to an "outbox" collection instead, so you can
// read test sends in the dashboard with nothing else running:
//
//	go run ./example serve
//
// Logins (ext/devusers, local testing only): dashboard admin@example.com / password, app user user@example.com / password.
package main

import (
	"log"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/dop251/goja"
	"github.com/pockethost/pocketbase/ext/bulkmail"
	"github.com/pockethost/pocketbase/ext/devusers"
)

func main() {
	app := pocketbase.New()
	devusers.Register(app) // local testing: superuser admin@example.com and user user@example.com, password "password"

	registerOutbox(app) // while SMTP is off, mail lands in the "outbox" collection (outbox.go)

	bulkmail.MustRegister(app, bulkmail.Config{
		Rate:  5,    // messages per second
		Daily: 5000, // messages per day; bulkmail.NoBulkMail forbids sending
	})

	jsvm.MustRegister(app, jsvm.Config{
		OnInit: func(vm *goja.Runtime) { bulkmail.BindJS(vm, app) },
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
