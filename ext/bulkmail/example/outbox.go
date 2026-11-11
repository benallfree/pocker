package main

import (
	"log"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// outboxCollection holds the mail the app would have sent while SMTP is off. Superusers only.
const outboxCollection = "outbox"

// registerOutbox makes the dashboard the inbox for local testing: while SMTP is off (Settings > Mail), every mail the
// app sends, bulk mail included, is saved to the outbox collection instead of going out. Turn SMTP on to send for real.
func registerOutbox(app core.App) {
	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		if err := ensureOutbox(e.App); err != nil {
			return err
		}
		if !e.App.Settings().SMTP.Enabled {
			log.Printf("SMTP is off: mail goes to the %q collection in the dashboard", outboxCollection)
		}
		return nil
	})

	app.OnMailerSend().BindFunc(func(e *core.MailerEvent) error {
		if e.App.Settings().SMTP.Enabled {
			return e.Next()
		}
		col, err := e.App.FindCachedCollectionByNameOrId(outboxCollection)
		if err != nil {
			return err
		}
		m := e.Message
		to := make([]string, 0, len(m.To)+len(m.Cc)+len(m.Bcc))
		for _, a := range append(append(m.To, m.Cc...), m.Bcc...) {
			to = append(to, a.String())
		}
		r := core.NewRecord(col)
		r.Set("from", m.From.String())
		r.Set("to", strings.Join(to, ", "))
		r.Set("subject", m.Subject)
		r.Set("html", m.HTML)
		r.Set("text", m.Text)
		r.Set("headers", m.Headers)
		// Not calling e.Next(): the mail stops here.
		return e.App.Save(r)
	})
}

func ensureOutbox(app core.App) error {
	if _, err := app.FindCollectionByNameOrId(outboxCollection); err == nil {
		return nil
	}
	c := core.NewBaseCollection(outboxCollection)
	c.Fields.Add(
		&core.TextField{Name: "from"},
		&core.TextField{Name: "to"},
		&core.TextField{Name: "subject"},
		&core.EditorField{Name: "html", MaxSize: 5 << 20},
		&core.TextField{Name: "text", Max: 5 << 20},
		&core.JSONField{Name: "headers"},
		&core.AutodateField{Name: "created", OnCreate: true},
	)
	c.AddIndex("idx_outbox_created", false, "created", "")
	return app.Save(c)
}
