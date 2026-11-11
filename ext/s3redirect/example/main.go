// Command example runs one PocketBase app whose file downloads redirect to an S3 bucket, with nothing to set up:
// it starts its own S3-compatible bucket in-process (gofakes3, stored in pb_data/demo-s3.bolt), points the app's
// Files storage at it, and seeds a "demo_files" record with an image and a protected document.
//
//	go run . serve
//
// Open the URLs it prints. Each answers 302 with a signed URL; the bucket logs every fetch and only serves it if the
// signature is valid and unexpired, checked independently with the AWS SDK's SigV4 signer.
//
// Set S3REDIRECT_DEMO=0 to skip the demo bucket and use whatever Files storage you configure in the dashboard.
//
// Logins (ext/devusers, local testing only): dashboard admin@example.com / password, app user user@example.com / password.
package main

import (
	"log"
	"os"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"github.com/pockethost/pocketbase/ext/devusers"
	"github.com/pockethost/pocketbase/ext/s3redirect"
)

func main() {
	app := pocketbase.New()
	devusers.Register(app) // local testing: superuser admin@example.com and user user@example.com, password "password"

	s3redirect.MustRegister(app, s3redirect.Config{TTL: 5 * time.Minute})

	if os.Getenv("S3REDIRECT_DEMO") != "0" {
		addr := os.Getenv("S3REDIRECT_DEMO_ADDR")
		if addr == "" {
			addr = "127.0.0.1:9199"
		}
		registerDemo(app, addr)
	}

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
