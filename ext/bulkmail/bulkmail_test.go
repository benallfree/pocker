package bulkmail

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/mailer"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
	"github.com/dop251/goja"
	"github.com/pockethost/pocketbase/ext/pocker"
	"gopkg.in/yaml.v3"
)

const superuserToken = "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6InN5d2JoZWNuaDQ2cmhtMCIsInR5cGUiOiJhdXRoIiwiY29sbGVjdGlvbklkIjoicGJjXzMxNDI2MzU4MjMiLCJleHAiOjI1MjQ2MDQ0NjEsInJlZnJlc2hhYmxlIjp0cnVlfQ.UXgO3j-0BumcugrFjbd7j0M4MQvbrLggLlcu_YNGjoY"

func newApp(t *testing.T, cfg Config) (*tests.TestApp, *BulkMail) {
	t.Helper()
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Cleanup)
	app.Settings().Meta.AppURL = "https://app.test"
	app.Settings().Meta.SenderAddress = "news@app.test"
	bm, err := Register(app, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(app); err != nil { // the test app is already bootstrapped
		t.Fatal(err)
	}
	return app, bm
}

func newCampaign(t *testing.T, app core.App, fields map[string]any) *core.Record {
	t.Helper()
	col, err := app.FindCollectionByNameOrId(CampaignsCollection)
	if err != nil {
		t.Fatal(err)
	}
	c := core.NewRecord(col)
	c.Set("name", "Test")
	c.Set("status", StatusDraft)
	c.Set("subject", "Hi {{record.name}}")
	c.Set("html", "<p>Hello {{record.name}}{{if record.verified}}, verified{{end}}. {{greeting}}</p>")
	c.Set("audience", map[string]any{"collection": "users", "filter": "verified = true"})
	c.Set("vars", map[string]any{"greeting": "Cheers"})
	for k, v := range fields {
		c.Set(k, v)
	}
	if err := app.Save(c); err != nil {
		t.Fatal(err)
	}
	return c
}

// drain runs the sender until it has nothing left to do right now.
func drain(t *testing.T, bm *BulkMail) {
	t.Helper()
	for i := 0; i < 1000; i++ {
		wait, err := bm.Step()
		if err != nil {
			t.Fatal(err)
		}
		if wait >= idleTick {
			return
		}
	}
	t.Fatal("sender didn't settle")
}

func verifiedUsers(t *testing.T, app core.App) []*core.Record {
	recs, err := app.FindRecordsByFilter("users", "verified = true", "id", 0, 0)
	if err != nil || len(recs) < 2 {
		t.Fatalf("test data needs at least 2 verified users: %v (%d)", err, len(recs))
	}
	return recs
}

func TestSchemaIsIdempotentAndAcceptsLegacyCreation(t *testing.T) {
	app, _ := newApp(t, Config{})

	if err := EnsureSchema(app); err != nil {
		t.Fatalf("second run: %v", err)
	}

	// what the legacy mothership's JS migration does: create the collections from their JSON
	for _, c := range []string{SendsId, SuppressionsId, CampaignsId} {
		col, _ := app.FindCollectionByNameOrId(c)
		col.System = false // system collections can't be deleted
		if err := app.SaveNoValidate(col); err != nil {
			t.Fatal(err)
		}
		if err := app.Delete(col); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(Collections())
	var defs []map[string]any
	_ = json.Unmarshal(raw, &defs)
	for _, d := range defs {
		b, _ := json.Marshal(d)
		col := &core.Collection{}
		if err := json.Unmarshal(b, col); err != nil {
			t.Fatal(err)
		}
		if err := app.Save(col); err != nil {
			t.Fatalf("create %s from JSON: %v", d["name"], err)
		}
	}
	if err := EnsureSchema(app); err != nil {
		t.Fatalf("after legacy-style creation: %v", err)
	}

	// a collection with the right name but another id is refused, not silently duplicated
	col, _ := app.FindCollectionByNameOrId(SuppressionsId)
	col.System = false
	_ = app.SaveNoValidate(col)
	_ = app.Delete(col)
	other := core.NewBaseCollection(SuppressionsCollection, "pbc_other_id")
	if err := app.Save(other); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(app); err == nil || !strings.Contains(err.Error(), "pbc_other_id") {
		t.Fatalf("expected an id mismatch error, got %v", err)
	}
}

func TestLegacyJSMigrationCompiles(t *testing.T) {
	js, err := LegacyJSMigration()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := goja.Compile("m.js", js, false); err != nil {
		t.Fatalf("invalid JS: %v", err)
	}
	for _, id := range []string{CampaignsId, SendsId, SuppressionsId, "bms_campaign", "idx_bulkmail_sends_campaign_email"} {
		if !strings.Contains(js, id) {
			t.Errorf("migration lacks %s", id)
		}
	}
}

func TestTemplates(t *testing.T) {
	data := map[string]any{
		"record": map[string]any{"name": "<Ann>", "has_sftp": true, "plan": ""},
		"promo":  "X",
	}
	cases := []struct{ src, want string }{
		{"Hi {{record.name}}", "Hi &lt;Ann&gt;"},
		{"{{if record.has_sftp}}A{{else}}B{{end}}", "A"},
		{"{{if record.plan}}A{{else}}B{{end}}", "B"},
		{"{{if !record.plan}}free{{end}}", "free"},
		{"{{if record.has_sftp}}{{if promo}}{{promo}}{{end}}{{end}}", "X"},
		{"{{ missing.path }}|", "|"},
	}
	for _, c := range cases {
		got, err := renderTemplate(c.src, data, true)
		if err != nil || got != c.want {
			t.Errorf("%q: got %q, %v; want %q", c.src, got, err, c.want)
		}
	}
	if got, _ := renderTemplate("{{record.name}}", data, false); got != "<Ann>" {
		t.Errorf("unescaped: %q", got)
	}
	for _, bad := range []string{"{{if x}}", "{{end}}", "{{else}}", "{{a b}}", "{{if x}}{{else}}{{else}}{{end}}"} {
		if _, err := parseTemplate(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
	if got := ConvertLegacyTemplate("Hi {$name}, {$count} left"); got != "Hi {{name}}, {{count}} left" {
		t.Errorf("legacy: %q", got)
	}
}

func TestCampaignSendsOncePerAddressAndHonorsSuppressions(t *testing.T) {
	app, bm := newApp(t, Config{Rate: 1000})
	users := verifiedUsers(t, app)

	suppressed := users[0].Email()
	if _, err := bm.Suppress(suppressed, ReasonUnsubscribed, "test", ""); err != nil {
		t.Fatal(err)
	}

	c := newCampaign(t, app, nil)
	if _, err := bm.Start(c.Id); err != nil {
		t.Fatal(err)
	}
	drain(t, bm)

	c, _ = bm.campaign(c.Id)
	if c.GetString("status") != StatusDone {
		t.Fatalf("status %s (%s)", c.GetString("status"), c.GetString("status_reason"))
	}
	want := len(users) - 1
	if c.GetInt("sent") != want || c.GetInt("skipped") != 1 || c.GetInt("queued") != 0 {
		t.Fatalf("counters sent=%d skipped=%d queued=%d, want sent=%d skipped=1",
			c.GetInt("sent"), c.GetInt("skipped"), c.GetInt("queued"), want)
	}
	if n := app.TestMailer.TotalSend(); n != want {
		t.Fatalf("mailer got %d messages, want %d", n, want)
	}

	msg := app.TestMailer.LastMessage()
	if msg.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" ||
		!strings.HasPrefix(msg.Headers["List-Unsubscribe"], "<https://app.test/api/bulkmail/unsubscribe?t=") {
		t.Fatalf("headers: %v", msg.Headers)
	}
	if !strings.Contains(msg.HTML, ", verified") || !strings.Contains(msg.HTML, "Cheers") ||
		!strings.Contains(msg.HTML, "Unsubscribe</a>") {
		t.Fatalf("html: %s", msg.HTML)
	}
	for _, m := range app.TestMailer.Messages() {
		if m.To[0].Address == suppressed {
			t.Fatal("mailed a suppressed address")
		}
	}

	// starting again only mails new matches: none
	if _, err := bm.Start(c.Id); err != nil {
		t.Fatal(err)
	}
	drain(t, bm)
	if n := app.TestMailer.TotalSend(); n != want {
		t.Fatalf("re-run mailed again: %d", n)
	}
}

func TestPockerPlugin(t *testing.T) {
	p := New(Config{Rate: 3})
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("bulkmail:\n  rate: 9\n  daily: 100\n"), &doc); err != nil {
		t.Fatal(err)
	}
	if err := p.Configure(&doc, pocker.Config{}); err != nil {
		t.Fatal(err)
	}
	if p.cfg.Rate != 3 || p.cfg.Daily != 100 {
		t.Fatalf("Go fields win, file fills the rest: %+v", p.cfg)
	}

	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	if err := p.InitApp(pocker.Instance{ID: "i1", Plugins: map[string]any{Name: map[string]any{"daily": 0}}}, app); err != nil {
		t.Fatal(err)
	}
	bm := For(app)
	if bm == nil || bm.cfg.Daily != NoBulkMail {
		t.Fatalf("instance daily: 0 means no bulk mail: %+v", bm)
	}
	vm := goja.New()
	p.InitJS(pocker.Instance{ID: "i1"}, app, vm)
	if v, err := vm.RunString("typeof $bulkmail.start"); err != nil || v.String() != "function" {
		t.Fatalf("$bulkmail: %v %v", v, err)
	}

	off, _ := tests.NewTestApp()
	defer off.Cleanup()
	if err := p.InitApp(pocker.Instance{ID: "i2", Plugins: map[string]any{Name: map[string]any{"enabled": false}}}, off); err != nil {
		t.Fatal(err)
	}
	if For(off) != nil {
		t.Fatal("enabled: false still registered")
	}
}

func TestStartLimitQueuesInSteps(t *testing.T) {
	app, bm := newApp(t, Config{Rate: 1000})
	users := verifiedUsers(t, app)
	c := newCampaign(t, app, nil)
	if _, err := bm.StartLimit(c.Id, 1); err != nil {
		t.Fatal(err)
	}
	drain(t, bm)
	if n := app.TestMailer.TotalSend(); n != 1 {
		t.Fatalf("limit 1: sent %d", n)
	}
	if _, err := bm.StartLimit(c.Id, 0); err != nil {
		t.Fatal(err)
	}
	drain(t, bm)
	if n := app.TestMailer.TotalSend(); n != len(users) {
		t.Fatalf("rest: sent %d, want %d", n, len(users))
	}
}

type failingMailer struct{ err error }

func (f failingMailer) Send(*mailer.Message) error { return f.err }

func TestFailuresRetryThenFailAndBrake(t *testing.T) {
	var mailErr error
	app, bm := newApp(t, Config{Rate: 1000, Mailer: func(core.App) mailer.Mailer { return failingMailer{mailErr} }})

	// permanent (5xx): fails at once
	mailErr = &textproto.Error{Code: 550, Msg: "no such user"}
	c := newCampaign(t, app, nil)
	if _, err := bm.Start(c.Id); err != nil {
		t.Fatal(err)
	}
	drain(t, bm)
	c, _ = bm.campaign(c.Id)
	if c.GetInt("failed") == 0 || c.GetInt("sent") != 0 || c.GetString("status") != StatusDone {
		t.Fatalf("permanent: failed=%d status=%s", c.GetInt("failed"), c.GetString("status"))
	}

	// temporary: stays queued with a retry time
	mailErr = errors.New("connection reset")
	c2 := newCampaign(t, app, nil)
	if _, err := bm.Start(c2.Id); err != nil {
		t.Fatal(err)
	}
	drain(t, bm)
	sends, _ := app.FindRecordsByFilter(SendsId, "campaign = {:c}", "", 0, 0, map[string]any{"c": c2.Id})
	for _, s := range sends {
		if s.GetString("status") != SendQueued || s.GetInt("attempts") != 1 || s.GetDateTime("retry_at").IsZero() {
			t.Fatalf("temporary: %s attempts=%d retry=%v", s.GetString("status"), s.GetInt("attempts"), s.Get("retry_at"))
		}
	}
	c2, _ = bm.campaign(c2.Id)
	if c2.GetString("status") != StatusSending {
		t.Fatalf("temporary: campaign %s", c2.GetString("status"))
	}

	// brake: >5% failures after 100 sends pauses the campaign
	c3 := newCampaign(t, app, nil)
	c3.Set("sent", 95)
	c3.Set("failed", 10)
	c3.Set("status", StatusSending)
	_ = app.Save(c3)
	sendsCol, _ := app.FindCollectionByNameOrId(SendsId)
	s := core.NewRecord(sendsCol)
	s.Set("campaign", c3.Id)
	s.Set("email", "x@y.test")
	s.Set("status", SendFailed)
	_ = app.Save(s)
	// counters come from the ledger, so fake 100+ ledger rows
	for i := 0; i < 104; i++ {
		r := core.NewRecord(sendsCol)
		r.Set("campaign", c3.Id)
		r.Set("email", "f"+string(rune('a'+i%26))+strings.Repeat("x", i/26)+"@y.test")
		r.Set("status", SendFailed)
		if err := app.Save(r); err != nil {
			t.Fatal(err)
		}
	}
	bm.saveSend(c3, s)
	c3, _ = bm.campaign(c3.Id)
	if c3.GetString("status") != StatusPaused || !strings.Contains(c3.GetString("status_reason"), "automatically") {
		t.Fatalf("brake: %s %q", c3.GetString("status"), c3.GetString("status_reason"))
	}
}

func TestDailyLimitAndNoBulkMail(t *testing.T) {
	app, bm := newApp(t, Config{Rate: 1000, Daily: 1})
	c := newCampaign(t, app, nil)
	if _, err := bm.Start(c.Id); err != nil {
		t.Fatal(err)
	}
	drain(t, bm)
	if n := app.TestMailer.TotalSend(); n != 1 {
		t.Fatalf("daily 1: sent %d", n)
	}
	c, _ = bm.campaign(c.Id)
	if c.GetString("status") != StatusSending || !strings.HasPrefix(c.GetString("status_reason"), "Daily limit") {
		t.Fatalf("daily: %s %q", c.GetString("status"), c.GetString("status_reason"))
	}

	app2, bm2 := newApp(t, Config{Daily: NoBulkMail})
	c2 := newCampaign(t, app2, nil)
	if _, err := bm2.Start(c2.Id); !errors.Is(err, errNotAllowed) {
		t.Fatalf("NoBulkMail: %v", err)
	}
}

func TestScheduledCampaignStartsWhenDue(t *testing.T) {
	now := time.Now().UTC()
	clock := now
	app, bm := newApp(t, Config{Rate: 1000, Now: func() time.Time { return clock }})
	c := newCampaign(t, app, map[string]any{"scheduled_at": now.Add(time.Hour)})
	if c2, err := bm.Start(c.Id); err != nil || c2.GetString("status") != StatusScheduled {
		t.Fatalf("schedule: %v", err)
	}
	drain(t, bm)
	if app.TestMailer.TotalSend() != 0 {
		t.Fatal("sent before scheduled_at")
	}
	clock = now.Add(2 * time.Hour)
	drain(t, bm)
	if app.TestMailer.TotalSend() == 0 {
		t.Fatal("didn't send after scheduled_at")
	}
}

func TestSQLAudienceIsReadOnly(t *testing.T) {
	app, bm := newApp(t, Config{})
	users := verifiedUsers(t, app)

	n, _, _, err := bm.CountAudience(Audience{SQL: "SELECT email, id FROM users WHERE verified = 1"}, 5)
	if err != nil || n != len(users) {
		t.Fatalf("count %d, %v", n, err)
	}
	for _, q := range []string{
		"DELETE FROM users",
		"SELECT 1; DELETE FROM users",
		"WITH x AS (SELECT 1) DELETE FROM users",
		"SELECT email FROM users WHERE id IN (SELECT id FROM users) RETURNING id",
	} {
		if _, err := bm.QueryReadOnly(q); err == nil {
			t.Errorf("%q should be refused", q)
		}
	}
	var count int
	_ = app.DB().Select("COUNT(*)").From("users").Row(&count)
	if count == 0 {
		t.Fatal("users were deleted")
	}
	// the connection is writable again afterwards
	if _, err := bm.Suppress("after@sql.test", ReasonManual, "test", ""); err != nil {
		t.Fatalf("write after read-only query: %v", err)
	}
}

func TestUnsubscribeLinks(t *testing.T) {
	app, bm := newApp(t, Config{})
	var hooked []string
	bm.OnUnsubscribe.BindFunc(func(e *SuppressEvent) error {
		hooked = append(hooked, e.Email+"/"+e.Source)
		return e.Next()
	})
	token := bm.UnsubscribeToken("Ann@Example.test", "camp1")
	if email, camp, err := bm.ParseUnsubscribeToken(token); err != nil || email != "ann@example.test" || camp != "camp1" {
		t.Fatalf("parse: %s %s %v", email, camp, err)
	}
	if _, _, err := bm.ParseUnsubscribeToken(token[:len(token)-2] + "xx"); err == nil {
		t.Fatal("tampered token accepted")
	}

	factory := func(testing.TB) *tests.TestApp { return app }
	scenarios := []tests.ApiScenario{
		{
			Name: "GET shows a confirmation and doesn't unsubscribe", Method: http.MethodGet,
			URL: "/api/bulkmail/unsubscribe?t=" + token, ExpectedStatus: 200,
			ExpectedContent: []string{"Stop sending", "ann@example.test", `method="post"`},
			TestAppFactory:  factory,
			AfterTestFunc: func(t testing.TB, _ *tests.TestApp, _ *http.Response) {
				if s, _ := bm.IsSuppressed("ann@example.test"); s {
					t.Fatal("GET unsubscribed")
				}
			},
		},
		{
			Name: "one-click POST unsubscribes", Method: http.MethodPost,
			URL:            "/api/bulkmail/unsubscribe?t=" + token,
			Body:           strings.NewReader("List-Unsubscribe=One-Click"),
			Headers:        map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
			ExpectedStatus: 200, ExpectedContent: []string{"won't receive"},
			TestAppFactory: factory,
			AfterTestFunc: func(t testing.TB, _ *tests.TestApp, _ *http.Response) {
				if s, _ := bm.IsSuppressed("ann@example.test"); !s {
					t.Fatal("not suppressed")
				}
				if len(hooked) != 1 || hooked[0] != "ann@example.test/one-click" {
					t.Fatalf("hook: %v", hooked)
				}
			},
		},
		{
			Name: "bad token", Method: http.MethodPost, URL: "/api/bulkmail/unsubscribe?t=nope",
			ExpectedStatus: 400, ExpectedContent: []string{"isn't valid"}, TestAppFactory: factory,
		},
		{
			Name: "superuser routes need auth", Method: http.MethodGet, URL: "/api/bulkmail/settings",
			ExpectedStatus: 401, ExpectedContent: []string{`"data":{}`}, TestAppFactory: factory,
		},
		{
			Name: "settings never return secrets", Method: http.MethodGet, URL: "/api/bulkmail/settings",
			Headers:        map[string]string{"Authorization": superuserToken},
			ExpectedStatus: 200, ExpectedContent: []string{`"secret":""`, `"daily":5000`, `"allowed":true`},
			TestAppFactory: factory,
		},
		{
			Name: "webhook needs its secret", Method: http.MethodPost, URL: "/api/bulkmail/feedback/webhook",
			Body:           strings.NewReader(`{"type":"bounce","email":"b@x.test"}`),
			ExpectedStatus: 401, ExpectedContent: []string{`"data":{}`}, TestAppFactory: factory,
		},
	}
	for _, s := range scenarios {
		s.DisableTestAppCleanup = true
		s.Test(t)
	}
	bm.StopSender()
}

func TestWebhookAndAudienceCountRoutes(t *testing.T) {
	app, bm := newApp(t, Config{})
	s, _ := bm.Settings()
	s.WebhookSecret = "hook-secret"
	if err := bm.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	users := verifiedUsers(t, app)
	factory := func(testing.TB) *tests.TestApp { return app }
	for _, sc := range []tests.ApiScenario{
		{
			Name: "webhook bounce", Method: http.MethodPost, URL: "/api/bulkmail/feedback/webhook",
			Body:           strings.NewReader(`{"type":"bounce","email":"B@x.test"}`),
			Headers:        map[string]string{"X-Bulkmail-Secret": "hook-secret"},
			ExpectedStatus: 204, TestAppFactory: factory,
		},
		{
			Name: "audience count", Method: http.MethodPost, URL: "/api/bulkmail/audience/count",
			Body:           strings.NewReader(`{"audience":{"collection":"users","filter":"verified = true"}}`),
			Headers:        map[string]string{"Authorization": superuserToken},
			ExpectedStatus: 200, ExpectedContent: []string{`"count":` + itoa(len(users))}, TestAppFactory: factory,
		},
	} {
		sc.DisableTestAppCleanup = true
		sc.Test(t)
	}
	bm.StopSender()
	rec, err := app.FindFirstRecordByData(SuppressionsId, "email", "b@x.test")
	if err != nil || rec.GetString("reason") != ReasonBounced || rec.GetString("source") != "webhook" {
		t.Fatalf("webhook suppression: %v %v", rec, err)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestSESFeedbackVerifiesSignatures(t *testing.T) {
	app, bm := newApp(t, Config{})

	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "sns.amazonaws.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	var fetched []string
	bm.fetchPEM = func(u string) ([]byte, error) { fetched = append(fetched, u); return certPEM, nil }

	sign := func(m *SNSMessage) {
		h := sha256.Sum256([]byte(m.stringToSign()))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
		m.Signature = base64.StdEncoding.EncodeToString(sig)
	}
	note := `{"notificationType":"Bounce","bounce":{"bounceType":"Permanent","bouncedRecipients":[{"emailAddress":"Gone@x.test"}]},"mail":{"headers":[{"name":"X-Bulkmail-Campaign","value":"c1"}]}}`
	m := &SNSMessage{Type: "Notification", MessageId: "1", TopicArn: "arn:aws:sns:us-east-1:1:ses", Message: note,
		Timestamp: "2026-11-11T00:00:00Z", SignatureVersion: "2",
		SigningCertURL: "https://sns.us-east-1.amazonaws.com/SimpleNotificationService-abc.pem"}
	sign(m)

	tampered := *m
	tampered.Message = strings.Replace(note, "Gone@", "victim@", 1)
	if err := bm.HandleSNS(&tampered); err == nil {
		t.Fatal("tampered message accepted")
	}
	evil := *m
	evil.SigningCertURL = "https://evil.test/cert.pem"
	if err := bm.HandleSNS(&evil); err == nil {
		t.Fatal("untrusted cert URL accepted")
	}
	if err := bm.HandleSNS(m); err != nil {
		t.Fatalf("valid message: %v", err)
	}
	rec, err := app.FindFirstRecordByData(SuppressionsId, "email", "gone@x.test")
	if err != nil || rec.GetString("reason") != ReasonBounced || rec.GetString("campaign") != "c1" {
		t.Fatalf("suppression: %v", err)
	}
	if s, _ := bm.IsSuppressed("victim@x.test"); s {
		t.Fatal("tampered recipient suppressed")
	}
	for _, u := range fetched {
		if strings.Contains(u, "evil") {
			t.Fatal("fetched an untrusted cert URL")
		}
	}

	// topic allow-list
	s, _ := bm.Settings()
	s.SNSTopicARNs = []string{"arn:aws:sns:us-east-1:1:other"}
	_ = bm.SaveSettings(s)
	if err := bm.HandleSNS(m); err == nil {
		t.Fatal("topic outside the allow-list accepted")
	}
}

func TestJSBinds(t *testing.T) {
	app, _ := newApp(t, Config{})
	vm := goja.New()
	BindJS(vm, app)
	v, err := vm.RunString(`
		$bulkmail.suppress("JS@x.test");
		const a = $bulkmail.isSuppressed("js@x.test");
		$bulkmail.unsuppress("js@x.test");
		const b = $bulkmail.isSuppressed("js@x.test");
		const c = $bulkmail.count({collection: "users", filter: "verified = true"}).count;
		const u = $bulkmail.unsubscribeURL("js@x.test", "c1");
		[a, b, c > 0, u.startsWith("https://app.test/api/bulkmail/unsubscribe?t=")].join(",")
	`)
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "true,false,true,true" {
		t.Fatalf("got %s", v.String())
	}
	if _, err := vm.RunString(`$bulkmail.start("missing")`); err == nil {
		t.Fatal("expected an error for a missing campaign")
	}
}

func TestPreviewAndTestSend(t *testing.T) {
	app, bm := newApp(t, Config{})
	users := verifiedUsers(t, app)
	c := newCampaign(t, app, map[string]any{"test_to": "qa@app.test"})

	msg, err := bm.Preview(c.Id, users[1].Email())
	if err != nil || msg.To[0].Address != users[1].Email() {
		t.Fatalf("preview: %v", err)
	}
	if err := bm.Test(c.Id, ""); err != nil {
		t.Fatal(err)
	}
	last := app.TestMailer.LastMessage()
	if last.To[0].Address != "qa@app.test" || !strings.HasPrefix(last.Subject, "[TEST] ") {
		t.Fatalf("test send: %v %s", last.To, last.Subject)
	}
	var n int
	_ = app.DB().Select("COUNT(*)").From(SendsCollection).Row(&n)
	if n != 0 {
		t.Fatal("test send wrote ledger rows")
	}
}

func TestSuppressionRecordsAreNormalized(t *testing.T) {
	app, bm := newApp(t, Config{})
	col, err := app.FindCollectionByNameOrId(SuppressionsCollection)
	if err != nil {
		t.Fatal(err)
	}

	// a record saved like the dashboard's record form does it
	rec := core.NewRecord(col)
	rec.Set("email", "  Mixed@Example.COM ")
	if err := app.Save(rec); err != nil {
		t.Fatal(err)
	}
	if got := rec.GetString("email"); got != "mixed@example.com" {
		t.Fatalf("email = %q", got)
	}
	if got := rec.GetString("reason"); got != ReasonManual {
		t.Fatalf("reason = %q", got)
	}
	if ok, _ := bm.IsSuppressed("mixed@example.com"); !ok {
		t.Fatal("not suppressed")
	}

	bad := core.NewRecord(col)
	bad.Set("email", "not an address")
	if err := app.Save(bad); err == nil {
		t.Fatal("saved an invalid address")
	}
}

func TestViewAudience(t *testing.T) {
	app, bm := newApp(t, Config{})
	view := core.NewViewCollection("mail_audience")
	view.ViewQuery = "SELECT id, email, lower(email) AS email_lc, name FROM users WHERE verified = TRUE"
	if err := app.Save(view); err != nil {
		t.Fatal(err)
	}
	want := len(verifiedUsers(t, app))

	for _, field := range []string{"email", "email_lc"} {
		n, _, _, err := bm.CountAudience(Audience{Collection: "mail_audience", EmailField: field}, 5)
		if err != nil {
			t.Fatalf("%s: %v", field, err)
		}
		if n != want {
			t.Fatalf("%s: count = %d, want %d", field, n, want)
		}
	}
	if _, _, _, err := bm.CountAudience(Audience{Collection: "mail_audience", EmailField: "id"}, 5); err == nil {
		t.Fatal("id accepted as the email field")
	}

	// a view audience sends like a collection one, with the view's columns in templates
	c := newCampaign(t, app, map[string]any{
		"audience": map[string]any{"collection": "mail_audience", "email_field": "email_lc"},
	})
	if _, err := bm.Start(c.Id); err != nil {
		t.Fatal(err)
	}
	drain(t, bm)
	if n := app.TestMailer.TotalSend(); n != want {
		t.Fatalf("sent %d, want %d", n, want)
	}

	// expression columns are json fields: templates show the value, not its JSON
	if got := toString(types.JSONRaw(`"a@b.test"`)); got != "a@b.test" {
		t.Fatalf("json string renders as %q", got)
	}
	if got := toString(types.JSONRaw(`3`)); got != "3" {
		t.Fatalf("json number renders as %q", got)
	}
}
