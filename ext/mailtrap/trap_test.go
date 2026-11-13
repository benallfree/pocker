package mailtrap

import (
	"context"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/tools/mailer"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func startTrap(t *testing.T) *Trap {
	t.Helper()
	tr := &Trap{SMTPAddr: freeAddr(t), HTTPAddr: freeAddr(t)}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := tr.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return tr
}

func testMessage() *mailer.Message {
	return &mailer.Message{
		From:    mail.Address{Name: "Support", Address: "support@example.com"},
		To:      []mail.Address{{Address: "user@example.com"}},
		Bcc:     []mail.Address{{Address: "hidden@example.com"}},
		Subject: "Reset your password",
		HTML:    "<p>Hello,</p><p>Click <a href=\"https://x\">here</a>.</p>",
	}
}

func TestTrapAddMessage(t *testing.T) {
	tr := startTrap(t)
	if _, err := tr.AddMessage(testMessage(), "Acme"); err != nil {
		t.Fatal(err)
	}
	got := tr.Messages()
	if len(got) != 1 {
		t.Fatalf("messages = %d", len(got))
	}
	m := got[0]
	if m.Instance != "Acme" || m.Subject != "Reset your password" || !strings.Contains(m.HTML, "Click") || !strings.Contains(m.Text, "Click") {
		t.Fatalf("caught %+v", m)
	}
	if strings.Join(m.RcptTo, ",") != "user@example.com,hidden@example.com" || strings.Contains(string(m.raw), "hidden@") {
		t.Fatalf("envelope %v / bcc in headers", m.RcptTo)
	}

	msg := testMessage()
	msg.Headers = map[string]string{InstanceHeader: "blog.localhost"}
	_, _ = tr.AddMessage(msg, "Acme")
	if got := tr.Messages()[0].Instance; got != "blog.localhost" {
		t.Fatalf("instance header ignored: %q", got)
	}
}

func TestTrapOverSMTP(t *testing.T) {
	tr := startTrap(t)
	host, port, _ := net.SplitHostPort(tr.SMTPAddr)
	p, _ := strconv.Atoi(port)
	if err := (&mailer.SMTPClient{Host: host, Port: p}).Send(testMessage()); err != nil {
		t.Fatal(err)
	}
	got := tr.Messages()
	if len(got) != 1 || got[0].MailFrom != "support@example.com" || got[0].Subject != "Reset your password" {
		t.Fatalf("caught %+v", got)
	}
}
