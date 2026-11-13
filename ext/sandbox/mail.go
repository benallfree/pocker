package sandbox

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/mailer"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/security"
	"github.com/domodwyer/mailyak/v3"
	"github.com/gabriel-vasile/mimetype"
)

// BindMail sends app's mail through the egress policy (see [InstallEgressPolicy]) whenever the app has its own SMTP
// on: the connection to the app's SMTP server is dialed by the policy's dialer, so it can't reach private addresses.
// With SMTP off, or with no policy installed, it does nothing and the next handler runs (a relay, the dev mail trap,
// or PocketBase's own sender).
//
// It's built on PocketBase's own OnMailerSend hook and needs no change to PocketBase: bound last, it does the SMTP
// exchange itself and never calls e.Next().
func BindMail(app core.App) {
	app.OnMailerSend().Bind(&hook.Handler[*core.MailerEvent]{
		Id:       "__sandboxMail__",
		Priority: 1 << 30, // last: the app's own onMailerSend hooks run (and may change the message) first
		Func: func(e *core.MailerEvent) error {
			dial := egressDialContext()
			cfg := e.App.Settings().SMTP
			if !cfg.Enabled || dial == nil {
				return e.Next()
			}
			// never e.Next() here: PocketBase's SMTP client would dial the server directly, around the policy
			return sendSMTP(dial, smtpServer{
				Host: cfg.Host, Port: cfg.Port, TLS: cfg.TLS, Username: cfg.Username, Password: cfg.Password,
				AuthMethod: cfg.AuthMethod, LocalName: cfg.LocalName,
			}, e.Message)
		},
	})
}

type smtpServer struct {
	Host, Username, Password, AuthMethod, LocalName string
	Port                                            int
	TLS                                             bool
}

// sendSMTP performs the exchange PocketBase's SMTP client does (implicit TLS, or STARTTLS when offered, then AUTH,
// MAIL, RCPT, DATA) over a connection from dial.
func sendSMTP(dial func(ctx context.Context, network, address string) (net.Conn, error), c smtpServer, m *mailer.Message) error {
	raw, err := encodeMessage(m)
	if err != nil {
		return err
	}
	var auth smtp.Auth
	if c.Username != "" || c.Password != "" {
		if c.AuthMethod == mailer.SMTPAuthLogin {
			auth = &loginAuth{c.Username, c.Password}
		} else {
			auth = smtp.PlainAuth("", c.Username, c.Password, c.Host)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", net.JoinHostPort(c.Host, strconv.Itoa(c.Port)))
	if err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if c.TLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: c.Host})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return err
		}
		conn = tlsConn
	}
	client, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _ = client.Quit() }()
	if c.LocalName != "" {
		if err := client.Hello(c.LocalName); err != nil {
			return err
		}
	}
	if !c.TLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: c.Host}); err != nil {
				return err
			}
		}
	}
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(m.From.Address); err != nil {
		return err
	}
	for _, list := range [][]mail.Address{m.To, m.Cc, m.Bcc} {
		for _, a := range list {
			if err := client.Rcpt(a.Address); err != nil {
				return err
			}
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

// encodeMessage builds the message the way PocketBase's SMTP client does (Bcc stays out of the headers).
func encodeMessage(m *mailer.Message) ([]byte, error) {
	yak := mailyak.New("", nil)
	if m.From.Name != "" {
		yak.FromName(m.From.Name)
	}
	yak.From(m.From.Address)
	yak.Subject(m.Subject)
	if m.HTML != "" {
		yak.HTML().Set(m.HTML)
	}
	if m.Text != "" {
		yak.Plain().Set(m.Text)
	} else if plain := htmlToText(m.HTML); plain != "" {
		yak.Plain().Set(plain)
	}
	if len(m.To) > 0 {
		yak.To(addressList(m.To)...)
	}
	if len(m.Cc) > 0 {
		yak.Cc(addressList(m.Cc)...)
	}
	for name, data := range m.Attachments {
		r, mt, err := detectType(data)
		if err != nil {
			return nil, err
		}
		yak.AttachWithMimeType(name, r, mt)
	}
	for name, data := range m.InlineAttachments {
		r, mt, err := detectType(data)
		if err != nil {
			return nil, err
		}
		yak.AttachInlineWithMimeType(name, r, mt)
	}
	hasID := false
	for k, v := range m.Headers {
		if strings.EqualFold(k, "Message-ID") {
			hasID = true
		}
		yak.AddHeader(k, v)
	}
	if !hasID {
		if at := strings.LastIndexByte(m.From.Address, '@'); at >= 0 {
			yak.AddHeader("Message-ID", fmt.Sprintf("<%s@%s>", security.PseudorandomString(15), m.From.Address[at+1:]))
		}
	}
	buf, err := yak.MimeBuf()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func addressList(list []mail.Address) []string {
	out := make([]string, 0, len(list))
	for _, a := range list {
		if a.Address == "" {
			continue
		}
		if a.Name != "" {
			out = append(out, a.String())
		} else {
			out = append(out, a.Address)
		}
	}
	return out
}

var (
	breakRe = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</h[1-6]>|</li>`)
	tagRe   = regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>|<[^>]+>`)
	blankRe = regexp.MustCompile(`[ \t]*\n[ \t\n]*`)
)

// htmlToText is a plain-text part for HTML-only mail: tags dropped, entities decoded, blank runs collapsed.
func htmlToText(s string) string {
	s = html.UnescapeString(tagRe.ReplaceAllString(breakRe.ReplaceAllString(s, "\n"), ""))
	return strings.TrimSpace(blankRe.ReplaceAllString(s, "\n"))
}

func detectType(r io.Reader) (io.Reader, string, error) {
	var head bytes.Buffer
	mt, err := mimetype.DetectReader(io.TeeReader(r, &head))
	if err != nil {
		return nil, "", err
	}
	return io.MultiReader(&head, r), mt.String(), nil
}

// loginAuth is SMTP AUTH LOGIN (net/smtp only has PLAIN and CRAM-MD5).
type loginAuth struct{ username, password string }

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		// like PlainAuth: never send the password unencrypted, except to localhost
		host := server.Name
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			return "", nil, errors.New("smtp: refusing LOGIN auth over an unencrypted connection")
		}
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSuffix(string(fromServer), ":")) {
	case "username":
		return []byte(a.username), nil
	case "password":
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("smtp: unexpected LOGIN prompt %q", fromServer)
}
