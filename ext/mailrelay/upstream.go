package mailrelay

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"time"
)

// SMTPUpstream relays through an SMTP server (a provider's SMTP endpoint, a local MTA, or [trap.Trap] in development).
//
// It dials with its own dialer, not PocketBase's: Pocker's egress policy (public addresses only) is for tenants, and
// the operator's relay may well be on a private address.
type SMTPUpstream struct {
	// Addr is host:port.
	Addr string

	// Username and Password, when set, authenticate with PLAIN (only over TLS, or to localhost).
	Username string
	Password string

	// ImplicitTLS dials TLS directly (port 465). Otherwise STARTTLS is used when the server offers it.
	ImplicitTLS bool

	// Timeout bounds the whole exchange (default 30 s, or the context's deadline if sooner).
	Timeout time.Duration
}

// Send implements [Upstream].
func (u SMTPUpstream) Send(ctx context.Context, from string, to []string, raw []byte) error {
	host, _, err := net.SplitHostPort(u.Addr)
	if err != nil {
		return fmt.Errorf("smtp upstream address: %w", err)
	}
	timeout := u.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	d := &net.Dialer{}
	var conn net.Conn
	if u.ImplicitTLS {
		conn, err = (&tls.Dialer{NetDialer: d, Config: &tls.Config{ServerName: host}}).DialContext(ctx, "tcp", u.Addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", u.Addr)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Hello("localhost"); err != nil {
		return err
	}
	if !u.ImplicitTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
				return err
			}
		}
	}
	if u.Username != "" || u.Password != "" {
		if err := c.Auth(smtp.PlainAuth("", u.Username, u.Password, host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
