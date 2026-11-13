// Package mailtrap catches an app's mail in development: in dev mode, an app whose SMTP is off sends to a built-in
// mail catcher (like MailDev or Mailpit) instead of the host's sendmail, and you read it at http://127.0.0.1:8025.
//
// It needs no change to PocketBase: it binds OnMailerSend last and, when it takes a message, never calls e.Next(),
// so sendmail never runs. Outside dev mode, or with SMTP on, it does nothing.
//
// A single app:
//
//	mailtrap.MustRegister(app)
//
// Under pocker, add [New] to Server.Plugins; every dev instance's mail lands in the one inbox, labeled by instance.
//
// The inbox listens on 127.0.0.1:8025 (SMTP 127.0.0.1:1025); PB_MAILTRAP_HTTP and PB_MAILTRAP_SMTP move it.
package mailtrap

import (
	"ganigeorgiev.com/pocketbase/pocketbase/ceplugin"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
)

// Name is the plugin's name (ceplugin / pocker config key).
const Name = "mailtrap"

const reason = "No SMTP configured"

// Plugin is the ceplugin form, for pocker's Server.Plugins and ceplugin.MustRegister.
type Plugin struct{}

// New returns the plugin.
func New() *Plugin { return &Plugin{} }

// Name implements ceplugin.Plugin.
func (p *Plugin) Name() string { return Name }

// Register implements ceplugin.Plugin.
func (p *Plugin) Register(app core.App, _ ceplugin.Config) error {
	Register(app)
	return nil
}

// MustRegister routes app's mail to the dev mail trap while the app is in dev mode with SMTP off.
func MustRegister(app core.App) { Register(app) }

// Register is MustRegister; it can't fail.
func Register(app core.App) {
	// at startup, so the inbox address shows before the first mail
	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		if active(e.App) {
			_, _ = Default(reason)
		}
		return nil
	})
	app.OnMailerSend().Bind(&hook.Handler[*core.MailerEvent]{
		Id:       "__mailtrap__",
		Priority: 1 << 30, // last: the app's own onMailerSend hooks run (and may change the message) first
		Func: func(e *core.MailerEvent) error {
			if !active(e.App) {
				return e.Next()
			}
			t, _ := Default(reason) // if its ports are taken it still keeps mail in memory and prints each one
			// never e.Next() here: that would run sendmail on the host
			_, err := t.AddMessage(e.Message, e.App.Settings().Meta.AppName)
			return err
		},
	})
}

func active(app core.App) bool {
	return app.IsDev() && !app.Settings().SMTP.Enabled
}
