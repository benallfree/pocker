// Command example is a PocketBase that is an OAuth server: `pocketbase login` signs in to it in the browser, and MCP
// clients can register themselves (dynamic registration is on here).
//
//	go run ./example serve --http=127.0.0.1:8090
//	go run ./example login http://127.0.0.1:8090              # opens the browser; --device for a code
//	go run ./example remote whoami http://127.0.0.1:8090
//
// Logins (ext/devusers, local testing only): dashboard admin@example.com / password, app user user@example.com / password.
package main

import (
	"log"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"github.com/pockethost/pocketone/ext/devusers"
	oauthserver "github.com/pockethost/pocketone/ext/oauth-server"
	"github.com/pockethost/pocketone/ext/remotes"
	"github.com/pockethost/pocketone/ext/tokens"
)

func main() {
	app := pocketbase.New()
	devusers.Register(app) // local testing: superuser admin@example.com and user user@example.com, password "password"

	tokens.MustRegister(app, tokens.Config{})   // OAuth access tokens are API tokens
	remotes.MustRegister(app, remotes.Config{}) // whoami discovery + login/remote commands
	oauthserver.MustRegister(app, oauthserver.Config{
		DynamicRegistration: true, // MCP connectors register themselves
		Branding:            oauthserver.Branding{Accent: "#4f46e5", Footer: "Example OAuth server"},
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
