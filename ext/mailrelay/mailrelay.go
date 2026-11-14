// Package mailrelay is outgoing mail for Pocker instances that haven't set up SMTP.
//
// Stock PocketBase falls back to the host's `sendmail` when SMTP is off. Under Pocker that would run sendmail on the
// shared host for every tenant. With this plugin, an instance's mail goes one of two ways:
//
//   - SMTP on in its settings: PocketBase sends it as usual (bring your own SMTP always wins);
//   - SMTP off: the plugin sends it through the operator's relay (an [Upstream]) as <local part>@<Domain>, within
//     per-instance limits. sendmail never runs.
//
// Pocker only: a single PocketBase app can simply configure its own SMTP.
package mailrelay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/pockethost/pocketone/ext/ceplugin"
	"html"
	"net/mail"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/mailer"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/security"
	"github.com/domodwyer/mailyak/v3"
	"github.com/gabriel-vasile/mimetype"
	"github.com/pockethost/pocketone/ext/pocker"
)

// Name is the plugin's key in a pocker config file and under an instance's `plugins:`.
const Name = "mailrelay"

// Defaults for [Config] fields left at zero.
const (
	DefaultPerMinute     = 20
	DefaultDaily         = 200
	DefaultMaxRecipients = 5
	DefaultMaxSize       = 1 << 20 // bytes of the encoded message
)

// Upstream delivers one encoded message (RFC 5322, headers and body) to the given envelope recipients. It is the
// operator's side: an SMTP server ([SMTPUpstream]), or an API such as Amazon SES SendRawEmail.
type Upstream interface {
	Send(ctx context.Context, from string, to []string, raw []byte) error
}

// Config is the operator's relay. Domain and Upstream are set in Go; limits can also come from the config file's
// top-level `mailrelay:` key and per instance from `plugins: {mailrelay: {enabled, per_minute, daily}}`.
type Config struct {
	// Domain is the sending domain: every relayed message is From <local part>@Domain. Required.
	Domain string

	// Upstream delivers the messages. Required.
	Upstream Upstream

	// PerMinute and Daily cap messages per instance (defaults 20 and 200). A message counts once, whatever its
	// number of recipients. Daily resets at midnight UTC.
	PerMinute int
	Daily     int

	// MaxRecipients caps To + Cc + Bcc per message (default 5). MaxSize caps the encoded message (default 1 MiB).
	MaxRecipients int
	MaxSize       int

	// LocalPart picks the address's local part for an instance (default: the first label of its id, lowercased,
	// anything but a-z, 0-9 and "-" dropped).
	LocalPart func(inst pocker.Instance) string

	// Now is the clock (tests).
	Now func() time.Time
}

func (c Config) withDefaults() Config {
	if c.PerMinute == 0 {
		c.PerMinute = DefaultPerMinute
	}
	if c.Daily == 0 {
		c.Daily = DefaultDaily
	}
	if c.MaxRecipients == 0 {
		c.MaxRecipients = DefaultMaxRecipients
	}
	if c.MaxSize == 0 {
		c.MaxSize = DefaultMaxSize
	}
	if c.LocalPart == nil {
		c.LocalPart = defaultLocalPart
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

var nonLocal = regexp.MustCompile(`[^a-z0-9-]+`)

func defaultLocalPart(inst pocker.Instance) string {
	id := strings.ToLower(inst.ID)
	if i := strings.IndexByte(id, '.'); i > 0 {
		id = id[:i] // dev ids are hostnames: "blog.localhost" → "blog"
	}
	id = strings.Trim(nonLocal.ReplaceAllString(id, ""), "-")
	if id == "" {
		return "instance"
	}
	return id
}

// Errors returned to PocketBase (and so to whoever triggered the mail) when a message isn't relayed.
var (
	ErrDisabled      = errors.New("mailrelay: outgoing mail is off for this instance; configure SMTP in Settings → Mail")
	ErrRateLimited   = errors.New("mailrelay: mail limit reached for this instance, try again later")
	ErrTooManyRcpts  = errors.New("mailrelay: too many recipients in one message")
	ErrTooLarge      = errors.New("mailrelay: message too large")
	ErrNoRecipients  = errors.New("mailrelay: message has no recipients")
	errNotConfigured = errors.New("mailrelay: Config.Domain and Config.Upstream are required")
)

// Plugin is the pocker plugin. Include it with
//
//	srv.Plugins = append(srv.Plugins, mailrelay.New(mailrelay.Config{Domain: "mail.example.com", Upstream: up}))
type Plugin struct {
	cfg     Config
	enabled *bool

	mu    sync.Mutex
	usage map[string]*usage // by instance id

	mgr atomic.Pointer[pocker.Manager]
}

var (
	_ pocker.Plugin     = (*Plugin)(nil)
	_ pocker.Configurer = (*Plugin)(nil)
	_ pocker.AppIniter  = (*Plugin)(nil)
	_ pocker.Starter    = (*Plugin)(nil)
)

// New returns the plugin. cfg holds the relay and the defaults for every instance.
func New(cfg Config) *Plugin {
	return &Plugin{cfg: cfg.withDefaults(), usage: map[string]*usage{}}
}

// Name implements [pocker.Plugin].
func (p *Plugin) Name() string { return Name }

// SectionConfig is the shape of the `mailrelay` section and of an instance's `plugins.mailrelay`.
type SectionConfig struct {
	Enabled   *bool `yaml:"enabled"`
	PerMinute *int  `yaml:"per_minute"`
	Daily     *int  `yaml:"daily"`
}

// limits are the effective settings for one instance.
type limits struct {
	enabled   bool
	perMinute int
	daily     int
}

func (y SectionConfig) apply(l *limits) {
	if y.Enabled != nil {
		l.enabled = *y.Enabled
	}
	if y.PerMinute != nil && *y.PerMinute > 0 {
		l.perMinute = *y.PerMinute
	}
	if y.Daily != nil && *y.Daily > 0 {
		l.daily = *y.Daily
	}
}

// Configure implements [pocker.Configurer]: pocketone.yaml's `mailrelay` section. Limits set in Go win.
func (p *Plugin) Configure(pocker.Config) error {
	if p.cfg.Domain == "" || p.cfg.Upstream == nil {
		return errNotConfigured
	}
	y, found := Section.Get()
	if !found {
		return nil
	}
	p.enabled = y.Enabled
	if y.PerMinute != nil && *y.PerMinute > 0 && p.cfg.PerMinute == DefaultPerMinute {
		p.cfg.PerMinute = *y.PerMinute
	}
	if y.Daily != nil && *y.Daily > 0 && p.cfg.Daily == DefaultDaily {
		p.cfg.Daily = *y.Daily
	}
	return nil
}

// Start implements [pocker.Starter]: keeps the manager so relayed mail shows up in the instance's own log.
func (p *Plugin) Start(_ context.Context, m *pocker.Manager) { p.mgr.Store(m) }

// limitsFor reads the instance's settings each time, so changes from the instance source apply without a restart.
func (p *Plugin) limitsFor(inst pocker.Instance) (limits, error) {
	l := limits{enabled: p.enabled == nil || *p.enabled, perMinute: p.cfg.PerMinute, daily: p.cfg.Daily}
	if raw := inst.PluginConfig(Name); raw != nil {
		var y SectionConfig
		if err := pocker.DecodePluginConfig(raw, &y); err != nil {
			return l, fmt.Errorf("mailrelay: instance %s: %w", inst.ID, err)
		}
		y.apply(&l)
	}
	return l, nil
}

// Address is the From address an instance sends as.
func (p *Plugin) Address(inst pocker.Instance) string {
	return p.cfg.LocalPart(inst) + "@" + p.cfg.Domain
}

// InitApp implements [pocker.AppIniter]: takes over the instance's mail whenever its SMTP is off.
func (p *Plugin) InitApp(inst pocker.Instance, app core.App) error {
	if p.cfg.Domain == "" || p.cfg.Upstream == nil {
		return errNotConfigured
	}
	if _, err := p.limitsFor(inst); err != nil {
		return err
	}
	app.OnMailerSend().Bind(&hook.Handler[*core.MailerEvent]{
		Id:       "__mailrelay__",
		Priority: 1 << 30, // last: the app's own onMailerSend hooks run (and may change the message) first
		Func: func(e *core.MailerEvent) error {
			if e.App.Settings().SMTP.Enabled {
				return e.Next() // bring your own SMTP wins
			}
			// never e.Next() here: that would run sendmail on the host
			return p.relay(inst, e.App, e.Message)
		},
	})
	p.bindRoutes(inst, app)
	return nil
}

func (p *Plugin) relay(inst pocker.Instance, app core.App, m *mailer.Message) error {
	l, err := p.limitsFor(inst)
	if err != nil {
		return err
	}
	if !l.enabled {
		p.logInstance(inst, "mail refused: outgoing mail is off for this instance")
		return ErrDisabled
	}

	rcpts := envelopeRecipients(m)
	switch {
	case len(rcpts) == 0:
		return ErrNoRecipients
	case len(rcpts) > p.cfg.MaxRecipients:
		p.logInstance(inst, fmt.Sprintf("mail refused: %d recipients (max %d)", len(rcpts), p.cfg.MaxRecipients))
		return ErrTooManyRcpts
	}

	from := mail.Address{Name: displayName(m.From.Name, app.Settings().Meta.SenderName), Address: p.Address(inst)}
	raw, err := encode(m, from, replyTo(m, from.Address), inst.ID)
	if err != nil {
		return fmt.Errorf("mailrelay: %w", err)
	}
	if len(raw) > p.cfg.MaxSize {
		p.logInstance(inst, fmt.Sprintf("mail refused: %d bytes (max %d)", len(raw), p.cfg.MaxSize))
		return ErrTooLarge
	}

	if !p.take(inst.ID, l) {
		p.logInstance(inst, fmt.Sprintf("mail refused: limit reached (%d/minute, %d/day)", l.perMinute, l.daily))
		return ErrRateLimited
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.cfg.Upstream.Send(ctx, from.Address, rcpts, raw); err != nil {
		p.logInstance(inst, fmt.Sprintf("mail to %s failed: %v", maskList(rcpts), err))
		return fmt.Errorf("mailrelay: %w", err)
	}
	p.logInstance(inst, fmt.Sprintf("mail from %s to %s accepted", from.Address, maskList(rcpts)))
	app.Logger().Info("mailrelay: sent", "from", from.Address, "recipients", len(rcpts), "subject", m.Subject)
	return nil
}

func (p *Plugin) logInstance(inst pocker.Instance, msg string) {
	if m := p.mgr.Load(); m != nil {
		m.LogInstance(inst.ID, "mailrelay", msg)
	}
}

// usage counts one instance's messages in the current minute and UTC day.
type usage struct {
	minute    time.Time
	perMinute int
	day       time.Time
	daily     int
}

func (p *Plugin) take(id string, l limits) bool {
	now := p.cfg.Now().UTC()
	minute, day := now.Truncate(time.Minute), now.Truncate(24*time.Hour)

	p.mu.Lock()
	defer p.mu.Unlock()
	u := p.usage[id]
	if u == nil {
		u = &usage{}
		p.usage[id] = u
	}
	if !u.minute.Equal(minute) {
		u.minute, u.perMinute = minute, 0
	}
	if !u.day.Equal(day) {
		u.day, u.daily = day, 0
	}
	if u.perMinute >= l.perMinute || u.daily >= l.daily {
		return false
	}
	u.perMinute++
	u.daily++
	return true
}

// sentToday is the instance's count for the current UTC day.
func (p *Plugin) sentToday(id string) int {
	day := p.cfg.Now().UTC().Truncate(24 * time.Hour)
	p.mu.Lock()
	defer p.mu.Unlock()
	if u := p.usage[id]; u != nil && u.day.Equal(day) {
		return u.daily
	}
	return 0
}

func envelopeRecipients(m *mailer.Message) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]mail.Address{m.To, m.Cc, m.Bcc} {
		for _, a := range list {
			addr := strings.TrimSpace(a.Address)
			if addr == "" || seen[strings.ToLower(addr)] {
				continue
			}
			seen[strings.ToLower(addr)] = true
			out = append(out, addr)
		}
	}
	return out
}

var (
	dropBlocks = regexp.MustCompile(`(?is)<(head|style|script)[^>]*>.*?</(head|style|script)>`)
	breaks     = regexp.MustCompile(`(?i)<(br|/p|/div|/h[1-6]|/li|/tr)[^>]*>`)
	tags       = regexp.MustCompile(`(?s)<[^>]*>`)
	blankLines = regexp.MustCompile(`\n[ \t]*\n([ \t]*\n)+`)
)

// htmlToText is a plain-text version of an HTML body: blocks become lines, tags go, entities are decoded.
func htmlToText(h string) string {
	h = dropBlocks.ReplaceAllString(h, "")
	h = breaks.ReplaceAllString(h, "\n")
	h = html.UnescapeString(tags.ReplaceAllString(h, ""))
	lines := strings.Split(h, "\n")
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), " ")
	}
	return strings.TrimSpace(blankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

var unsafeName = regexp.MustCompile(`[@<>"\r\n]|https?://|www\.`)

// displayName keeps the app's sender name, minus anything that could pass for an address or a link.
func displayName(name, fallback string) string {
	if strings.TrimSpace(name) == "" {
		name = fallback
	}
	name = strings.TrimSpace(unsafeName.ReplaceAllString(name, ""))
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// replyTo sends replies to the app's own sender address, unless it is ours or PocketBase's placeholder.
func replyTo(m *mailer.Message, ours string) string {
	a := strings.TrimSpace(m.From.Address)
	switch {
	case a == "", strings.EqualFold(a, ours), strings.HasSuffix(strings.ToLower(a), "@example.com"):
		return ""
	}
	if _, err := mail.ParseAddress(a); err != nil {
		return ""
	}
	return a
}

// encode builds the message as PocketBase's SMTP client would, with our From, Reply-To and an instance header.
func encode(m *mailer.Message, from mail.Address, reply, instanceID string) ([]byte, error) {
	yak := mailyak.New("", nil)
	if from.Name != "" {
		yak.FromName(from.Name)
	}
	yak.From(from.Address)
	yak.Subject(m.Subject)
	if reply != "" {
		yak.ReplyTo(reply)
	}
	if m.HTML != "" {
		yak.HTML().Set(m.HTML)
	}
	if text := m.Text; text != "" {
		yak.Plain().Set(text)
	} else if m.HTML != "" {
		yak.Plain().Set(htmlToText(m.HTML)) // like PocketBase's SMTP client: always a plain part
	}
	yak.To(addrs(m.To)...)
	yak.Cc(addrs(m.Cc)...) // Bcc stays out of the headers; it is in the envelope only

	for name, r := range m.Attachments {
		data, mime, err := sniff(r)
		if err != nil {
			return nil, err
		}
		yak.AttachWithMimeType(name, data, mime)
	}
	for name, r := range m.InlineAttachments {
		data, mime, err := sniff(r)
		if err != nil {
			return nil, err
		}
		yak.AttachInlineWithMimeType(name, data, mime)
	}

	hasID := false
	for k, v := range m.Headers {
		switch strings.ToLower(k) {
		case "from", "sender", "return-path", "reply-to", "bcc", strings.ToLower(pocker.InstanceHeader):
			continue // ours (set below), or envelope-only
		case "message-id":
			hasID = true
		}
		yak.AddHeader(k, v)
	}
	if !hasID {
		yak.AddHeader("Message-ID", fmt.Sprintf("<%s@%s>", security.PseudorandomString(15), from.Address[strings.LastIndexByte(from.Address, '@')+1:]))
	}
	yak.AddHeader(pocker.InstanceHeader, instanceID)

	buf, err := yak.MimeBuf()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func addrs(list []mail.Address) []string {
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.String())
	}
	return out
}

func sniff(r interface{ Read([]byte) (int, error) }) (*bytes.Reader, string, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, "", err
	}
	return bytes.NewReader(buf.Bytes()), mimetype.Detect(buf.Bytes()).String(), nil
}

// mask hides most of an address's local part for logs: "alice@x.com" → "a***@x.com".
func mask(addr string) string {
	at := strings.LastIndexByte(addr, '@')
	if at <= 0 {
		return "***"
	}
	return addr[:1] + "***" + addr[at:]
}

func maskList(list []string) string {
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = mask(a)
	}
	return strings.Join(out, ", ")
}

// Section is the key of the `mailrelay` section (pocketone.yaml): the shared mail relay on a pocker host.
var Section = ceplugin.NewSection(Name, SectionConfig{}, "Shared mail relay on a pocker host: enabled, per-minute and daily limits per instance.")
