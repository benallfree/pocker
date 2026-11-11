package pitr

import (
	"embed"
	"io/fs"
	"net/http"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

//go:embed ui
var uiFiles embed.FS

// UI returns the admin extension (main.js), mounted on Settings → Backups.
func UI() fs.FS {
	sub, _ := fs.Sub(uiFiles, "ui")
	return sub
}

func (a *appPITR) onServe(se *core.ServeEvent) error {
	if err := a.attach(); err != nil {
		return err
	}
	g := se.Router.Group("/api/" + Name)
	g.Bind(apis.RequireSuperuserAuth())
	g.GET("", func(e *core.RequestEvent) error {
		st, err := a.Status(e.Request.Context())
		if err != nil {
			return e.InternalServerError("", err)
		}
		return e.JSON(http.StatusOK, st)
	})
	g.POST("/restore", func(e *core.RequestEvent) error {
		var body struct {
			At time.Time `json:"at"`
		}
		if err := e.BindBody(&body); err != nil {
			return e.BadRequestError("Expected {\"at\": \"<RFC 3339 time>\"}.", err)
		}
		if err := a.Restore(e.Request.Context(), body.At); err != nil {
			return e.BadRequestError(err.Error(), nil)
		}
		return e.JSON(http.StatusAccepted, map[string]any{"restoring": true, "at": body.At})
	})
	se.UIExtensions = append(se.UIExtensions, core.UIExtension{Name: Name, FS: UI()})
	return se.Next()
}
