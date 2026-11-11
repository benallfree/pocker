// Command example is a PocketBase with remotes and API tokens: run two copies, add one as a remote of the other's
// project and sign in.
//
//	go run ./example serve --http=127.0.0.1:8090                # the "remote"
//	go run ./example remote add local http://127.0.0.1:8090    # in a project folder
//	go run ./example login local --email admin@example.com     # password: password; mints a pbt_ token
//	go run ./example remote whoami
//
// Logins (ext/devusers, local testing only): dashboard admin@example.com / password, app user user@example.com / password.
package main

import (
	"log"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"github.com/pockethost/pocketbase/ext/devusers"
	"github.com/pockethost/pocketbase/ext/remotes"
	"github.com/pockethost/pocketbase/ext/tokens"
)

func main() {
	app := pocketbase.New()
	devusers.Register(app) // local testing: superuser admin@example.com and user user@example.com, password "password"

	tokens.MustRegister(app, tokens.Config{})  // so login can mint a revocable API token
	remotes.MustRegister(app, remotes.Config{}) // GET /_pocker/whoami + remote/login/logout/link commands

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
