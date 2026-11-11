package mcp

import (
	"bytes"
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

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	oauthserver "github.com/pockethost/pocketbase/ext/oauth-server"
	"github.com/pockethost/pocketbase/ext/tokens"
)

// The whole Claude connector flow against a real HTTP server: 401 → protected resource metadata → authorization
// server metadata → dynamic client registration → browser sign-in + consent → code + PKCE → token → /mcp.

type oauthEnv struct {
	t    *testing.T
	app  *tests.TestApp
	url  string
	tk   *tokens.Tokens
	http *http.Client
}

func newOAuthEnv(t *testing.T) *oauthEnv {
	t.Helper()
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Cleanup)
	// mcp first: the authenticator is picked on serve, so registration order doesn't matter
	MustRegister(app, Config{})
	tk := tokens.MustRegister(app, tokens.Config{})
	oauthserver.MustRegister(app, oauthserver.Config{})
	if err := app.OnBootstrap().Trigger(&core.BootstrapEvent{App: app}, func(*core.BootstrapEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := tokens.EnsureSchema(app); err != nil {
		t.Fatal(err)
	}
	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	var srv *httptest.Server
	if err := app.OnServe().Trigger(&core.ServeEvent{App: app, Router: router}, func(e *core.ServeEvent) error {
		mux, err := e.Router.BuildMux()
		srv = httptest.NewServer(mux)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return &oauthEnv{t: t, app: app, url: srv.URL, tk: tk, http: &http.Client{Transport: &http.Transport{}}}
}

func (o *oauthEnv) getJSON(u string) map[string]any {
	o.t.Helper()
	res, err := o.http.Get(u)
	if err != nil {
		o.t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	if res.StatusCode != 200 {
		o.t.Fatalf("GET %s: %d %v", u, res.StatusCode, m)
	}
	return m
}

// mcp posts a JSON-RPC request to /mcp.
func (o *oauthEnv) mcp(token, method string, params any) (*http.Response, map[string]any) {
	o.t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, _ := http.NewRequest(http.MethodPost, o.url+"/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := o.http.Do(req)
	if err != nil {
		o.t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return res, m
}

func (o *oauthEnv) tokenRequest(form url.Values) (int, map[string]any) {
	o.t.Helper()
	res, err := o.http.PostForm(o.url+"/oauth/token", form)
	if err != nil {
		o.t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return res.StatusCode, m
}

var (
	hiddenRE = regexp.MustCompile(`<input type="hidden" name="([^"]+)" value="([^"]*)">`)
	formRE   = regexp.MustCompile(`<form method="post" action="([^"]+)">`)
)

// signIn runs the browser part as the test superuser and returns the authorization code.
func (o *oauthEnv) signIn(authorizeURL, clientName string, wantScopes ...string) string {
	o.t.Helper()
	jar, _ := cookiejar.New(nil)
	b := &http.Client{Jar: jar, Transport: &http.Transport{}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var page string
	var last *url.URL
	do := func(req *http.Request) *http.Response {
		res, err := b.Do(req)
		if err != nil {
			o.t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		page, last = string(body), req.URL
		return res
	}
	submit := func(step string, fields map[string]string) *http.Response {
		m := formRE.FindStringSubmatch(page)
		if m == nil {
			o.t.Fatalf("no form: %s", page)
		}
		form := url.Values{}
		for _, h := range hiddenRE.FindAllStringSubmatch(page, -1) {
			form.Set(h[1], html.UnescapeString(h[2]))
		}
		for k, v := range fields {
			form.Set(k, v)
		}
		form.Set("step", step)
		target, _ := last.Parse(html.UnescapeString(m[1]))
		req, _ := http.NewRequest(http.MethodPost, target.String(), strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return do(req)
	}

	req, _ := http.NewRequest(http.MethodGet, authorizeURL, nil)
	do(req)
	submit("password", map[string]string{"email": "test@example.com", "password": "1234567890"})
	if !strings.Contains(page, clientName) {
		o.t.Fatalf("consent page doesn't name the client %q: %s", clientName, page)
	}
	for _, s := range wantScopes {
		if !strings.Contains(page, s) {
			o.t.Fatalf("consent page doesn't list %q: %s", s, page)
		}
	}
	res := submit("allow", nil)
	loc, _ := url.Parse(res.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		o.t.Fatalf("no code in redirect %q (%d): %s", res.Header.Get("Location"), res.StatusCode, page)
	}
	return code
}

func pkcePair() (string, string) {
	verifier := strings.Repeat("x", 64)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// connect does what Claude does when a connector is added and returns the token response.
func (o *oauthEnv) connect(scope string) map[string]any {
	o.t.Helper()

	// 1. unauthenticated → 401 pointing at the metadata
	res, _ := o.mcp("", "initialize", map[string]any{"protocolVersion": "2025-11-25"})
	if res.StatusCode != http.StatusUnauthorized {
		o.t.Fatalf("want 401, got %d", res.StatusCode)
	}
	m := regexp.MustCompile(`resource_metadata="([^"]+)"`).FindStringSubmatch(res.Header.Get("WWW-Authenticate"))
	if m == nil {
		o.t.Fatalf("no resource_metadata in %q", res.Header.Get("WWW-Authenticate"))
	}

	// 2. protected resource metadata
	prm := o.getJSON(m[1])
	resource := prm["resource"].(string)
	if resource != o.url+"/mcp" {
		o.t.Fatalf("resource %q", resource)
	}
	issuer := prm["authorization_servers"].([]any)[0].(string)

	// 3. authorization server metadata: same issuer, DCR on, MCP scopes listed
	as := o.getJSON(issuer + "/.well-known/oauth-authorization-server")
	if as["issuer"] != issuer || as["registration_endpoint"] == nil {
		o.t.Fatalf("AS metadata: %v", as)
	}
	scopes, _ := json.Marshal(as["scopes_supported"])
	if !strings.Contains(string(scopes), ScopeRead) || !strings.Contains(string(scopes), ScopeWrite) {
		o.t.Fatalf("MCP scopes not advertised: %s", scopes)
	}

	// 4. dynamic client registration, as Claude does with "Register automatically"
	redirect := "https://claude.ai/api/mcp/auth_callback"
	body, _ := json.Marshal(map[string]any{"client_name": "Claude", "redirect_uris": []string{redirect},
		"token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}})
	rr, err := o.http.Post(as["registration_endpoint"].(string), "application/json", bytes.NewReader(body))
	if err != nil {
		o.t.Fatal(err)
	}
	var client map[string]any
	_ = json.NewDecoder(rr.Body).Decode(&client)
	rr.Body.Close()
	clientID, _ := client["client_id"].(string)
	if rr.StatusCode != http.StatusCreated && rr.StatusCode != http.StatusOK || clientID == "" {
		o.t.Fatalf("registration: %d %v", rr.StatusCode, client)
	}

	// 5. browser: sign in, consent
	verifier, challenge := pkcePair()
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect}, "state": {"st"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "scope": {scope}, "resource": {resource}}
	want := []string{}
	for _, s := range strings.Fields(scope) {
		if s != "offline_access" {
			want = append(want, s)
		}
	}
	code := o.signIn(as["authorization_endpoint"].(string)+"?"+q.Encode(), "Claude", want...)

	// 6. code + PKCE → token bound to the resource
	st, tok := o.tokenRequest(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id": {clientID}, "code_verifier": {verifier}, "resource": {resource}})
	if st != 200 || tok["access_token"] == nil || tok["refresh_token"] == nil {
		o.t.Fatalf("token: %d %v", st, tok)
	}
	tok["client_id"] = clientID
	return tok
}

func toolsOf(m map[string]any) []string {
	res, _ := m["result"].(map[string]any)
	list, _ := res["tools"].([]any)
	out := []string{}
	for _, t := range list {
		out = append(out, t.(map[string]any)["name"].(string))
	}
	return out
}

func TestOAuthConnectorFlow(t *testing.T) {
	o := newOAuthEnv(t)
	tok := o.connect("mcp:read mcp:write offline_access")
	access := tok["access_token"].(string)

	res, m := o.mcp(access, "initialize", map[string]any{"protocolVersion": "2025-11-25"})
	if res.StatusCode != 200 || m["result"] == nil {
		t.Fatalf("initialize with OAuth token: %d %v", res.StatusCode, m)
	}
	_, m = o.mcp(access, "tools/list", nil)
	if names := toolsOf(m); !contains(names, "pb_create_record") || !contains(names, "pb_list_records") {
		t.Fatalf("full-scope token should see read and write tools: %v", names)
	}
	_, m = o.mcp(access, "tools/call", map[string]any{"name": "pb_server_info"})
	sc := m["result"].(map[string]any)["structuredContent"].(map[string]any)
	if sc["auth"].(map[string]any)["collection"] != core.CollectionNameSuperusers {
		t.Fatalf("server info: %v", sc)
	}

	// the MCP token can't be used elsewhere (audience + scope paths)
	req, _ := http.NewRequest(http.MethodGet, o.url+"/api/collections", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	if r, _ := o.http.Do(req); r.StatusCode == 200 {
		t.Fatal("MCP token worked on the records API")
	}

	// refresh: new token works, the old one is revoked
	st, tok2 := o.tokenRequest(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}, "client_id": {tok["client_id"].(string)}})
	if st != 200 {
		t.Fatalf("refresh: %d %v", st, tok2)
	}
	if res, _ := o.mcp(tok2["access_token"].(string), "ping", nil); res.StatusCode != 200 {
		t.Fatalf("refreshed token: %d", res.StatusCode)
	}
	if res, _ := o.mcp(access, "ping", nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token after refresh: want 401, got %d", res.StatusCode)
	}
}

func TestOAuthReadOnlyGrant(t *testing.T) {
	o := newOAuthEnv(t)
	access := o.connect("mcp:read offline_access")["access_token"].(string)

	_, m := o.mcp(access, "tools/list", nil)
	names := toolsOf(m)
	if !contains(names, "pb_list_records") || contains(names, "pb_create_record") {
		t.Fatalf("read-only token: %v", names)
	}
	res, _ := o.mcp(access, "tools/call", map[string]any{"name": "pb_create_record", "arguments": map[string]any{}})
	if res.StatusCode != http.StatusForbidden || !strings.Contains(res.Header.Get("WWW-Authenticate"), `scope="mcp:write"`) {
		t.Fatalf("write tool with read token: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
}

func TestOAuthRejectsTokensNotIssuedForMCP(t *testing.T) {
	o := newOAuthEnv(t)
	su, err := o.app.FindAuthRecordByEmail(core.CollectionNameSuperusers, "test@example.com")
	if err != nil {
		t.Fatal(err)
	}

	// an API token without an audience (e.g. made for deploys)
	unbound, _, err := o.tk.Create(su, tokens.CreateOptions{Name: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := o.mcp(unbound, "ping", nil); res.StatusCode != http.StatusUnauthorized ||
		!strings.Contains(res.Header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatalf("unbound API token: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}

	// a pasted PocketBase auth token
	jwt, _ := su.NewAuthToken()
	if res, _ := o.mcp(jwt, "ping", nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("PocketBase JWT: want 401, got %d", res.StatusCode)
	}

	// a token bound to another resource on this host
	other, _, err := o.tk.Create(su, tokens.CreateOptions{Name: "other", Audience: o.url + "/other"})
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := o.mcp(other, "ping", nil); res.StatusCode == 200 {
		t.Fatal("token for another resource accepted")
	}

	// a token bound to /mcp works
	bound, _, err := o.tk.Create(su, tokens.CreateOptions{Name: "mcp", Audience: o.url + "/mcp", Scopes: []string{ScopeRead}})
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := o.mcp(bound, "ping", nil); res.StatusCode != 200 {
		t.Fatalf("token bound to /mcp: %d", res.StatusCode)
	}
}

func TestSameResource(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"https://Acme.example.com/mcp", "https://acme.example.com/mcp/", true},
		{"https://acme.example.com/mcp", "http://acme.example.com/mcp", false},
		{"https://acme.example.com/mcp", "https://acme.example.com/other", false},
		{"https://acme.example.com/mcp", "https://evil.example.com/mcp", false},
	} {
		if got := sameResource(c.a, c.b); got != c.want {
			t.Errorf("sameResource(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
