package mailrelay

import (
	"embed"
	"io/fs"
	"net/http"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/pockethost/pocketone/ext/pocker"
)

//go:embed ui
var uiFiles embed.FS

// Status is what GET /api/mailrelay returns to the instance's superusers (and what Settings → Mail shows).
type Status struct {
	// Active is true when this instance's mail goes through the relay (SMTP off and the relay enabled for it).
	Active    bool   `json:"active"`
	Enabled   bool   `json:"enabled"`
	Address   string `json:"address"`
	PerMinute int    `json:"perMinute"`
	Daily     int    `json:"daily"`
	SentToday int    `json:"sentToday"`
}

func (p *Plugin) status(inst pocker.Instance, app core.App) (Status, error) {
	l, err := p.limitsFor(inst)
	if err != nil {
		return Status{}, err
	}
	return Status{
		Active:    l.enabled && !app.Settings().SMTP.Enabled,
		Enabled:   l.enabled,
		Address:   p.Address(inst),
		PerMinute: l.perMinute,
		Daily:     l.daily,
		SentToday: p.sentToday(inst.ID),
	}, nil
}

func (p *Plugin) bindRoutes(inst pocker.Instance, app core.App) {
	ui, _ := fs.Sub(uiFiles, "ui")
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/api/mailrelay", func(e *core.RequestEvent) error {
			s, err := p.status(inst, e.App)
			if err != nil {
				return e.InternalServerError("", err)
			}
			return e.JSON(http.StatusOK, s)
		}).Bind(apis.RequireSuperuserAuth())

		se.UIExtensions = append(se.UIExtensions, core.UIExtension{Name: Name, FS: ui})
		return se.Next()
	})
}
