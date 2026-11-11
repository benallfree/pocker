// Command example runs one PocketBase app with an MCP endpoint at /mcp: the built-in pb_* tools, a Go tool, and a
// JS tool from pb_hooks/mcp.pb.js.
//
//	cd ext/mcp/example && go run . serve
//
// Logins (ext/devusers, local testing only): dashboard admin@example.com / password, app user user@example.com / password.
//
// Until ext/oauth-server lands, sign in with a PocketBase superuser token (the README shows how to mint one), then:
//
//	claude mcp add --transport http pb-example http://127.0.0.1:8090/mcp --header "Authorization: Bearer <token>"
package main

import (
	"log"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/dop251/goja"
	"github.com/pockethost/pocketbase/ext/devusers"
	"github.com/pockethost/pocketbase/ext/mcp"
)

func main() {
	app := pocketbase.New()
	devusers.Register(app) // local testing: superuser admin@example.com and user user@example.com, password "password"

	server := mcp.MustRegister(app, mcp.Config{
		Name:        "pb-example",
		Description: "Example PocketBase app with ext/mcp.",
		// The users collection may sign in too; the pb_* tools stay superuser-only.
		Auth: mcp.PocketBaseTokenAuth{Collections: []string{"_superusers", "users"}},
	})

	// A tool from Go, the way other plugins add theirs.
	server.MustAddTool(mcp.Tool{
		Name:        "whoami",
		Title:       "Who am I",
		Description: "The signed-in record's collection and email.",
		Annotations: mcp.Read,
		Allow:       mcp.RequireAuth(), // any signed-in record
		Handler: func(c *mcp.Call) (any, error) {
			a := c.Auth()
			return map[string]any{"collection": a.Collection().Name, "email": a.Email()}, nil
		},
	})

	jsvm.MustRegister(app, jsvm.Config{
		OnInit: func(vm *goja.Runtime) { mcp.BindJS(vm, app) },
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
