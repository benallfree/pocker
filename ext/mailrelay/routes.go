package mailrelay

import (
	"embed"
	"io/fs"
	"net/http"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

//go:embed ui
var uiFiles embed.FS

// Status is what GET /api/mailrelay returns to the instance's superusers (and what Settings → Mail shows).
type Status struct {
	// Active is true when this instance's mail goes through the relay (SMTP off and sending allowed for it).
	// Send is false when the instance's `send: false` refuses its mail.
	Active    bool   `json:"active"`
	Send      bool   `json:"send"`
	Address   string `json:"address"`
	PerMinute int    `json:"perMinute"`
	Daily     int    `json:"daily"`
	SentToday int    `json:"sentToday"`
}

func (r *appRelay) status(app core.App) Status {
	l := r.limits
	return Status{
		Active:    l.send && !app.Settings().SMTP.Enabled,
		Send:      l.send,
		Address:   r.Address(r.id),
		PerMinute: l.perMinute,
		Daily:     l.daily,
		SentToday: r.sentToday(r.id),
	}
}

func (r *appRelay) bindRoutes(app core.App) {
	ui, _ := fs.Sub(uiFiles, "ui")
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/api/mailrelay", func(e *core.RequestEvent) error {
			return e.JSON(http.StatusOK, r.status(e.App))
		}).Bind(apis.RequireSuperuserAuth())

		se.UIExtensions = append(se.UIExtensions, core.UIExtension{Name: Name, FS: ui})
		return se.Next()
	})
}
