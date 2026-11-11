// Command example serves many PocketPages sites from one process: pocker in dev mode, with the pages plugin. Every
// hostname is its own instance, with its own folder <pb_root>/<hostname> (created on first use), its own
// database, its own pb_pages and its own sandboxed JS.
//
//	go run . --pbRoot ./pb_root --listen 127.0.0.1:8791
//
// Copy a site into <pb_root>/<name>.localhost/pb_pages and open http://<name>.localhost:8791 (browsers resolve
// *.localhost to this machine). Edit pages and reload; an idle instance sleeps and starts again on the next request.
//
// Logins on every instance (ext/devusers, local testing only): dashboard admin@example.com / password, app user
// user@example.com / password.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/pockethost/pocketbase/ext/devusers"
	"github.com/pockethost/pocketbase/ext/pages"
	"github.com/pockethost/pocketbase/ext/pages/pockerplugin"
	"github.com/pockethost/pocketbase/ext/pocker"
)

func main() {
	pbRoot := flag.String("pbRoot", "./pb_root", "folder holding one folder per instance")
	listen := flag.String("listen", "127.0.0.1:8791", "listen address")
	flag.Parse()

	cfg, err := pocker.LoadConfig("pocker.yaml") // defaults apply when the file is missing
	if err != nil {
		log.Fatal(err)
	}
	cfg.Dev = true // any hostname is an instance; drop this for production (instances.yaml required)
	cfg.PBRoot = *pbRoot
	cfg.Listen = *listen

	srv := &pocker.Server{
		Config:  cfg,
		Plugins: []pocker.Plugin{pockerplugin.New(pages.Config{})},
		SetupApp: func(inst pocker.Instance, app core.App) error {
			return devusers.Ensure(app) // local testing: admin@example.com and user@example.com, password "password"
		},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := srv.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
