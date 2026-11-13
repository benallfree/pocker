// Command example runs one PocketBase app whose JS hooks are sandboxed: file access is confined to the app folder,
// host primitives ($os.exec, $os.exit, ...) are disabled, and every JS call has a time limit.
//
//	go run ./example serve
//
// Logins (ext/devusers, local testing only): dashboard admin@example.com / password, app user user@example.com / password.
package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/pockethost/pocketone/ext/devusers"
	"github.com/pockethost/pocketone/ext/sandbox"
)

func main() {
	app := pocketbase.New()
	devusers.Register(app) // local testing: superuser admin@example.com and user user@example.com, password "password"

	dir, _ := os.Getwd()
	sb := &sandbox.Sandbox{
		Dir:          dir,                                 // JS may only touch this folder
		Env:          map[string]string{"GREETING": "hi"}, // the only environment JS sees
		MaxExecution: 5 * time.Second,
	}

	jsvm.MustRegister(app, jsvm.Config{
		HooksDir:   filepath.Join(dir, "pb_hooks"),
		Extensions: []jsvm.Extension{sb}, // OnInit, OnCall and HTTPClient
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
