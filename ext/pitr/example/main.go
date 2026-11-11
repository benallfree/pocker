// Command example runs one PocketBase app with point-in-time restore: Litestream copies data.db continuously into
// pb_data/litestream/, and Settings → Backups gets a "Point-in-time restore" section.
//
//	go run ./example serve
//	go run ./example pitr status
//	go run ./example pitr restore --at 2026-11-11T14:05:00Z --to /tmp/then.db
package main

import (
	"log"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"github.com/pockethost/pocketbase/ext/pitr"
)

func main() {
	app := pocketbase.New()

	pitr.MustRegister(app, pitr.Config{
		Enabled:   true,
		Retention: 72 * time.Hour,
		// Bucket: &pitr.Bucket{Endpoint: "...", Bucket: "...", Region: "auto", AccessKey: "...", Secret: "...", Label: "EU"},
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
