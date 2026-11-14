package oauthserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"github.com/pockethost/pocketone/ext/remotes"
	"github.com/pockethost/pocketone/ext/tokens"
)

func serveApp(t *testing.T, app *tests.TestApp) string {
	t.Helper()
	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	se := &core.ServeEvent{App: app, Router: router}
	var srv *httptest.Server
	err = app.OnServe().Trigger(se, func(e *core.ServeEvent) error {
		mux, err := e.Router.BuildMux()
		if err != nil {
			return err
		}
		srv = httptest.NewServer(mux)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return srv.URL
}

type env struct {
	app *tests.TestApp
	url string
	srv *Server
	tk  *tokens.Tokens
}

func newEnv(t *testing.T, cfg Config) *env {
	t.Helper()
	pollInterval = 1
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Cleanup)
	tk := tokens.MustRegister(app, tokens.Config{})
	remotes.MustRegister(app, remotes.Config{})
	srv := MustRegister(app, cfg)
	if err := app.OnBootstrap().Trigger(&core.BootstrapEvent{App: app}, func(*core.BootstrapEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := tokens.EnsureSchema(app); err != nil {
		t.Fatal(err)
	}
	tk.RegisterScope(tokens.Scope{Name: "deploy", Description: "Deploy code", Paths: []string{"/_pocker/deploy/"}})
	return &env{app: app, url: serveApp(t, app), srv: srv, tk: tk}
}

// browser is a scripted user: cookies, and forms posted by step.
type browser struct {
	t      *testing.T
	client *http.Client
	page   string // last HTML
	status int
	last   *url.URL
}

func newBrowser(t *testing.T) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, client: &http.Client{Jar: jar, Transport: &http.Transport{},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (b *browser) do(req *http.Request) *http.Response {
	b.t.Helper()
	res, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	b.page, b.status, b.last = string(body), res.StatusCode, req.URL
	return res
}

func (b *browser) get(u string) *http.Response {
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	return b.do(req)
}

var inputRE = regexp.MustCompile(`<input type="hidden" name="([^"]+)" value="([^"]*)">`)
var actionRE = regexp.MustCompile(`<form method="post" action="([^"]+)">`)

// submit posts the page's form with its hidden fields, extra fields and step.
func (b *browser) submit(step string, fields map[string]string) *http.Response {
	b.t.Helper()
	m := actionRE.FindStringSubmatch(b.page)
	if m == nil {
		b.t.Fatalf("no form on page (%d): %s", b.status, b.page)
	}
	form := url.Values{}
	for _, h := range inputRE.FindAllStringSubmatch(b.page, -1) {
		form.Set(h[1], html.UnescapeString(h[2]))
	}
	for k, v := range fields {
		form.Set(k, v)
	}
	form.Set("step", step)
	target, _ := b.last.Parse(html.UnescapeString(m[1]))
	req, _ := http.NewRequest(http.MethodPost, target.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return b.do(req)
}

func (b *browser) has(s string) bool { return strings.Contains(b.page, s) }

func pkce() (verifier, challenge string) {
	verifier = strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func postToken(t *testing.T, base string, form url.Values) (int, map[string]any) {
	t.Helper()
	res, err := (&http.Client{Transport: &http.Transport{}}).PostForm(base+"/oauth/token", form)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestMetadata(t *testing.T) {
	e := newEnv(t, Config{DynamicRegistration: true})
	res, err := (&http.Client{Transport: &http.Transport{}}).Get(e.url + MetadataPath)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.NewDecoder(res.Body).Decode(&m)
	res.Body.Close()
	if m["issuer"] != e.url || m["token_endpoint"] != e.url+"/oauth/token" || m["registration_endpoint"] != e.url+"/oauth/register" {
		t.Fatalf("%v", m)
	}
	// discovery through whoami
	rc := &remotes.Client{Remote: &remotes.Remote{URL: e.url}, HTTP: remotes.NewHTTPClient()}
	w, err := rc.Whoami(context.Background())
	if err != nil || w.Features["oauth"] != MetadataPath {
		t.Fatalf("%+v %v", w, err)
	}
}

func TestAuthorizeCodeFlow(t *testing.T) {
	e := newEnv(t, Config{})
	verifier, challenge := pkce()
	redirect := "http://127.0.0.1:43123/callback"
	q := url.Values{"response_type": {"code"}, "client_id": {CLIClientId}, "redirect_uri": {redirect}, "state": {"st"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "scope": {"deploy"}}
	b := newBrowser(t)

	b.get(e.url + "/oauth/authorize?" + q.Get("x") + "client_id=nope")
	if b.status != 400 || !b.has("Unknown client") {
		t.Fatalf("unknown client: %d", b.status)
	}
	bad := url.Values{}
	for k, v := range q {
		bad[k] = v
	}
	bad.Set("redirect_uri", "https://evil.example.com/cb")
	if b.get(e.url + "/oauth/authorize?" + bad.Encode()); b.status != 400 || !b.has("redirect") {
		t.Fatalf("bad redirect: %d %s", b.status, b.page)
	}
	bad.Set("redirect_uri", redirect)
	bad.Del("code_challenge")
	if res := b.get(e.url + "/oauth/authorize?" + bad.Encode()); res.StatusCode != 302 || !strings.Contains(res.Header.Get("Location"), "error=invalid_request") {
		t.Fatalf("no PKCE: %d %s", res.StatusCode, res.Header.Get("Location"))
	}

	b.get(e.url + "/oauth/authorize?" + q.Encode())
	if b.status != 200 || !b.has("Sign in to") || !b.has("PocketBase CLI") {
		t.Fatalf("sign-in page: %d %s", b.status, b.page)
	}
	b.submit("password", map[string]string{"email": "test@example.com", "password": "wrong"})
	if b.status != 401 || !b.has("Wrong email or password") {
		t.Fatalf("wrong password: %d", b.status)
	}
	b.submit("password", map[string]string{"email": "test@example.com", "password": "1234567890"})
	if b.status != 200 || !b.has("wants to access") || !b.has("deploy") || !b.has("an app on this computer") {
		t.Fatalf("consent: %d %s", b.status, b.page)
	}
	res := b.submit("allow", nil)
	loc, _ := url.Parse(res.Header.Get("Location"))
	if res.StatusCode != 302 || loc.Query().Get("state") != "st" || loc.Query().Get("code") == "" || loc.Host != "127.0.0.1:43123" {
		t.Fatalf("redirect: %d %s", res.StatusCode, loc)
	}
	code := loc.Query().Get("code")

	if st, out := postToken(t, e.url, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id": {CLIClientId}, "code_verifier": {"wrong-verifier"}}); st != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("bad verifier: %d %v", st, out)
	}
	// the code is single use even after a failed exchange: start over
	res = b.get(e.url + "/oauth/authorize?" + q.Encode()) // already signed in: straight to consent
	if !b.has("wants to access") {
		t.Fatalf("session not kept: %s", b.page)
	}
	res = b.submit("allow", nil)
	loc, _ = url.Parse(res.Header.Get("Location"))
	code = loc.Query().Get("code")
	st, tok := postToken(t, e.url, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id": {CLIClientId}, "code_verifier": {verifier}})
	if st != 200 || !strings.HasPrefix(tok["access_token"].(string), "pbt_") || tok["scope"] != "deploy" || tok["refresh_token"] == "" {
		t.Fatalf("token: %d %v", st, tok)
	}
	if st, out := postToken(t, e.url, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id": {CLIClientId}, "code_verifier": {verifier}}); st != 400 || out["error"] != "invalid_grant" {
		t.Fatal("code reused")
	}
	access := tok["access_token"].(string)
	info, err := e.tk.Verify(access)
	if err != nil || info.Token.GetString("client_id") != CLIClientId || !info.HasScope("deploy") || info.HasScope("other") {
		t.Fatalf("access token: %v", err)
	}

	// refresh rotates both and revokes the old access token
	st, tok2 := postToken(t, e.url, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}, "client_id": {CLIClientId}})
	if st != 200 || tok2["access_token"] == access {
		t.Fatalf("refresh: %d %v", st, tok2)
	}
	if _, err := e.tk.Verify(access); err == nil {
		t.Fatal("old access token survived the refresh")
	}
	if st, _ := postToken(t, e.url, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}, "client_id": {CLIClientId}}); st != 400 {
		t.Fatal("refresh token reused")
	}

	// revoke
	res2, _ := (&http.Client{Transport: &http.Transport{}}).PostForm(e.url+"/oauth/revoke", url.Values{"token": {tok2["refresh_token"].(string)}, "client_id": {CLIClientId}})
	res2.Body.Close()
	if _, err := e.tk.Verify(tok2["access_token"].(string)); err == nil {
		t.Fatal("revoke left the access token")
	}

	// deny
	b.get(e.url + "/oauth/authorize?" + q.Encode())
	res = b.submit("deny", nil)
	if !strings.Contains(res.Header.Get("Location"), "error=access_denied") {
		t.Fatalf("deny: %s", res.Header.Get("Location"))
	}
	// CSRF
	b.get(e.url + "/oauth/authorize?" + q.Encode())
	b.page = strings.Replace(b.page, `name="csrf" value="`, `name="csrf" value="x`, 1)
	if b.submit("allow", nil); b.status != 403 {
		t.Fatalf("csrf: %d", b.status)
	}
	// unknown scope
	q.Set("scope", "nope")
	if res := b.get(e.url + "/oauth/authorize?" + q.Encode()); !strings.Contains(res.Header.Get("Location"), "error=invalid_scope") {
		t.Fatalf("scope: %s", res.Header.Get("Location"))
	}
}

func TestUsersCantSignInByDefault(t *testing.T) {
	e := newEnv(t, Config{})
	_, challenge := pkce()
	q := url.Values{"response_type": {"code"}, "client_id": {CLIClientId}, "redirect_uri": {"http://127.0.0.1:1/cb"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	b := newBrowser(t)
	b.get(e.url + "/oauth/authorize?" + q.Encode())
	b.submit("password", map[string]string{"collection": "users", "email": "test@example.com", "password": "1234567890"})
	if b.status != 401 || !b.has("can&#39;t sign in here") {
		t.Fatalf("%d %s", b.status, b.page)
	}
}

// The CLI half (ext/remotes) against the server: browser login with the scripted browser standing in.
func TestCLIBrowserLogin(t *testing.T) {
	e := newEnv(t, Config{})
	old, oldCan := remotes.OpenBrowser, remotes.CanOpenBrowser
	defer func() { remotes.OpenBrowser, remotes.CanOpenBrowser = old, oldCan }()
	remotes.CanOpenBrowser = func() bool { return true } // a CI runner has no display
	remotes.OpenBrowser = func(u string) error {
		go func() {
			b := newBrowser(t)
			b.get(u)
			b.submit("password", map[string]string{"email": "test@example.com", "password": "1234567890"})
			res := b.submit("allow", nil)
			// follow the redirect to the CLI's loopback listener
			(&http.Client{Transport: &http.Transport{}}).Get(res.Header.Get("Location"))
		}()
		return nil
	}
	rc := &remotes.Client{Remote: &remotes.Remote{Name: "local", URL: e.url}, HTTP: remotes.NewHTTPClient()}
	w, _ := rc.Whoami(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var out bytes.Buffer
	res, err := remotes.OAuthLogin(ctx, rc, w, remotes.BrowserLoginOptions{Out: &out})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	cr := res.Credential
	if cr.Via != "oauth" || !strings.HasPrefix(cr.Token, "pbt_") || cr.RefreshToken == "" || cr.Email != "test@example.com" || cr.Expires.IsZero() {
		t.Fatalf("%+v", cr)
	}
}

func TestCLIDeviceLogin(t *testing.T) {
	e := newEnv(t, Config{})
	rc := &remotes.Client{Remote: &remotes.Remote{Name: "local", URL: e.url}, HTTP: remotes.NewHTTPClient()}
	w, _ := rc.Whoami(context.Background())
	pr, pw := io.Pipe()
	codeCh := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		var all string
		for {
			n, err := pr.Read(buf)
			all += string(buf[:n])
			if m := regexp.MustCompile(`enter the code\s+([A-Z]{4}-[A-Z]{4})`).FindStringSubmatch(all); m != nil {
				codeCh <- m[1]
				io.Copy(io.Discard, pr)
				return
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		code := <-codeCh
		b := newBrowser(t)
		b.get(e.url + "/oauth/device/verify")
		if !b.has("Connect a device") {
			t.Errorf("device page: %s", b.page)
			return
		}
		b.get(e.url + "/oauth/device/verify?user_code=" + strings.ToLower(strings.ReplaceAll(code, "-", "")))
		b.submit("password", map[string]string{"email": "test@example.com", "password": "1234567890"})
		b.submit("allow", nil)
		if !b.has("is connected") {
			t.Errorf("approve: %s", b.page)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := remotes.OAuthLogin(ctx, rc, w, remotes.BrowserLoginOptions{Device: true, Out: pw})
	pw.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Credential.Token, "pbt_") || res.Credential.Email != "test@example.com" {
		t.Fatalf("%+v", res.Credential)
	}
}

func TestDynamicRegistration(t *testing.T) {
	e := newEnv(t, Config{DynamicRegistration: true})
	body := `{"client_name":"Claude","redirect_uris":["https://claude.ai/api/mcp/auth_callback","http://127.0.0.1/cb"]}`
	res, err := (&http.Client{Transport: &http.Transport{}}).Post(e.url+"/oauth/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var reg map[string]any
	json.NewDecoder(res.Body).Decode(&reg)
	res.Body.Close()
	id, _ := reg["client_id"].(string)
	if res.StatusCode != 201 || id == "" || reg["token_endpoint_auth_method"] != "none" || reg["client_secret"] != nil {
		t.Fatalf("%d %v", res.StatusCode, reg)
	}
	c, err := e.srv.FindClient(id)
	if err != nil || !c.AllowsRedirect("https://claude.ai/api/mcp/auth_callback") || !c.AllowsRedirect("http://127.0.0.1:5555/cb") ||
		c.AllowsRedirect("https://claude.ai/other") || c.AllowsRedirect("http://127.0.0.1:5555/other") {
		t.Fatalf("redirects %+v %v", c, err)
	}
	res, _ = (&http.Client{Transport: &http.Transport{}}).Post(e.url+"/oauth/register", "application/json",
		strings.NewReader(`{"client_name":"x","redirect_uris":["http://evil.example.com/cb"]}`))
	if res.StatusCode != 400 {
		t.Fatalf("plain http accepted: %d", res.StatusCode)
	}
	// off by default
	off := newEnv(t, Config{})
	res, _ = (&http.Client{Transport: &http.Transport{}}).Post(off.url+"/oauth/register", "application/json", strings.NewReader(body))
	if res.StatusCode != 404 {
		t.Fatalf("registration on by default: %d", res.StatusCode)
	}
}

func TestConfidentialClientAndCommand(t *testing.T) {
	e := newEnv(t, Config{})
	var out bytes.Buffer
	cmd := NewCommand(e.srv)
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"clients", "add", "--name", "Web app", "--redirect", "https://app.example.com/cb"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`client_id: (\S+)\nclient_secret: (\S+)`).FindStringSubmatch(out.String())
	if m == nil {
		t.Fatal(out.String())
	}
	if st, o := postToken(t, e.url, url.Values{"grant_type": {"authorization_code"}, "code": {"x"}, "client_id": {m[1]}}); st != 401 || o["error"] != "invalid_client" {
		t.Fatalf("no secret: %d %v", st, o)
	}
	if st, o := postToken(t, e.url, url.Values{"grant_type": {"authorization_code"}, "code": {"x"}, "client_id": {m[1]}, "client_secret": {m[2]}}); st != 400 || o["error"] != "invalid_grant" {
		t.Fatalf("with secret: %d %v", st, o)
	}
	out.Reset()
	cmd.SetArgs([]string{"clients", "list"})
	cmd.Execute()
	if !strings.Contains(out.String(), "Web app") || !strings.Contains(out.String(), CLIClientId) {
		t.Fatal(out.String())
	}
}

func TestPagesRendererHook(t *testing.T) {
	e := newEnv(t, Config{Render: func(ev *core.RequestEvent, page string, d PageData) (bool, error) {
		if page != PageSignIn {
			return false, nil
		}
		return true, ev.HTML(200, "custom "+page+" "+d.ClientName+" csrf="+d.CSRF)
	}, Branding: Branding{Name: "Acme Cloud"}})
	b := newBrowser(t)
	_, challenge := pkce()
	b.get(e.url + "/oauth/authorize?" + url.Values{"response_type": {"code"}, "client_id": {CLIClientId}, "redirect_uri": {"http://127.0.0.1:1/cb"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}}.Encode())
	if !b.has("custom signin PocketBase CLI csrf=") {
		t.Fatal(b.page)
	}
	b.get(e.url + "/oauth/device/verify")
	if !b.has("Acme Cloud") {
		t.Fatal("branding")
	}
}

func TestOTPAndMFA(t *testing.T) {
	e := newEnv(t, Config{})
	col, _ := e.app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
	col.OTP.Enabled = true
	col.MFA.Enabled = true
	if err := e.app.Save(col); err != nil {
		t.Fatal(err)
	}
	codeFromMail := func() string {
		m := regexp.MustCompile(`\b(\d{6,8})\b`).FindStringSubmatch(e.app.TestMailer.LastMessage().HTML)
		if m == nil {
			t.Fatal("no code in mail")
		}
		return m[1]
	}
	_, challenge := pkce()
	q := url.Values{"response_type": {"code"}, "client_id": {CLIClientId}, "redirect_uri": {"http://127.0.0.1:1/cb"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}}

	// OTP alone isn't enough when MFA is on
	b := newBrowser(t)
	b.get(e.url + "/oauth/authorize?" + q.Encode())
	b.submit("request_otp", map[string]string{"email": "test@example.com"})
	if !b.has("Check your email") {
		t.Fatalf("otp page: %s", b.page)
	}
	b.submit("otp", map[string]string{"code": codeFromMail()})
	if !b.has("needs its password and a code") {
		t.Fatalf("mfa: %s", b.page)
	}

	// password, then the emailed code
	b = newBrowser(t)
	b.get(e.url + "/oauth/authorize?" + q.Encode())
	b.submit("password", map[string]string{"email": "test@example.com", "password": "1234567890"})
	if !b.has("Check your email") {
		t.Fatalf("mfa otp page: %s", b.page)
	}
	b.submit("otp", map[string]string{"code": "000000"})
	if b.status != 401 || !b.has("wrong or expired") {
		t.Fatalf("bad code: %d", b.status)
	}
	b.submit("otp", map[string]string{"code": codeFromMail()})
	if !b.has("wants to access") {
		t.Fatalf("consent after MFA: %s", b.page)
	}
}

func TestEnsureSchemaAddsNewFields(t *testing.T) {
	e := newEnv(t, Config{})
	col, _ := e.app.FindCollectionByNameOrId(CodesCollectionId)
	col.Fields.RemoveById("oad_resource")
	if err := e.app.Save(col); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(e.app); err != nil {
		t.Fatal(err)
	}
	col, _ = e.app.FindCollectionByNameOrId(CodesCollectionId)
	if col.Fields.GetById("oad_resource") == nil {
		t.Fatal("field not added back")
	}
}
