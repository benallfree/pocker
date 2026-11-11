// Command example runs one PocketBase app that serves pages from pb_pages next to its pb_data.
//
//	go run ./example serve --dir=./site/pb_data
//
// (with ./site/pb_pages beside it).
package main

import (
	"log"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/pockethost/pocketbase/ext/pages"
)

func main() {
	app := pocketbase.New()

	jsvm.MustRegister(app, jsvm.Config{}) // page code runs in the app's JS VM
	pages.MustRegister(app, pages.Config{})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
