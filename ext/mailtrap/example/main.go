// Command example runs one app in dev mode with the dev mail trap: request a password reset for user@example.com
// in the dashboard and read the mail at http://127.0.0.1:8025.
//
//	go run ./example serve --dev
package main

import (
	"log"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"github.com/pockethost/pocketone/ext/devusers"
	"github.com/pockethost/pocketone/ext/mailtrap"
)

func main() {
	app := pocketbase.New()
	devusers.Register(app)
	mailtrap.MustRegister(app)
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
