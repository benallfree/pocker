// Command example is a Pocker build with mailrelay, relaying into PocketBase CE's dev mail trap: every instance
// without its own SMTP sends as <instance>@mail.localhost, and all of it lands in one inbox.
//
//	go run ./example --dev
//
// Then trigger mail on any instance, for example a password reset (each instance has the test user below):
//
//	curl -X POST http://alpha.localhost:8090/api/collections/users/request-password-reset -d email=user@example.com
//	curl -X POST http://beta.localhost:8090/api/collections/users/request-password-reset -d email=user@example.com
//
// and read it in PocketBase CE's dev mail trap at http://127.0.0.1:8025 (filter by instance; PB_MAILTRAP_HTTP and
// PB_MAILTRAP_SMTP change its ports). Settings → Mail in each dashboard shows the relay.
//
// Logins (ext/devusers, local testing only): dashboard admin@example.com / password, app user user@example.com / password.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/mailer"
	"github.com/pockethost/pocketbase/ext/devusers"
	"github.com/pockethost/pocketbase/ext/mailrelay"
	"github.com/pockethost/pocketbase/ext/pocker"
)

func main() {
	dev := flag.Bool("dev", false, "serve any hostname (alpha.localhost, beta.localhost, ...); otherwise instances.yaml is required")
	listen := flag.String("listen", "", "HTTP address for the instances (default from pocker.yaml, else :8090)")
	flag.Parse()

	cfg, err := pocker.LoadConfig("pocker.yaml") // defaults apply when the file is missing
	if err != nil {
		log.Fatal(err)
	}
	cfg.Dev = cfg.Dev || *dev
	if *listen != "" {
		cfg.Listen = *listen
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// PocketBase CE's dev mail trap stands in for the operator's real relay (SES, a provider's SMTP, ...)
	inbox, err := mailer.DevTrap("mailrelay example: the relay sends to the trap")
	if err != nil {
		log.Fatal(err)
	}

	srv := &pocker.Server{
		Config: cfg,
		Plugins: []pocker.Plugin{
			mailrelay.New(mailrelay.Config{
				Domain:   "mail.localhost",
				Upstream: mailrelay.SMTPUpstream{Addr: inbox.SMTPAddr},
			}),
		},
		// local testing: every instance gets superuser admin@example.com and user user@example.com, password "password"
		SetupApp: func(_ pocker.Instance, app core.App) error { return devusers.Ensure(app) },
	}
	if err := srv.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
