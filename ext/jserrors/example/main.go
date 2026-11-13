// Command example shows hook errors pointing at the .pb.js file: run it in dev mode next to example/pb_hooks and
// request /boom; the logged error names pb_hooks/main.pb.js:4:... instead of <cwd>/pb.js:1:...
//
//	cd example && go run . serve --dev
//	curl http://127.0.0.1:8090/boom
package main

import (
	"log"

	"ganigeorgiev.com/pocketbase/pocketbase"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/pockethost/pocketone/ext/devusers"
	"github.com/pockethost/pocketone/ext/jserrors"
)

func main() {
	app := pocketbase.New()
	devusers.Register(app)
	jsvm.MustRegister(app, jsvm.Config{HooksDir: "pb_hooks", Extensions: []jsvm.Extension{jserrors.New()}})
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
