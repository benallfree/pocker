package mailer

// CE: this whole file. A development mail catcher (like MailDev or Mailpit) built into PocketBase CE: in dev mode,
// an app whose SMTP is off sends to it instead of the host's sendmail (see core.BaseApp.NewMailClient).

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/security"
	"github.com/domodwyer/mailyak/v3"
)

// Where the dev mail trap listens. Set them before the first app bootstraps, or with the environment variables
// PB_MAILTRAP_HTTP and PB_MAILTRAP_SMTP.
var (
	DevTrapHTTPAddr = "127.0.0.1:8025"
	DevTrapSMTPAddr = "127.0.0.1:1025"
)

// InstanceHeader names the app that sent a message when several apps share a process (e.g. under pocker). The trap
// shows and filters by it.
const InstanceHeader = "X-Pocker-Instance"

var (
	devTrapOnce sync.Once
	devTrap     *Trap
	devTrapErr  error
)

// DevTrap returns the process-wide dev mail trap, starting it on first use and printing reason with the inbox
// address. It listens until the process exits. If its ports are taken it still catches mail in memory and prints each
// message to stderr; err says why the inbox isn't served.
func DevTrap(reason string) (t *Trap, err error) {
	devTrapOnce.Do(func() {
		devTrap = &Trap{SMTPAddr: envOr("PB_MAILTRAP_SMTP", DevTrapSMTPAddr), HTTPAddr: envOr("PB_MAILTRAP_HTTP", DevTrapHTTPAddr)}
		devTrapErr = devTrap.Start(context.Background())
		if devTrapErr != nil {
			fmt.Fprintf(os.Stderr, "Mail trap: %v; caught mail is printed here only.\n", devTrapErr)
			return
		}
		fmt.Fprintf(os.Stderr, "%s, so the dev mail trap is active: mail is caught, not delivered. Inbox http://%s (SMTP %s).\n", reason, devTrap.HTTPAddr, devTrap.SMTPAddr)
	})
	return devTrap, devTrapErr
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// Trap catches mail in memory: over SMTP (it accepts everything and delivers nothing) and in-process through
// [TrapClient]. Never expose it: anyone who can reach its ports can read the mail.
type Trap struct {
	// SMTPAddr and HTTPAddr are where it listens (defaults 127.0.0.1:1025 and 127.0.0.1:8025).
	SMTPAddr string
	HTTPAddr string

	// Max is how many messages are kept; the oldest go first (default 500).
	Max int

	mu     sync.Mutex
	msgs   []*TrapMessage
	nextID int
}

// TrapMessage is one caught message.
type TrapMessage struct {
	ID       int       `json:"id"`
	Received time.Time `json:"received"`
	// Envelope (MAIL FROM and RCPT TO, or the message's From and To + Cc + Bcc when sent in-process).
	MailFrom string   `json:"mailFrom"`
	RcptTo   []string `json:"rcptTo"`
	// From the headers.
	From     string `json:"from"`
	To       string `json:"to"`
	Cc       string `json:"cc,omitempty"`
	ReplyTo  string `json:"replyTo,omitempty"`
	Subject  string `json:"subject"`
	Instance string `json:"instance,omitempty"` // InstanceHeader, else the sending app's name
	Text     string `json:"text,omitempty"`
	HTML     string `json:"html,omitempty"`
	Size     int    `json:"size"`

	raw []byte
}

func (t *Trap) defaults() {
	if t.SMTPAddr == "" {
		t.SMTPAddr = "127.0.0.1:1025"
	}
	if t.HTTPAddr == "" {
		t.HTTPAddr = "127.0.0.1:8025"
	}
	if t.Max == 0 {
		t.Max = 500
	}
}

// Start listens on both addresses in the background until ctx is done. It returns once both are listening.
func (t *Trap) Start(ctx context.Context) error {
	t.defaults()
	sl, err := net.Listen("tcp", t.SMTPAddr)
	if err != nil {
		return fmt.Errorf("SMTP %s: %w", t.SMTPAddr, err)
	}
	hl, err := net.Listen("tcp", t.HTTPAddr)
	if err != nil {
		sl.Close()
		return fmt.Errorf("inbox %s: %w", t.HTTPAddr, err)
	}
	hs := &http.Server{Handler: t.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sl.Close()
		hs.Close()
	}()
	go func() {
		for {
			c, err := sl.Accept()
			if err != nil {
				return
			}
			go t.serveSMTP(c)
		}
	}()
	go func() {
		if err := hs.Serve(hl); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "Mail trap inbox: %v\n", err)
		}
	}()
	return nil
}

// Messages returns the caught messages, newest first.
func (t *Trap) Messages() []TrapMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]TrapMessage, 0, len(t.msgs))
	for i := len(t.msgs) - 1; i >= 0; i-- {
		out = append(out, *t.msgs[i])
	}
	return out
}

// Clear drops every message.
func (t *Trap) Clear() {
	t.mu.Lock()
	t.msgs = nil
	t.mu.Unlock()
}

// Add stores an encoded message (RFC 5322) with its envelope. label names the sender when the message has no
// [InstanceHeader].
func (t *Trap) Add(from string, to []string, raw []byte, label string) *TrapMessage {
	m := parseTrapped(raw)
	if m.Instance == "" {
		m.Instance = label
	}
	m.MailFrom, m.RcptTo, m.Received, m.Size, m.raw = from, to, time.Now(), len(raw), raw
	t.mu.Lock()
	defer t.mu.Unlock()
	t.defaults()
	t.nextID++
	m.ID = t.nextID
	t.msgs = append(t.msgs, m)
	if over := len(t.msgs) - t.Max; over > 0 {
		t.msgs = t.msgs[over:]
	}
	return m
}

func (t *Trap) get(id int) *TrapMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, m := range t.msgs {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// TrapClient is a [Mailer] that hands every message to a [Trap] in-process.
type TrapClient struct {
	Trap *Trap

	// Label names the sending app in the inbox when the message has no [InstanceHeader] (e.g. the app name).
	Label string

	onSend *hook.Hook[*SendEvent]
}

// OnSend implements [SendInterceptor], so OnMailerSend hooks run as with the other clients.
func (c *TrapClient) OnSend() *hook.Hook[*SendEvent] {
	if c.onSend == nil {
		c.onSend = &hook.Hook[*SendEvent]{}
	}
	return c.onSend
}

// Send implements [Mailer].
func (c *TrapClient) Send(m *Message) error {
	if c.onSend != nil {
		return c.onSend.Trigger(&SendEvent{Message: m}, func(e *SendEvent) error {
			return c.send(e.Message)
		})
	}
	return c.send(m)
}

func (c *TrapClient) send(m *Message) error {
	raw, err := encodeForTrap(m)
	if err != nil {
		return err
	}
	var rcpts []string
	for _, list := range [][]mail.Address{m.To, m.Cc, m.Bcc} {
		rcpts = append(rcpts, addressesToStrings(list, false)...)
	}
	tm := c.Trap.Add(m.From.Address, rcpts, raw, c.Label)
	who := ""
	if tm.Instance != "" {
		who = " [" + tm.Instance + "]"
	}
	fmt.Fprintf(os.Stderr, "Mail trap caught #%d%s from %s to %s: %q (http://%s)\n", tm.ID, who, m.From.Address, strings.Join(rcpts, ", "), m.Subject, c.Trap.HTTPAddr)
	return nil
}

// encodeForTrap builds the message the way [SMTPClient] does.
func encodeForTrap(m *Message) ([]byte, error) {
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
	} else if plain, err := html2Text(m.HTML); err == nil && plain != "" {
		yak.Plain().Set(plain)
	}
	if len(m.To) > 0 {
		yak.To(addressesToStrings(m.To, true)...)
	}
	if len(m.Cc) > 0 {
		yak.Cc(addressesToStrings(m.Cc, true)...)
	}
	for name, data := range m.Attachments {
		r, mt, err := detectReaderMimeType(data)
		if err != nil {
			return nil, err
		}
		yak.AttachWithMimeType(name, r, mt)
	}
	for name, data := range m.InlineAttachments {
		r, mt, err := detectReaderMimeType(data)
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

// serveSMTP speaks just enough SMTP for clients to hand over mail. AUTH is accepted with any credentials.
func (t *Trap) serveSMTP(c net.Conn) {
	defer c.Close()
	r := textproto.NewReader(bufio.NewReader(c))
	w := bufio.NewWriter(c)
	reply := func(s string) {
		_ = c.SetDeadline(time.Now().Add(5 * time.Minute))
		w.WriteString(s + "\r\n")
		w.Flush()
	}
	reply("220 trap ESMTP (development mail catcher, nothing is delivered)")

	var from string
	var to []string
	for {
		line, err := r.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO":
			reply("250-trap\r\n250-8BITMIME\r\n250-AUTH PLAIN LOGIN\r\n250 SIZE 26214400")
		case "HELO":
			reply("250 trap")
		case "AUTH":
			if strings.HasPrefix(strings.ToUpper(arg), "LOGIN") {
				reply("334 VXNlcm5hbWU6") // Username:
				if _, err := r.ReadLine(); err != nil {
					return
				}
				reply("334 UGFzc3dvcmQ6") // Password:
				if _, err := r.ReadLine(); err != nil {
					return
				}
			} else if !strings.Contains(arg, " ") { // PLAIN without the initial response
				reply("334 ")
				if _, err := r.ReadLine(); err != nil {
					return
				}
			}
			reply("235 2.7.0 accepted")
		case "MAIL":
			from, to = smtpAddrArg(arg), nil
			reply("250 2.1.0 ok")
		case "RCPT":
			to = append(to, smtpAddrArg(arg))
			reply("250 2.1.5 ok")
		case "DATA":
			if len(to) == 0 {
				reply("503 5.5.1 RCPT first")
				continue
			}
			reply("354 end with <CRLF>.<CRLF>")
			raw, err := r.ReadDotBytes()
			if err != nil {
				return
			}
			m := t.Add(from, to, raw, "")
			reply(fmt.Sprintf("250 2.0.0 ok: queued as %d", m.ID))
			from, to = "", nil
		case "RSET":
			from, to = "", nil
			reply("250 ok")
		case "NOOP":
			reply("250 ok")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("502 5.5.2 not implemented")
		}
	}
}

// smtpAddrArg pulls the address out of "FROM:<a@b> SIZE=1" or "TO:<a@b>".
func smtpAddrArg(arg string) string {
	if i := strings.IndexByte(arg, '<'); i >= 0 {
		if j := strings.IndexByte(arg[i:], '>'); j > 0 {
			return arg[i+1 : i+j]
		}
	}
	_, v, _ := strings.Cut(arg, ":")
	return strings.TrimSpace(v)
}

var trapWordDecoder = &mime.WordDecoder{}

func trapHeader(h mail.Header, k string) string {
	v := h.Get(k)
	if d, err := trapWordDecoder.DecodeHeader(v); err == nil {
		return d
	}
	return v
}

func parseTrapped(raw []byte) *TrapMessage {
	m := &TrapMessage{}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		m.Text = string(raw)
		return m
	}
	m.From = trapHeader(msg.Header, "From")
	m.To = trapHeader(msg.Header, "To")
	m.Cc = trapHeader(msg.Header, "Cc")
	m.ReplyTo = trapHeader(msg.Header, "Reply-To")
	m.Subject = trapHeader(msg.Header, "Subject")
	m.Instance = msg.Header.Get(InstanceHeader)
	walkTrapped(m, textproto.MIMEHeader(msg.Header), msg.Body, 0)
	return m
}

// walkTrapped collects the first text/plain and text/html parts, decoding the transfer encoding.
func walkTrapped(m *TrapMessage, h textproto.MIMEHeader, body io.Reader, depth int) {
	if depth > 10 {
		return
	}
	ct := h.Get("Content-Type")
	if ct == "" {
		ct = "text/plain"
	}
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return
	}
	if strings.HasPrefix(mt, "multipart/") {
		mr := multipart.NewReader(body, params["boundary"])
		for {
			p, err := mr.NextRawPart()
			if err != nil {
				return
			}
			walkTrapped(m, p.Header, p, depth+1)
		}
	}
	if !strings.HasPrefix(mt, "text/") || strings.HasPrefix(strings.ToLower(h.Get("Content-Disposition")), "attachment") {
		return
	}
	var r io.Reader = body
	switch strings.ToLower(h.Get("Content-Transfer-Encoding")) {
	case "quoted-printable":
		r = quotedprintable.NewReader(body)
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, newlineStripper{body})
	}
	b, _ := io.ReadAll(io.LimitReader(r, 4<<20))
	switch {
	case mt == "text/html" && m.HTML == "":
		m.HTML = string(b)
	case mt == "text/plain" && m.Text == "":
		m.Text = string(b)
	}
}

type newlineStripper struct{ r io.Reader }

func (n newlineStripper) Read(p []byte) (int, error) {
	k, err := n.r.Read(p)
	j := 0
	for _, c := range p[:k] {
		if c != '\r' && c != '\n' {
			p[j] = c
			j++
		}
	}
	return j, err
}

//go:embed trap_inbox.html
var trapInboxHTML []byte

// Handler is the web inbox: GET / (the page), GET /api/messages, GET /api/messages/{id},
// GET /api/messages/{id}/raw (.eml) and DELETE /api/messages.
func (t *Trap) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(trapInboxHTML)
	})
	mux.HandleFunc("GET /api/messages", func(w http.ResponseWriter, _ *http.Request) {
		list := t.Messages()
		for i := range list {
			list[i].Text, list[i].HTML = "", "" // the list stays small; bodies come from /api/messages/{id}
		}
		trapJSON(w, list)
	})
	mux.HandleFunc("DELETE /api/messages", func(w http.ResponseWriter, _ *http.Request) {
		t.Clear()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		if m := t.byPath(r); m != nil {
			trapJSON(w, m)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /api/messages/{id}/raw", func(w http.ResponseWriter, r *http.Request) {
		if m := t.byPath(r); m != nil {
			w.Header().Set("Content-Type", "message/rfc822")
			w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"message-%d.eml\"", m.ID))
			w.Write(m.raw)
			return
		}
		http.NotFound(w, r)
	})
	return mux
}

func (t *Trap) byPath(r *http.Request) *TrapMessage {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return nil
	}
	return t.get(id)
}

func trapJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
