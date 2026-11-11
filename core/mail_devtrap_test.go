package core_test

// POCKER: dev mode sends to the dev mail trap instead of sendmail.

import (
	"net"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/mailer"
)

func freeTCPAddr(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func TestNewMailClientDevTrap(t *testing.T) {
	t.Setenv("PB_MAILTRAP_HTTP", freeTCPAddr(t))
	t.Setenv("PB_MAILTRAP_SMTP", freeTCPAddr(t))

	for _, tc := range []struct {
		dev, smtp bool
		want      string
	}{
		{dev: true, want: "trap"},
		{dev: false, want: "sendmail"},
		{dev: true, smtp: true, want: "smtp"},
	} {
		app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir(), IsDev: tc.dev})
		if err := app.Bootstrap(); err != nil {
			t.Fatal(err)
		}
		app.Settings().SMTP.Enabled = tc.smtp
		app.Settings().SMTP.Host = "127.0.0.1"

		var got string
		switch app.NewMailClient().(type) {
		case *mailer.TrapClient:
			got = "trap"
		case *mailer.Sendmail:
			got = "sendmail"
		case *mailer.SMTPClient:
			got = "smtp"
		}
		if got != tc.want {
			t.Errorf("dev=%v smtp=%v: client = %s, want %s", tc.dev, tc.smtp, got, tc.want)
		}
		_ = app.ResetBootstrapState()
	}
}
