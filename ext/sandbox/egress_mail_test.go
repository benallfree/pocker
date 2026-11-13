package sandbox

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/netip"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	_ "ganigeorgiev.com/pocketbase/pocketbase/migrations" // system migrations, so Bootstrap has its tables
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/mailer"
)

// fakeSMTP accepts one message on 127.0.0.1 and returns it on the channel.
func fakeSMTP(t *testing.T) (string, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() {
		defer ln.Close()
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		w := func(s string) { fmt.Fprintf(c, "%s\r\n", s) }
		w("220 fake ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					w("250 queued")
					got <- data.String()
					continue
				}
				data.WriteString(line)
				continue
			}
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(cmd, "DATA"):
				inData = true
				w("354 go ahead")
			case strings.HasPrefix(cmd, "QUIT"):
				w("221 bye")
				return
			default:
				w("250 ok")
			}
		}
	}()
	return ln.Addr().String(), got
}

// The app's own SMTP and $http.send's client both go through the egress policy; 127.0.0.1 is allowed here so the
// test needs no second loopback address (ext/pocker's TestEgressFromApps covers the rest on Linux).
func TestEgressMailAndHTTPClient(t *testing.T) {
	InstallEgressPolicy(EgressPolicy{Allow: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}})

	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
	BindMail(app)
	// a guard after BindMail: reached only when BindMail passed the message on; it stops there (no sendmail)
	passedOn := false
	app.OnMailerSend().Bind(&hook.Handler[*core.MailerEvent]{
		Priority: 1<<30 + 1,
		Func:     func(e *core.MailerEvent) error { passedOn = true; return nil },
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	defer app.ResetBootstrapState()

	msg := func() *mailer.Message {
		return &mailer.Message{
			From:    mail.Address{Address: "app@example.com"},
			To:      []mail.Address{{Address: "user@example.com"}},
			Bcc:     []mail.Address{{Address: "hidden@example.com"}},
			Subject: "sandbox egress test",
			HTML:    "<p>hello</p>",
		}
	}
	smtp := &app.Settings().SMTP

	// SMTP off: not ours, the next handler runs
	smtp.Enabled = false
	if err := app.NewMailClient().Send(msg()); err != nil || !passedOn {
		t.Fatalf("SMTP off: err=%v passedOn=%v", err, passedOn)
	}

	// SMTP to a private address: refused by the policy, before any connection
	passedOn = false
	smtp.Enabled, smtp.Host, smtp.Port = true, "10.255.255.1", 25
	if err := app.NewMailClient().Send(msg()); err == nil || !strings.Contains(err.Error(), "not allowed") || passedOn {
		t.Fatalf("private SMTP: err=%v passedOn=%v", err, passedOn)
	}

	// SMTP to an allowed address: delivered by BindMail itself, Bcc only in the envelope
	addr, got := fakeSMTP(t)
	host, port, _ := net.SplitHostPort(addr)
	smtp.Host = host
	fmt.Sscanf(port, "%d", &smtp.Port)
	if err := app.NewMailClient().Send(msg()); err != nil || passedOn {
		t.Fatalf("allowed SMTP: err=%v passedOn=%v", err, passedOn)
	}
	select {
	case m := <-got:
		if !strings.Contains(m, "sandbox egress test") || strings.Contains(strings.ToLower(m), "hidden@example.com") {
			t.Fatalf("unexpected message (subject missing or Bcc in headers):\n%s", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fake SMTP got no message")
	}

	// $http.send's client: policy applies (private address refused), body cap fails the request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 4096)))
	}))
	defer srv.Close()
	sb := &Sandbox{HTTPSendMaxTimeout: 5, HTTPSendMaxBody: 1024}
	c := sb.httpClient()
	if _, err := c.Get("http://169.254.169.254/latest/meta-data/"); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("metadata: expected refusal, got %v", err)
	}
	if _, err := c.Get(srv.URL); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("big body: expected the cap, got %v", err)
	}
	small := &Sandbox{HTTPSendMaxBody: 1 << 20}
	res, err := small.httpClient().Get(srv.URL)
	if err != nil || res.ContentLength != 4096 {
		t.Fatalf("under the cap: %v %+v", err, res)
	}
	if c.Timeout != 5*time.Second || small.httpClient().Timeout != 0 {
		t.Fatalf("timeouts: %v / %v", c.Timeout, small.httpClient().Timeout)
	}
}
