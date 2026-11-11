// Package bulkmail adds bulk mail campaigns to a PocketBase app: audiences from the app's own collections, a paced
// and restart-safe sender, a per-campaign ledger, signed one-click unsubscribe, bounce and complaint handling, and
// dashboard screens. It's a native Go plugin; JS hooks can drive it through [BindJS].
//
//	app := pocketbase.New()
//	bulkmail.MustRegister(app, bulkmail.Config{})
package bulkmail

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	validation "ganigeorgiev.com/pocketbase/ozzo-validation/v4"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/mailer"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
)

// NoBulkMail as [Config.Daily] means the app may not send bulk mail (the screens say so instead of offering Send).
const NoBulkMail = -1

const (
	defaultRate  = 5.0
	defaultDaily = 5000
	storeKey     = "bulkmail"
	paramKey     = "bulkmail"
)

// Config is set by whoever builds the binary (or, under pocker, per instance).
type Config struct {
	// Rate caps messages per second for this app (default 5). A campaign's own rate_per_sec can only lower it.
	Rate float64

	// Daily caps messages per UTC day for this app across all campaigns (default 5000). [NoBulkMail] forbids sending.
	Daily int

	// PublicURL is the base for unsubscribe links (default: the app's Settings → Application URL).
	PublicURL string

	// Mailer, when set, supplies the mail client instead of app.NewMailClient(), for example a platform relay.
	Mailer func(app core.App) mailer.Mailer

	// HTTPClient is used to fetch SNS signing certificates and confirm SNS subscriptions (default: 10 s timeout).
	HTTPClient *http.Client

	// Now is the clock (tests).
	Now func() time.Time
}

// Settings are stored per app (a _params row, so they ride along in backups) and edited in Settings → Bulk mail.
type Settings struct {
	// The default sender is the app's (Settings > Mail); a campaign's from_name / from_address override it.
	PostalAddress string `json:"postalAddress"`
	// UnsubscribeMailto, when set, is added to List-Unsubscribe as a mailto: alternative.
	UnsubscribeMailto string `json:"unsubscribeMailto"`
	// SNSTopicARNs, when not empty, limits the SES feedback endpoint to these topics.
	SNSTopicARNs []string `json:"snsTopicArns"`
	// Secret signs unsubscribe links. Generated on first use; never returned by the API.
	Secret string `json:"secret"`
	// WebhookSecret enables the generic feedback webhook. Never returned by the API.
	WebhookSecret string `json:"webhookSecret"`
}

// BulkMail is the bulk mail service of one app. Get it with [For].
type BulkMail struct {
	app core.App
	cfg Config

	// OnSend fires before every campaign message (not test sends). Change e.Message, or return an error to fail
	// that recipient without sending.
	OnSend *hook.Hook[*SendEvent]
	// OnUnsubscribe fires when someone unsubscribes through a signed link or one-click header.
	OnUnsubscribe *hook.Hook[*SuppressEvent]
	// OnFeedback fires for each bounce or complaint reported by a provider.
	OnFeedback *hook.Hook[*SuppressEvent]

	mu       sync.Mutex
	settings *Settings
	vars     map[string]map[string]any // campaign id -> resolved vars
	wake     chan struct{}
	stop     chan struct{}
	stopped  chan struct{}
	certs    sync.Map // SNS signing cert URL -> *x509.Certificate
	fetchPEM func(url string) ([]byte, error)
}

// SendEvent is passed to [BulkMail.OnSend].
type SendEvent struct {
	hook.Event
	App      core.App
	Campaign *core.Record
	Send     *core.Record
	Message  *mailer.Message
}

// SuppressEvent is passed to [BulkMail.OnUnsubscribe] and [BulkMail.OnFeedback].
type SuppressEvent struct {
	hook.Event
	App         core.App
	Email       string
	Reason      string // unsubscribed, bounced, complained
	Source      string // link, one-click, ses, webhook, js
	CampaignId  string
	Suppression *core.Record
}

//go:embed ui
var uiFiles embed.FS

// MustRegister is [Register] that panics on error.
func MustRegister(app core.App, cfg Config) *BulkMail {
	bm, err := Register(app, cfg)
	if err != nil {
		panic(err)
	}
	return bm
}

// Register adds bulk mail to app: its collections (on bootstrap), routes and dashboard screens (on serve), and the
// background sender (on serve, stopped on terminate). Call it before app.Start(), or from pocker's SetupApp.
func Register(app core.App, cfg Config) (*BulkMail, error) {
	if cfg.Rate <= 0 {
		cfg.Rate = defaultRate
	}
	if cfg.Daily == 0 {
		cfg.Daily = defaultDaily
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}

	bm := &BulkMail{
		app:           app,
		cfg:           cfg,
		OnSend:        &hook.Hook[*SendEvent]{},
		OnUnsubscribe: &hook.Hook[*SuppressEvent]{},
		OnFeedback:    &hook.Hook[*SuppressEvent]{},
		vars:          map[string]map[string]any{},
		wake:          make(chan struct{}, 1),
	}
	bm.fetchPEM = bm.httpGetPEM
	app.Store().Set(storeKey, bm)

	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		return EnsureSchema(e.App)
	})

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		bm.bindRoutes(se)
		ui, err := fs.Sub(uiFiles, "ui")
		if err != nil {
			return err
		}
		se.UIExtensions = append(se.UIExtensions, core.UIExtension{Name: "bulkmail", FS: ui})
		bm.StartSender()
		return se.Next()
	})

	// a campaign changed (dashboard, records API, JS): let an idle sender look again now
	app.OnRecordAfterUpdateSuccess(CampaignsCollection).BindFunc(func(e *core.RecordEvent) error {
		bm.poke()
		return e.Next()
	})

	// suppressions added or edited anywhere (dashboard record form, records API, JS) store the normalized address,
	// the same one IsSuppressed looks up; a manual entry without a reason is "manual"
	app.OnRecordValidate(SuppressionsCollection).BindFunc(func(e *core.RecordEvent) error {
		email := NormalizeEmail(e.Record.GetString("email"))
		if email == "" {
			return validation.Errors{"email": validation.NewError("validation_invalid_email", "Must be a valid email address.")}
		}
		e.Record.Set("email", email)
		if e.Record.GetString("reason") == "" {
			e.Record.Set("reason", ReasonManual)
		}
		return e.Next()
	})

	app.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
		bm.StopSender()
		return e.Next()
	})

	return bm, nil
}

// For returns the bulk mail service registered on app, or nil.
func For(app core.App) *BulkMail {
	bm, _ := app.Store().Get(storeKey).(*BulkMail)
	return bm
}

// App returns the app this service belongs to.
func (bm *BulkMail) App() core.App { return bm.app }

// Limits reports this app's caps and how many messages it sent today (UTC).
func (bm *BulkMail) Limits() (rate float64, daily int, sentToday int, err error) {
	n, err := bm.sentToday()
	return bm.cfg.Rate, bm.cfg.Daily, n, err
}

// Settings returns a copy of the stored settings, generating the signing secret on first use.
func (bm *BulkMail) Settings() (Settings, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if bm.settings != nil {
		return *bm.settings, nil
	}

	s := &Settings{}
	param := &core.Param{}
	err := bm.app.ModelQuery(param).Model(paramKey, param)
	if err == nil && len(param.Value) > 0 {
		if err := json.Unmarshal(param.Value, s); err != nil {
			return Settings{}, fmt.Errorf("bulkmail: settings: %w", err)
		}
	}
	if s.Secret == "" {
		s.Secret = randomHex(32)
		if err := bm.saveLocked(s); err != nil {
			return Settings{}, err
		}
	}
	bm.settings = s
	return *s, nil
}

// SaveSettings stores s. Empty Secret or WebhookSecret keep the stored values (the API never returns them).
func (bm *BulkMail) SaveSettings(s Settings) error {
	cur, err := bm.Settings()
	if err != nil {
		return err
	}
	if s.Secret == "" {
		s.Secret = cur.Secret
	}
	if s.WebhookSecret == "" {
		s.WebhookSecret = cur.WebhookSecret
	}

	bm.mu.Lock()
	defer bm.mu.Unlock()
	if err := bm.saveLocked(&s); err != nil {
		return err
	}
	bm.settings = &s
	return nil
}

func (bm *BulkMail) saveLocked(s *Settings) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	param := &core.Param{}
	if err := bm.app.ModelQuery(param).Model(paramKey, param); err != nil {
		param = &core.Param{}
		param.Id = paramKey
		param.MarkAsNew()
	}
	param.Value = types.JSONRaw(raw)
	now := types.NowDateTime()
	if param.IsNew() {
		param.Created = now
	}
	param.Updated = now
	return bm.app.Save(param)
}

func (bm *BulkMail) publicURL() string {
	u := bm.cfg.PublicURL
	if u == "" {
		u = bm.app.Settings().Meta.AppURL
	}
	return strings.TrimRight(u, "/")
}

func (bm *BulkMail) mailClient() mailer.Mailer {
	if bm.cfg.Mailer != nil {
		return bm.cfg.Mailer(bm.app)
	}
	return bm.app.NewMailClient()
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

var errNotAllowed = errors.New("bulk mail isn't allowed for this app")
