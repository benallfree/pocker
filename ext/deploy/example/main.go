// Command example is a PocketBase that accepts deploys and can deploy: the deploy API (/_pocker/deploy/v1), API
// tokens, remotes, and the deploy/dev/pull/logs commands.
//
//	go run ./example serve --http=127.0.0.1:8090                  # the remote
//	cd myproject && mkdir pb_hooks && echo 'routerAdd("GET", "/hi", (e) => e.string(200, "hi"))' > pb_hooks/main.pb.js
//	go run ./example link http://127.0.0.1:8090 --email admin@example.com   # password: password
//	go run ./example deploy
//	curl http://127.0.0.1:8090/hi
//
// Logins (ext/devusers, local testing only): dashboard admin@example.com / password, app user user@example.com / password.
package main

import (
	"log"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/pockethost/pocketbase/ext/deploy"
	"github.com/pockethost/pocketbase/ext/devusers"
	"github.com/pockethost/pocketbase/ext/remotes"
	"github.com/pockethost/pocketbase/ext/tokens"
)

func main() {
	app := pocketbase.New()
	devusers.Register(app) // local testing: superuser admin@example.com and user user@example.com, password "password"

	jsvm.MustRegister(app, jsvm.Config{}) // deployed pb_hooks and pb_migrations
	tokens.MustRegister(app, tokens.Config{})
	remotes.MustRegister(app, remotes.Config{})
	deploy.MustRegister(app, deploy.Config{}) // deploys land next to pb_data

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
