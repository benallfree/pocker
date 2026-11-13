// Command example runs one PocketBase app with an MCP endpoint at /mcp: the built-in pb_* tools, a Go tool, and a
// JS tool from pb_hooks/mcp.pb.js.
//
//	cd ext/mcp/example && go run . serve
//
// Logins (ext/devusers, local testing only): dashboard admin@example.com / password, app user user@example.com / password.
//
// Sign-in is OAuth (ext/oauth-server + ext/tokens): add the endpoint to Claude Code and sign in in the browser as
// admin@example.com when it asks.
//
//	claude mcp add --transport http pb-example http://127.0.0.1:8090/mcp
//	claude mcp login pb-example   (or /mcp in a session)
package main

import (
	"log"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/dop251/goja"
	"github.com/pockethost/pocketone/ext/devusers"
	"github.com/pockethost/pocketone/ext/mcp"
	oauthserver "github.com/pockethost/pocketone/ext/oauth-server"
	"github.com/pockethost/pocketone/ext/tokens"
)

func main() {
	app := pocketbase.New()
	devusers.Register(app) // local testing: superuser admin@example.com and user user@example.com, password "password"

	// OAuth sign-in: API tokens + the authorization server. ext/mcp picks it up on serve, registers the mcp:read and
	// mcp:write scopes and turns on dynamic client registration.
	tokens.MustRegister(app, tokens.Config{})
	oauthserver.MustRegister(app, oauthserver.Config{})

	server := mcp.MustRegister(app, mcp.Config{
		Name:        "pb-example",
		Description: "Example PocketBase app with ext/mcp.",
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
