package mailtrap

import (
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	_ "ganigeorgiev.com/pocketbase/pocketbase/migrations" // system migrations, so Bootstrap has its tables
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
)

// The plugin takes an app's mail only in dev mode with SMTP off, and then sendmail never runs.
func TestRegisterRoutesDevMailToTrap(t *testing.T) {
	t.Setenv("PB_MAILTRAP_HTTP", freeAddr(t))
	t.Setenv("PB_MAILTRAP_SMTP", freeAddr(t))

	for _, tc := range []struct {
		name      string
		dev, smtp bool
		caught    bool
	}{
		{"dev, no SMTP", true, false, true},
		{"not dev", false, false, false},
		{"dev with SMTP", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir(), IsDev: tc.dev})
			Register(app)
			if err := app.Bootstrap(); err != nil {
				t.Fatal(err)
			}
			defer app.ResetBootstrapState()
			app.Settings().SMTP.Enabled = tc.smtp
			app.Settings().Meta.AppName = "Acme " + tc.name

			// a guard after the trap: reached only when the trap passed the message on; it stops there, so the test
			// never runs the host's sendmail or dials SMTP
			passedOn := false
			app.OnMailerSend().Bind(&hook.Handler[*core.MailerEvent]{
				Priority: 1<<30 + 1,
				Func:     func(e *core.MailerEvent) error { passedOn = true; return nil },
			})

			before := 0
			if tr, _ := Default(reason); tr != nil {
				before = len(tr.Messages())
			}
			msg := testMessage()
			msg.Subject = "probe " + tc.name
			if err := app.NewMailClient().Send(msg); err != nil {
				t.Fatal(err)
			}
			if passedOn == tc.caught {
				t.Fatalf("passed on to the default sender = %v, caught expected = %v", passedOn, tc.caught)
			}

			tr, _ := Default(reason)
			got := tr.Messages()
			caught := len(got) > before && strings.HasPrefix(got[0].Subject, "probe "+tc.name)
			if caught != tc.caught {
				t.Fatalf("caught = %v, want %v", caught, tc.caught)
			}
			if caught && got[0].Instance != "Acme "+tc.name {
				t.Fatalf("label = %q", got[0].Instance)
			}
		})
	}
}
