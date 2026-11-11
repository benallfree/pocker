// Command example runs one PocketBase app that serves pages from pb_pages next to its pb_data.
//
//	go run ./example serve --dir=./site/pb_data
//
// (with ./site/pb_pages beside it). Static files and routing work today; page rendering is being built.
package main

import (
	"log"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"github.com/pockethost/pocketbase/ext/pages"
)

func main() {
	app := pocketbase.New()

	pages.MustRegister(app, pages.Config{})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
