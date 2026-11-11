// Command example runs one PocketBase app with API tokens: Settings → API tokens in the dashboard, the /api/tokens
// routes, and the `token` command.
//
//	go run ./example serve
//	go run ./example token create --superuser admin@example.com --name ci
//	curl -H "Authorization: Bearer pbt_..." http://127.0.0.1:8090/api/collections
//
// Logins (ext/devusers, local testing only): dashboard admin@example.com / password, app user user@example.com / password.
package main

import (
	"log"
	"net/http"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/pockethost/pocketbase/ext/devusers"
	"github.com/pockethost/pocketbase/ext/tokens"
)

func main() {
	app := pocketbase.New()
	devusers.Register(app) // local testing: superuser admin@example.com and user user@example.com, password "password"

	tk := tokens.MustRegister(app, tokens.Config{
		Collections: []string{"_superusers", "users"}, // who may own tokens
	})
	// a plugin's scope: a token limited to "hello" only authenticates under /api/hello
	tk.RegisterScope(tokens.Scope{Name: "hello", Description: "Say hello", Paths: []string{"/api/hello"}})

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/api/hello", func(e *core.RequestEvent) error {
			return e.JSON(http.StatusOK, map[string]any{"hello": e.Auth.Email()})
		}).Bind(apis.RequireAuth(), tokens.RequireScope("hello"))
		return se.Next()
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
