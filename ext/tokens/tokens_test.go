package tokens

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
)

func newApp(t *testing.T, cfg Config) (*tests.TestApp, *Tokens) {
	t.Helper()
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Cleanup)
	tk, err := Register(app, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(app); err != nil { // the test app is already bootstrapped
		t.Fatal(err)
	}
	tk.RegisterScope(Scope{Name: "deploy", Description: "Deploy code", Paths: []string{"/api/deploytest/"}})
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/api/deploytest/ping", func(e *core.RequestEvent) error {
			return e.String(http.StatusOK, "pong "+e.Auth.Email())
		}).Bind(apis.RequireSuperuserAuth(), RequireScope("deploy"))
		se.Router.GET("/_pocker/whoami", func(e *core.RequestEvent) error {
			if e.Auth == nil {
				return e.String(http.StatusOK, "anon")
			}
			return e.String(http.StatusOK, "me "+e.Auth.Email())
		})
		return se.Next()
	})
	return app, tk
}

func superuser(t *testing.T, app core.App) *core.Record {
	t.Helper()
	su, err := app.FindAuthRecordByEmail(core.CollectionNameSuperusers, "test@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return su
}

func authHeader(s string) map[string]string { return map[string]string{"Authorization": "Bearer " + s} }

func TestCreateVerifyRevoke(t *testing.T) {
	app, tk := newApp(t, Config{})
	su := superuser(t, app)

	secret, rec, err := tk.Create(su, CreateOptions{Name: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, Prefix) || len(secret) != len(Prefix)+secretLen {
		t.Fatalf("secret %q", secret)
	}
	if rec.GetString("hash") == secret || strings.Contains(rec.GetString("hash"), secret[len(Prefix):]) {
		t.Fatal("secret stored")
	}
	info, err := tk.Verify(secret)
	if err != nil || info.Owner.Id != su.Id || len(info.Scopes) != 0 {
		t.Fatalf("verify: %v %+v", err, info)
	}
	if _, err := tk.Verify(secret[:len(secret)-1] + "x"); err != ErrInvalid {
		t.Fatalf("tampered: %v", err)
	}
	if err := tk.Revoke(rec.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := tk.Verify(secret); err != ErrInvalid {
		t.Fatalf("revoked: %v", err)
	}
}

func TestCollectionsScopesAndExpiry(t *testing.T) {
	now := time.Now()
	app, tk := newApp(t, Config{MaxTTL: time.Hour, Now: func() time.Time { return now }})
	user, err := app.FindAuthRecordByEmail("users", "test@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := tk.Create(user, CreateOptions{}); err == nil {
		t.Fatal("users may not own tokens by default")
	}
	su := superuser(t, app)
	if _, _, err := tk.Create(su, CreateOptions{Scopes: []string{"nope"}}); err == nil {
		t.Fatal("unknown scope accepted")
	}
	secret, rec, err := tk.Create(su, CreateOptions{Scopes: []string{"Deploy", "deploy"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := scopesOf(rec); len(got) != 1 || got[0] != "deploy" {
		t.Fatalf("scopes %v", got)
	}
	if exp := rec.GetDateTime("expires").Time(); exp.Sub(now) > time.Hour+time.Second {
		t.Fatalf("MaxTTL not applied: %v", exp)
	}
	now = now.Add(2 * time.Hour)
	if _, err := tk.Verify(secret); err != ErrExpired {
		t.Fatalf("expired: %v", err)
	}
	if n, err := tk.DeleteExpired(); err != nil || n != 1 {
		t.Fatalf("delete expired: %d %v", n, err)
	}
}

func TestOwnerDeleteRevokes(t *testing.T) {
	app, tk := newApp(t, Config{Collections: []string{"users"}})
	user, _ := app.FindAuthRecordByEmail("users", "test@example.com")
	if _, _, err := tk.Create(user, CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := app.Delete(user); err != nil {
		t.Fatal(err)
	}
	if recs, _ := tk.List(nil); len(recs) != 0 {
		t.Fatalf("%d tokens left", len(recs))
	}
}

func TestBearerAuthAndScopes(t *testing.T) {
	app, tk := newApp(t, Config{Collections: []string{"_superusers", "users"}})
	su := superuser(t, app)
	full, _, _ := tk.Create(su, CreateOptions{Name: "full"})
	scoped, scopedRec, _ := tk.Create(su, CreateOptions{Name: "deploy", Scopes: []string{"deploy"}})
	user, _ := app.FindAuthRecordByEmail("users", "test@example.com")
	userTok, _, _ := tk.Create(user, CreateOptions{})

	factory := func(testing.TB) *tests.TestApp { return app }
	scenarios := []tests.ApiScenario{
		{
			Name: "unscoped token is its owner everywhere", Method: http.MethodGet, URL: "/api/collections",
			Headers: authHeader(full), ExpectedStatus: 200, ExpectedContent: []string{`"totalItems"`},
		},
		{
			Name: "the Bearer prefix is optional", Method: http.MethodGet, URL: "/api/collections",
			Headers: map[string]string{"Authorization": full}, ExpectedStatus: 200, ExpectedContent: []string{`"totalItems"`},
		},
		{
			Name: "scoped token on its path", Method: http.MethodGet, URL: "/api/deploytest/ping",
			Headers: authHeader(scoped), ExpectedStatus: 200, ExpectedContent: []string{"pong test@example.com"},
			AfterTestFunc: func(t testing.TB, app *tests.TestApp, _ *http.Response) {
				r, _ := app.FindRecordById(CollectionId, scopedRec.Id)
				if r.GetString("last_used") == "" || r.GetString("last_ip") == "" {
					t.Fatal("last use not recorded")
				}
			},
		},
		{
			Name: "scoped token elsewhere is refused", Method: http.MethodGet, URL: "/api/collections",
			Headers: authHeader(scoped), ExpectedStatus: 403, ExpectedContent: []string{"scopes"},
		},
		{
			Name: "scoped token may ask who it is", Method: http.MethodGet, URL: "/_pocker/whoami",
			Headers: authHeader(scoped), ExpectedStatus: 200, ExpectedContent: []string{"me test@example.com"},
		},
		{
			Name: "invalid token is anonymous", Method: http.MethodGet, URL: "/_pocker/whoami",
			Headers: authHeader(Prefix + strings.Repeat("a", secretLen)), ExpectedStatus: 200, ExpectedContent: []string{"anon"},
		},
		{
			Name: "a token can't mint a JWT", Method: http.MethodPost, URL: "/api/collections/_superusers/auth-refresh",
			Headers: authHeader(full), ExpectedStatus: 403, ExpectedContent: []string{"exchanged"},
		},
		{
			Name: "a token can't impersonate", Method: http.MethodPost, URL: "/api/collections/users/impersonate/" + user.Id,
			Headers: authHeader(full), ExpectedStatus: 403, ExpectedContent: []string{"exchanged"},
		},
		{
			Name: "a user token isn't a superuser", Method: http.MethodGet, URL: "/api/deploytest/ping",
			Headers: authHeader(userTok), ExpectedStatus: 403, ExpectedContent: []string{`"status":403`},
		},
		{
			Name: "a user lists only its own tokens", Method: http.MethodGet, URL: "/api/tokens",
			Headers: authHeader(userTok), ExpectedStatus: 200, ExpectedContent: []string{`"ownerCollection":"users"`},
			NotExpectedContent: []string{`"name":"full"`, `"hash"`},
		},
		{
			Name: "a user can't create tokens for others", Method: http.MethodPost, URL: "/api/tokens",
			Body:    strings.NewReader(`{"name":"x","owner":"` + su.Id + `","ownerCollection":"_superusers"}`),
			Headers: authHeader(userTok), ExpectedStatus: 403, ExpectedContent: []string{`"status":403`},
		},
		{
			Name: "superuser creates a scoped token for a user", Method: http.MethodPost, URL: "/api/tokens",
			Body:    strings.NewReader(`{"name":"made","owner":"test@example.com","ownerCollection":"users","scopes":["deploy"],"ttl":60}`),
			Headers: authHeader(full), ExpectedStatus: 200,
			ExpectedContent: []string{`"token":"pbt_`, `"scopes":["deploy"]`, `"ownerEmail":"test@example.com"`},
		},
		{
			Name: "superuser lists everything", Method: http.MethodGet, URL: "/api/tokens",
			Headers: authHeader(full), ExpectedStatus: 200, ExpectedContent: []string{`"name":"full"`, `"name":"made"`},
		},
		{
			Name: "scopes route", Method: http.MethodGet, URL: "/api/tokens/scopes",
			Headers: authHeader(full), ExpectedStatus: 200, ExpectedContent: []string{`"name":"deploy"`, `"canCreate":true`},
		},
		{
			Name: "a user can't revoke another's token", Method: http.MethodDelete, URL: "/api/tokens/" + scopedRec.Id,
			Headers: authHeader(userTok), ExpectedStatus: 404, ExpectedContent: []string{"not found"},
		},
		{
			Name: "revoke-all for self", Method: http.MethodPost, URL: "/api/tokens/revoke-all",
			Headers: authHeader(userTok), ExpectedStatus: 200, ExpectedContent: []string{`"revoked":2`},
		},
		{
			Name: "the revoked token is anonymous", Method: http.MethodGet, URL: "/api/tokens",
			Headers: authHeader(userTok), ExpectedStatus: 401, ExpectedContent: []string{`"status":401`},
		},
	}
	for _, s := range scenarios {
		s.TestAppFactory = factory
		s.DisableTestAppCleanup = true
		s.Test(t)
	}
}

func TestCommand(t *testing.T) {
	app, tk := newApp(t, Config{})
	run := func(args ...string) (string, error) {
		cmd := NewCommand(tk)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), err
	}
	out, err := run("create", "--superuser", "test@example.com", "--name", "ci", "--scope", "deploy", "--ttl", "24h")
	if err != nil {
		t.Fatal(err, out)
	}
	secret := strings.TrimSpace(out)
	info, err := tk.Verify(secret)
	if err != nil || !info.HasScope("deploy") || info.HasScope("other") {
		t.Fatalf("verify %q: %v", secret, err)
	}
	if out, err = run("list"); err != nil || !strings.Contains(out, "ci") || !strings.Contains(out, "deploy") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if _, err = run("create", "--name", "x"); err == nil {
		t.Fatal("create without owner")
	}
	if out, err = run("revoke", "--all", "--superuser", "test@example.com"); err != nil || !strings.Contains(out, "Revoked 1") {
		t.Fatalf("revoke: %v %s", err, out)
	}
	_ = app
}

func TestAudienceBinding(t *testing.T) {
	app, tk := newApp(t, Config{})
	su := superuser(t, app)
	mcpTok, _, err := tk.Create(su, CreateOptions{Audience: "https://app.example.com/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/mcp", func(e *core.RequestEvent) error { return e.String(200, "mcp "+e.Auth.Email()) })
		return se.Next()
	})
	factory := func(testing.TB) *tests.TestApp { return app }
	for _, s := range []tests.ApiScenario{
		{Name: "bound token on its resource", Method: http.MethodGet, URL: "/mcp", Headers: authHeader(mcpTok),
			ExpectedStatus: 200, ExpectedContent: []string{"mcp test@example.com"}},
		{Name: "bound token elsewhere", Method: http.MethodGet, URL: "/api/collections", Headers: authHeader(mcpTok),
			ExpectedStatus: 403, ExpectedContent: []string{"bound to another resource"}},
		{Name: "bound token on deploy", Method: http.MethodGet, URL: "/api/deploytest/ping", Headers: authHeader(mcpTok),
			ExpectedStatus: 403, ExpectedContent: []string{"bound to another resource"}},
		{Name: "bound token may ask who it is", Method: http.MethodGet, URL: "/_pocker/whoami", Headers: authHeader(mcpTok),
			ExpectedStatus: 200, ExpectedContent: []string{"me test@example.com"}},
	} {
		s.TestAppFactory = factory
		s.DisableTestAppCleanup = true
		s.Test(t)
	}
}
