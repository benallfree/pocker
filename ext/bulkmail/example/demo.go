package main

import (
	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// demoViewName is a view a campaign can use as its audience. It's created once; edit or delete it like any view.
const demoViewName = "newsletter_audience"

// registerDemoView shows the pattern for dynamic audiences: a view selects who gets mail and computes the columns
// templates use ({{record.name}}, {{record.domain}}), and the campaign picks it with its email field.
func registerDemoView(app core.App) {
	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		if _, err := e.App.FindCollectionByNameOrId(demoViewName); err == nil {
			return nil
		}
		if _, err := e.App.FindCollectionByNameOrId("users"); err != nil {
			return nil // no users collection to build on
		}
		v := core.NewViewCollection(demoViewName)
		v.ViewQuery = `SELECT
	id,
	email,
	name,
	lower(substr(email, instr(email, '@') + 1)) AS domain,
	created AS joined
FROM users
WHERE verified = TRUE`
		return e.App.Save(v)
	})
}
