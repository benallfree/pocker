package pages

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// fakeOIDC is a minimal OIDC provider: /auth (never called; the browser would go there), /token and /userinfo.
func fakeOIDC(t *testing.T, sub, email string) (*httptest.Server, *url.Values) {
	var mu sync.Mutex
	var lastToken url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			mu.Lock()
			lastToken = r.PostForm
			mu.Unlock()
			if r.PostForm.Get("code") != "good-code" {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"at","token_type":"bearer","expires_in":3600}`)
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"sub": sub, "email": email, "email_verified": true, "name": "Oidc User"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &lastToken
}

func TestOAuth2SignIn(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"+config.js":                   "module.exports = { plugins: ['auth'] }",
		"auth/oauth/login/+page.js":    "module.exports = { get(ctx) { ctx.session.requestOAuth2('oidc') } }",
		"auth/oauth/login/index.ejs":   "unused",
		"auth/oauth/confirm/+page.js":  "module.exports = { get(ctx) { try { const r = ctx.session.signInWithOAuth2(ctx.req.query('state'), ctx.req.query('code')); ctx.data.isNew = r.meta.isNew } catch (e) { ctx.data.err = String(e) } } }",
		"auth/oauth/confirm/index.ejs": "<%= data.err ? 'ERR ' + data.err : 'OK ' + ctx.auth.email() + ' new=' + data.isNew %>",
	})
	h, p := newServer(t, dir)
	srv, lastToken := fakeOIDC(t, "oidc-1", "oidc@example.com")

	users, err := p.app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	users.OAuth2.Enabled = true
	users.MFA.Enabled = false // the test app turns it on
	users.OAuth2.Providers = []core.OAuth2ProviderConfig{{
		Name: "oidc", ClientId: "cid", ClientSecret: "secret",
		AuthURL: srv.URL + "/auth", TokenURL: srv.URL + "/token", UserInfoURL: srv.URL + "/userinfo",
	}}
	if err := p.app.Save(users); err != nil {
		t.Fatal(err)
	}

	do := func(path, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	cookieOf := func(rec *httptest.ResponseRecorder, name string) string {
		for _, c := range rec.Result().Cookies() {
			if c.Name == name {
				return name + "=" + c.Value
			}
		}
		return ""
	}
	start := func() (state, cookie string) {
		rec := do("/auth/oauth/login", "")
		if rec.Code != http.StatusFound {
			t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
		}
		loc, _ := url.Parse(rec.Header().Get("Location"))
		q := loc.Query()
		if !strings.HasPrefix(loc.String(), srv.URL+"/auth?") || q.Get("redirect_uri") != "http://example.com/auth/oauth/confirm" ||
			q.Get("code_challenge") == "" || q.Get("client_id") != "cid" {
			t.Fatalf("auth url: %s", loc)
		}
		return q.Get("state"), cookieOf(rec, oauth2Cookie)
	}

	// wrong state
	state, cookie := start()
	if body := do("/auth/oauth/confirm?state=nope&code=good-code", cookie).Body.String(); !strings.Contains(body, "ERR") {
		t.Fatalf("wrong state accepted: %s", body)
	}
	// no cookie
	if body := do("/auth/oauth/confirm?state="+state+"&code=good-code", "").Body.String(); !strings.Contains(body, "ERR") {
		t.Fatalf("missing cookie accepted: %s", body)
	}
	// provider rejects the code
	state, cookie = start()
	if body := do("/auth/oauth/confirm?state="+state+"&code=bad", cookie).Body.String(); !strings.Contains(body, "ERR") {
		t.Fatalf("bad code accepted: %s", body)
	}

	// success: new user
	state, cookie = start()
	rec := do("/auth/oauth/confirm?state="+state+"&code=good-code", cookie)
	if body := rec.Body.String(); !strings.Contains(body, "OK oidc@example.com new=true") {
		t.Fatalf("sign-in: %s", body)
	}
	if cookieOf(rec, "pb_auth") == "" {
		t.Fatal("no pb_auth cookie after OAuth2 sign-in")
	}
	if lastToken.Get("code_verifier") == "" || lastToken.Get("redirect_uri") != "http://example.com/auth/oauth/confirm" {
		t.Fatalf("token request: %v", *lastToken)
	}
	// the state cookie is one-use
	if body := do("/auth/oauth/confirm?state="+state+"&code=good-code", cookieOf(rec, oauth2Cookie)).Body.String(); !strings.Contains(body, "ERR") {
		t.Fatalf("state reused: %s", body)
	}

	// second time: same account, not new
	state, cookie = start()
	if body := do("/auth/oauth/confirm?state="+state+"&code=good-code", cookie).Body.String(); !strings.Contains(body, "OK oidc@example.com new=false") {
		t.Fatalf("second sign-in: %s", body)
	}

	// MFA on: PocketBase asks for a second step, which pages reports clearly
	users.MFA.Enabled = true
	if err := p.app.Save(users); err != nil {
		t.Fatal(err)
	}
	state, cookie = start()
	if body := do("/auth/oauth/confirm?state="+state+"&code=good-code", cookie).Body.String(); !strings.Contains(body, "MFA") {
		t.Fatalf("mfa: %s", body)
	}
}
