// Command example serves its JS hooks from files embedded in the binary: no pb_hooks folder on disk.
//
//	go run ./example serve
//	curl http://127.0.0.1:8090/hello
package main

import (
	"embed"
	"io/fs"
	"log"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/pockethost/pocketbase/ext/devusers"
	"github.com/pockethost/pocketbase/ext/jsembed"
)

//go:embed hooks
var files embed.FS

func main() {
	hooks, err := fs.Sub(files, "hooks")
	if err != nil {
		log.Fatal(err)
	}
	app := pocketbase.New()
	devusers.Register(app)
	jsvm.MustRegister(app, jsembed.Config(hooks, nil, jsvm.Config{}))
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
