// Command example runs one PocketBase app with bulk mail: a "Mail" page in the dashboard, campaigns to the app's
// own users, and $bulkmail in JS hooks.
//
//	go run ./example serve
package main

import (
	"log"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/dop251/goja"
	"github.com/pockethost/pocketbase/ext/bulkmail"
)

func main() {
	app := pocketbase.New()

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
