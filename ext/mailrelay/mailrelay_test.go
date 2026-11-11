package mailrelay

import (
	"context"
	"errors"
	"net"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/mailer"
	"github.com/pockethost/pocketbase/ext/pocker"
)

type sent struct {
	from string
	to   []string
	raw  string
}

type fakeUpstream struct {
	mu   sync.Mutex
	msgs []sent
}

func (f *fakeUpstream) Send(_ context.Context, from string, to []string, raw []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, sent{from, to, string(raw)})
	return nil
}

func newApp(t *testing.T) core.App {
	t.Helper()
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })
	return app
}

func msg(to ...string) *mailer.Message {
	m := &mailer.Message{
		From:    mail.Address{Name: "My App", Address: "owner@myapp.dev"},
		Subject: "Reset your password",
		HTML:    "<p>Click <a href=\"https://x\">here</a></p>",
		Text:    "Click here",
	}
	for _, a := range to {
		m.To = append(m.To, mail.Address{Address: a})
	}
	return m
}

func setup(t *testing.T, cfg Config, inst pocker.Instance) (*Plugin, core.App, *fakeUpstream) {
	t.Helper()
	up := &fakeUpstream{}
	if cfg.Upstream == nil {
		cfg.Upstream = up
	}
	if cfg.Domain == "" {
		cfg.Domain = "mail.test"
	}
	p := New(cfg)
	app := newApp(t)
	if err := p.InitApp(inst, app); err != nil {
		t.Fatal(err)
	}
	return p, app, up
}

func TestRelaysWhenSMTPIsOff(t *testing.T) {
	_, app, up := setup(t, Config{}, pocker.Instance{ID: "blog.localhost"})

	m := msg("alice@example.org")
	m.Bcc = []mail.Address{{Address: "hidden@example.org"}}
	if err := app.NewMailClient().Send(m); err != nil {
		t.Fatal(err)
	}
	if len(up.msgs) != 1 {
		t.Fatalf("upstream got %d messages, want 1", len(up.msgs))
	}
	s := up.msgs[0]
	if s.from != "blog@mail.test" {
		t.Errorf("envelope from = %q", s.from)
	}
	if strings.Join(s.to, ",") != "alice@example.org,hidden@example.org" {
		t.Errorf("envelope to = %v", s.to)
	}
	for _, want := range []string{"From: My App <blog@mail.test>", "Reply-To: owner@myapp.dev", "X-Pocker-Instance: blog.localhost", "Subject: Reset your password"} {
		if !strings.Contains(s.raw, want) {
			t.Errorf("message lacks %q:\n%s", want, s.raw)
		}
	}
	if strings.Contains(s.raw, "hidden@example.org") {
		t.Error("Bcc leaked into the headers")
	}
}

func TestOwnSMTPWins(t *testing.T) {
	_, app, up := setup(t, Config{}, pocker.Instance{ID: "a"})

	// a closed port: PocketBase's own SMTP client must be the one that tries (and fails)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	app.Settings().SMTP.Enabled = true
	app.Settings().SMTP.Host = "127.0.0.1"
	app.Settings().SMTP.Port = port

	if err := app.NewMailClient().Send(msg("a@example.org")); err == nil {
		t.Fatal("expected PocketBase's SMTP client to fail on a closed port")
	}
	if len(up.msgs) != 0 {
		t.Fatal("relay used although the instance has its own SMTP")
	}
}

func TestDisabledForInstance(t *testing.T) {
	inst := pocker.Instance{ID: "a", Plugins: map[string]any{Name: map[string]any{"enabled": false}}}
	_, app, up := setup(t, Config{}, inst)
	if err := app.NewMailClient().Send(msg("a@example.org")); !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
	if len(up.msgs) != 0 {
		t.Fatal("sent although disabled")
	}
}

func TestLimits(t *testing.T) {
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	inst := pocker.Instance{ID: "a", Plugins: map[string]any{Name: map[string]any{"per_minute": 2, "daily": 3}}}
	_, app, up := setup(t, Config{Now: clock}, inst)
	send := func() error { return app.NewMailClient().Send(msg("a@example.org")) }

	for i := 0; i < 2; i++ {
		if err := send(); err != nil {
			t.Fatal(err)
		}
	}
	if err := send(); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("3rd in a minute: err = %v", err)
	}
	now = now.Add(time.Minute)
	if err := send(); err != nil {
		t.Fatal(err)
	}
	if err := send(); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("4th in a day: err = %v", err)
	}
	now = now.Add(24 * time.Hour)
	if err := send(); err != nil {
		t.Fatalf("next day: %v", err)
	}
	if len(up.msgs) != 4 {
		t.Fatalf("sent %d, want 4", len(up.msgs))
	}

	if err := app.NewMailClient().Send(msg("1@x.org", "2@x.org", "3@x.org", "4@x.org", "5@x.org", "6@x.org")); !errors.Is(err, ErrTooManyRcpts) {
		t.Fatalf("6 recipients: err = %v", err)
	}
}

func TestDisplayNameSanitized(t *testing.T) {
	if got := displayName("PayPal <security@paypal.com> https://evil", ""); strings.ContainsAny(got, "@<>") || strings.Contains(got, "https://") {
		t.Fatalf("displayName = %q", got)
	}
	if got := displayName("", "Acme"); got != "Acme" {
		t.Fatalf("fallback = %q", got)
	}
}

// End to end: two instances, the real SMTP upstream, one trap.
func TestTrapCatchesEveryInstance(t *testing.T) {
	smtpLn, _ := net.Listen("tcp", "127.0.0.1:0")
	httpLn, _ := net.Listen("tcp", "127.0.0.1:0")
	smtpAddr, httpAddr := smtpLn.Addr().String(), httpLn.Addr().String()
	smtpLn.Close()
	httpLn.Close()

	tr := &mailer.Trap{SMTPAddr: smtpAddr, HTTPAddr: httpAddr}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tr.Start(ctx); err != nil {
		t.Fatal(err)
	}

	p := New(Config{Domain: "mail.localhost", Upstream: SMTPUpstream{Addr: smtpAddr, Timeout: 5 * time.Second}})
	for _, id := range []string{"alpha.localhost", "beta.localhost"} {
		app := newApp(t)
		if err := p.InitApp(pocker.Instance{ID: id}, app); err != nil {
			t.Fatal(err)
		}
		if err := app.NewMailClient().Send(msg("user@example.com")); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}

	got := tr.Messages()
	if len(got) != 2 {
		t.Fatalf("trap has %d messages, want 2", len(got))
	}
	seen := map[string]string{}
	for _, m := range got {
		seen[m.Instance] = m.From
		if m.Subject != "Reset your password" || !strings.Contains(m.HTML, "Click") || m.Text != "Click here" {
			t.Errorf("parsed badly: %+v", m)
		}
	}
	if seen["alpha.localhost"] != "My App <alpha@mail.localhost>" || seen["beta.localhost"] != "My App <beta@mail.localhost>" {
		t.Fatalf("instances/from = %v", seen)
	}
}

func TestHTMLToText(t *testing.T) {
	got := htmlToText("<html><head><style>p{}</style></head><body><p>Hello,</p><p>Click <a href=\"x\">here</a> &amp; go.</p></body></html>")
	if got != "Hello,\nClick here & go." {
		t.Fatalf("htmlToText = %q", got)
	}
}
