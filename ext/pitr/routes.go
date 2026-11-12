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
	g.GET("/settings", func(e *core.RequestEvent) error {
		s, err := LoadSettings(e.App)
		if err != nil {
			return e.InternalServerError("", err)
		}
		return e.JSON(http.StatusOK, toPublic(s))
	})
	g.PATCH("/settings", func(e *core.RequestEvent) error {
		prev, err := LoadSettings(e.App)
		if err != nil {
			return e.InternalServerError("", err)
		}
		var next Settings
		if err := e.BindBody(&next); err != nil {
			return e.BadRequestError("Expected {\"s3\": {...}}.", err)
		}
		next = mergeSecret(next, prev)
		if err := SaveSettings(e.App, next); err != nil {
			return e.BadRequestError(err.Error(), nil)
		}
		// the history keeps going, now to the new destination (a fresh snapshot there)
		if err := a.reattach(); err != nil {
			return e.InternalServerError("Saved, but replication didn't restart: "+err.Error(), err)
		}
		return e.JSON(http.StatusOK, toPublic(next))
	})
	g.POST("/settings/test-s3", func(e *core.RequestEvent) error {
		prev, err := LoadSettings(e.App)
		if err != nil {
			return e.InternalServerError("", err)
		}
		var body Settings
		if err := e.BindBody(&body); err != nil {
			return e.BadRequestError("Expected {\"s3\": {...}}.", err)
		}
		body = mergeSecret(body, prev)
		if err := testS3(body.S3); err != nil {
			return e.BadRequestError("S3 test failed: "+err.Error(), nil)
		}
		return e.NoContent(http.StatusNoContent)
	})
	se.UIExtensions = append(se.UIExtensions, core.UIExtension{Name: Name, FS: UI()})
	return se.Next()
}
