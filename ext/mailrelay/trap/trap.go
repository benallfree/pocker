// Package trap is a development mail catcher (like MailDev or Mailpit) that runs inside your process: an SMTP
// server that accepts everything and delivers nothing, and a web inbox to read what arrived.
//
// Point [mailrelay.SMTPUpstream] at [Trap.SMTPAddr] and every Pocker instance's mail lands in one inbox, tagged with
// the instance that sent it. Never use it in production: it accepts mail from anyone who can reach the port.
package trap

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
	"strconv"
	"strings"
	"sync"
	"time"
)

// Trap holds the caught messages in memory.
type Trap struct {
	// SMTPAddr and HTTPAddr are where it listens (defaults 127.0.0.1:1025 and 127.0.0.1:8025).
	SMTPAddr string
	HTTPAddr string

	// Max is how many messages are kept; the oldest go first (default 500).
	Max int

	mu     sync.Mutex
	msgs   []*Message
	nextID int
}

// Message is one caught message.
type Message struct {
	ID       int       `json:"id"`
	Received time.Time `json:"received"`
	// Envelope (what the client said in MAIL FROM and RCPT TO).
	MailFrom string   `json:"mailFrom"`
	RcptTo   []string `json:"rcptTo"`
	// From the headers.
	From     string `json:"from"`
	To       string `json:"to"`
	Cc       string `json:"cc,omitempty"`
	ReplyTo  string `json:"replyTo,omitempty"`
	Subject  string `json:"subject"`
	Instance string `json:"instance,omitempty"` // X-Pocker-Instance
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

// Run listens on both addresses until ctx is done.
func (t *Trap) Run(ctx context.Context) error {
	t.defaults()
	sl, err := net.Listen("tcp", t.SMTPAddr)
	if err != nil {
		return fmt.Errorf("trap smtp: %w", err)
	}
	hl, err := net.Listen("tcp", t.HTTPAddr)
	if err != nil {
		sl.Close()
		return fmt.Errorf("trap http: %w", err)
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
	if err := hs.Serve(hl); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Messages returns the caught messages, newest first.
func (t *Trap) Messages() []Message {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Message, 0, len(t.msgs))
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

func (t *Trap) add(from string, to []string, raw []byte) *Message {
	m := parse(raw)
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

func (t *Trap) get(id int) *Message {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, m := range t.msgs {
		if m.ID == id {
			return m
		}
	}
	return nil
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
			from, to = addrArg(arg), nil
			reply("250 2.1.0 ok")
		case "RCPT":
			to = append(to, addrArg(arg))
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
			m := t.add(from, to, raw)
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

// addrArg pulls the address out of "FROM:<a@b> SIZE=1" or "TO:<a@b>".
func addrArg(arg string) string {
	if i := strings.IndexByte(arg, '<'); i >= 0 {
		if j := strings.IndexByte(arg[i:], '>'); j > 0 {
			return arg[i+1 : i+j]
		}
	}
	_, v, _ := strings.Cut(arg, ":")
	return strings.TrimSpace(v)
}

var wordDecoder = &mime.WordDecoder{}

func header(h mail.Header, k string) string {
	v := h.Get(k)
	if d, err := wordDecoder.DecodeHeader(v); err == nil {
		return d
	}
	return v
}

func parse(raw []byte) *Message {
	m := &Message{}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		m.Text = string(raw)
		return m
	}
	m.From = header(msg.Header, "From")
	m.To = header(msg.Header, "To")
	m.Cc = header(msg.Header, "Cc")
	m.ReplyTo = header(msg.Header, "Reply-To")
	m.Subject = header(msg.Header, "Subject")
	m.Instance = msg.Header.Get("X-Pocker-Instance")
	walk(m, textproto.MIMEHeader(msg.Header), msg.Body, 0)
	return m
}

// walk collects the first text/plain and text/html parts, decoding the transfer encoding.
func walk(m *Message, h textproto.MIMEHeader, body io.Reader, depth int) {
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
			walk(m, p.Header, p, depth+1)
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

//go:embed inbox.html
var inboxHTML []byte

// Handler is the web inbox: GET / (the page), GET /api/messages, GET /api/messages/{id},
// GET /api/messages/{id}/raw (.eml) and DELETE /api/messages.
func (t *Trap) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(inboxHTML)
	})
	mux.HandleFunc("GET /api/messages", func(w http.ResponseWriter, _ *http.Request) {
		list := t.Messages()
		for i := range list {
			list[i].Text, list[i].HTML = "", "" // the list stays small; bodies come from /api/messages/{id}
		}
		writeJSON(w, list)
	})
	mux.HandleFunc("DELETE /api/messages", func(w http.ResponseWriter, _ *http.Request) {
		t.Clear()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		if m := t.byPath(r); m != nil {
			writeJSON(w, m)
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

func (t *Trap) byPath(r *http.Request) *Message {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return nil
	}
	return t.get(id)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
