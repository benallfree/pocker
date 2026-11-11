// Command pocker-extension is a Pocker build with one extension: a Go route and an admin dashboard page,
// added to every instance. See docs/plugins.md.
package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/pockethost/pocketbase/ext/pocker"
)

//go:embed ui
var uiFiles embed.FS

func main() {
	cfg, err := pocker.LoadConfig("pocker.yaml") // defaults apply when the file is missing
	if err != nil {
		log.Fatal(err)
	}
	cfg.Dev = true // any hostname; drop this for production (instances.yaml required)

	ui, _ := fs.Sub(uiFiles, "ui")

	srv := &pocker.Server{
		Config: cfg,
		// Runs for every instance's app after its migrations, before it serves.
		SetupApp: func(inst pocker.Instance, app core.App) error {
			app.OnServe().BindFunc(func(se *core.ServeEvent) error {
				// a Go route, superuser only
				se.Router.GET("/api/hello", func(e *core.RequestEvent) error {
					return e.JSON(http.StatusOK, map[string]string{"instance": inst.ID})
				}).Bind(apis.RequireSuperuserAuth())

				// an admin dashboard extension: ui/main.js is loaded by the dashboard before its router starts
				se.UIExtensions = append(se.UIExtensions, core.UIExtension{Name: "hello", FS: ui})
				return se.Next()
			})
			return nil
		},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := srv.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
