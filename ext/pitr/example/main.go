// Command example runs one PocketBase app with point-in-time restore: Litestream copies data.db continuously into
// pb_data/litestream/, and Settings → Backups gets a "Point-in-time restore" section.
//
//	go run ./example serve
//	go run ./example pitr status
//	go run ./example pitr restore --at 2026-11-11T14:05:00Z --to /tmp/then.db
//
// Logins (ext/devusers, local testing only): dashboard admin@example.com / password, app user user@example.com / password.
package main

import (
	"log"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"github.com/pockethost/pocketbase/ext/devusers"
	"github.com/pockethost/pocketbase/ext/pitr"
)

func main() {
	app := pocketbase.New()
	devusers.Register(app) // local testing: superuser admin@example.com and user user@example.com, password "password"

	pitr.MustRegister(app, pitr.Config{
		Retention: 72 * time.Hour, // on by default; Enabled: false turns it off
		// Bucket: &pitr.Bucket{Endpoint: "...", Bucket: "...", Region: "auto", AccessKey: "...", Secret: "...", Label: "EU"},
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
