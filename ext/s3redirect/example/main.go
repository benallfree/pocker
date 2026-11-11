// Command example runs one PocketBase app whose file downloads redirect to the bucket. Turn on S3 storage in
// Settings → Files storage (any S3-compatible bucket: R2, MinIO, AWS), upload a file to a record, then open its URL:
// the app answers 302 with a signed URL that expires after 5 minutes.
//
//	go run ./example serve
//
// Logins (ext/devusers, local testing only): dashboard admin@example.com / password, app user user@example.com / password.
package main

import (
	"log"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"github.com/pockethost/pocketbase/ext/devusers"
	"github.com/pockethost/pocketbase/ext/s3redirect"
)

func main() {
	app := pocketbase.New()
	devusers.Register(app) // local testing: superuser admin@example.com and user user@example.com, password "password"

	s3redirect.MustRegister(app, s3redirect.Config{TTL: 5 * time.Minute})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
