package remotes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"github.com/pockethost/pocketone/ext/tokens"
)

// serve runs app's router (OnServe included) on a local test server.
func serve(t *testing.T, app *tests.TestApp) string {
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

func newApp(t *testing.T, withTokens bool) (*tests.TestApp, string) {
	t.Helper()
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Cleanup)
	if err := Register(app, Config{}); err != nil {
		t.Fatal(err)
	}
	Advertise(app, "deploy", "/_pocker/deploy/v1")
	if withTokens {
		tokens.MustRegister(app, tokens.Config{})
		if err := tokens.EnsureSchema(app); err != nil {
			t.Fatal(err)
		}
	}
	// the test app's superuser test@example.com has password 1234567890
	return app, serve(t, app)
}

func isolate(t *testing.T) string {
	t.Helper()
	t.Setenv("POCKETBASE_CONFIG_DIR", t.TempDir())
	t.Setenv("POCKETBASE_TOKEN", "")
	t.Setenv("POCKETHOST_TOKEN", "")
	t.Setenv("POCKETBASE_PASSWORD", "")
	return t.TempDir()
}

func TestProjectFile(t *testing.T) {
	dir := isolate(t)
	p, err := LoadProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Resolve(""); err == nil || !strings.Contains(err.Error(), "remote add") {
		t.Fatalf("empty project: %v", err)
	}
	if _, err := p.Add("bad name", "x.test"); err == nil {
		t.Fatal("bad name accepted")
	}
	if _, err := p.Add("prod", "prod.example.test/"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Add("staging", "http://127.0.0.1:8090"); err != nil {
		t.Fatal(err)
	}
	if err := p.Save(); err != nil {
		t.Fatal(err)
	}

	sub := filepath.Join(dir, "pb_hooks", "deep")
	os.MkdirAll(sub, 0o755)
	p2, err := LoadProject(sub) // found by walking up
	if err != nil {
		t.Fatal(err)
	}
	if p2.Default != "prod" || p2.Root != dir {
		t.Fatalf("default %q root %q", p2.Default, p2.Root)
	}
	r, _ := p2.Resolve("")
	if r.URL != "https://prod.example.test" {
		t.Fatalf("url %q", r.URL)
	}
	if r, _ := p2.Resolve("http://other.test"); r.URL != "http://other.test" {
		t.Fatal("URL as a name")
	}
	if _, err := p2.Resolve("nope"); err == nil || !strings.Contains(err.Error(), "prod, staging") {
		t.Fatalf("unknown: %v", err)
	}
}

func TestLegacyPocketHostLinkAndEnv(t *testing.T) {
	dir := isolate(t)
	os.WriteFile(filepath.Join(dir, LegacyPocketHostFile), []byte(`{"instanceName":"myapp"}`), 0o644)
	p, err := LoadProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Resolve("")
	if err != nil || r.Name != PocketHostRemote || r.URL != "https://myapp.pockethost.io" {
		t.Fatalf("%+v %v", r, err)
	}
	if err := p.Remove(PocketHostRemote); err == nil {
		t.Fatal("legacy remote removed")
	}
	p.Add("prod", "prod.test")
	p.Save()
	b, _ := os.ReadFile(filepath.Join(dir, ProjectDir, ProjectFile))
	if strings.Contains(string(b), "pockethost.io") {
		t.Fatalf("legacy remote written: %s", b)
	}

	t.Setenv("POCKETHOST_TOKEN", "ph_pat_x")
	if tok, src := EnvToken(PocketHostRemote); tok != "ph_pat_x" || src != "POCKETHOST_TOKEN" {
		t.Fatal(tok, src)
	}
	t.Setenv("POCKETBASE_TOKEN", "general")
	t.Setenv("POCKETBASE_TOKEN_MY_PROD", "specific")
	if tok, _ := EnvToken("my-prod"); tok != "specific" {
		t.Fatal(tok)
	}
	if tok, _ := EnvToken("other"); tok != "general" {
		t.Fatal(tok)
	}
	c, err := Open(dir, "prod")
	if err != nil || c.Token != "general" {
		t.Fatalf("open: %v", err)
	}
}

func TestCredentialsFile(t *testing.T) {
	isolate(t)
	c, _ := LoadCredentials()
	c.ByURL["https://x.test"] = &Credential{Token: "pbt_x", Via: "token"}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(c.Path())
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("perm %v %v", st.Mode(), err)
	}
	c2, _ := LoadCredentials()
	if c2.ByURL["https://x.test"].Token != "pbt_x" {
		t.Fatal("not saved")
	}
}

func TestWhoamiAndPasswordLoginMintsToken(t *testing.T) {
	dir := isolate(t)
	_, url := newApp(t, true)
	cl := &Client{Remote: &Remote{Name: "local", URL: url}, HTTP: NewHTTPClient()}
	ctx := context.Background()

	w, err := cl.Whoami(ctx)
	if err != nil || w.Auth != nil || w.Features["deploy"] != "/_pocker/deploy/v1" || w.Features["tokens"] != "/api/tokens" {
		t.Fatalf("anon whoami %+v %v", w, err)
	}
	if _, err := LoginWithPassword(ctx, cl, PasswordLogin{Email: "test@example.com", Password: "wrong"}); err == nil {
		t.Fatal("wrong password accepted")
	}
	res, err := LoginWithPassword(ctx, cl, PasswordLogin{Email: "test@example.com", Password: "1234567890", TokenName: "test cli"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Minted || !strings.HasPrefix(res.Credential.Token, "pbt_") || res.Warning != "" {
		t.Fatalf("%+v", res)
	}
	if err := SaveLogin(cl, res); err != nil {
		t.Fatal(err)
	}

	// a project remote pointing at it now opens with the saved login
	p, _ := LoadProject(dir)
	p.Add("local", url)
	p.Save()
	authed, err := Open(dir, "local")
	if err != nil {
		t.Fatal(err)
	}
	w, err = authed.Whoami(ctx)
	if err != nil || w.Auth == nil || w.Auth.Email != "test@example.com" || w.Via != "token" || w.TokenId != res.TokenId {
		t.Fatalf("authed whoami %+v %v", w, err)
	}

	// login with that token again (paste)
	res2, err := LoginWithToken(ctx, cl, res.Credential.Token)
	if err != nil || res2.Credential.Email != "test@example.com" {
		t.Fatalf("token login %+v %v", res2, err)
	}
	if _, err := LoginWithToken(ctx, cl, "pbt_"+strings.Repeat("z", 40)); err == nil {
		t.Fatal("bad token accepted")
	}

	// logout revokes the minted token
	out, err := runCmd(t, dir, "logout", "local")
	if err != nil || !strings.Contains(out, "Logged out") {
		t.Fatalf("logout: %v %s", err, out)
	}
	w, _ = cl.Whoami(ctx)
	probe := *cl
	probe.Token = res.Credential.Token
	if w, _ = probe.Whoami(ctx); w.Auth != nil {
		t.Fatal("minted token still works after logout")
	}
}

func TestPasswordLoginNeedsTokens(t *testing.T) {
	isolate(t)
	_, url := newApp(t, false)
	cl := &Client{Remote: &Remote{Name: "local", URL: url}, HTTP: NewHTTPClient()}
	_, err := LoginWithPassword(context.Background(), cl, PasswordLogin{Email: "test@example.com", Password: "1234567890"})
	if !errors.Is(err, ErrNoTokens) {
		t.Fatalf("got %v", err)
	}
}

func TestNoWhoamiRemoteTakesTokenAsIs(t *testing.T) {
	isolate(t)
	srv := httptest.NewServer(http.NotFoundHandler()) // e.g. a PocketHost edge today
	defer srv.Close()
	cl := &Client{Remote: &Remote{Name: "ph", URL: srv.URL}, HTTP: NewHTTPClient()}
	res, err := LoginWithToken(context.Background(), cl, "ph_pat_abc")
	if err != nil || res.Credential.Token != "ph_pat_abc" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestCommands(t *testing.T) {
	dir := isolate(t)
	_, url := newApp(t, true)
	if out, err := runCmd(t, dir, "remote", "add", "prod", url); err != nil || !strings.Contains(out, "Added prod") {
		t.Fatalf("%v %s", err, out)
	}
	t.Setenv("POCKETBASE_PASSWORD", "1234567890")
	if out, err := runCmd(t, dir, "login", "prod", "--email", "test@example.com"); err != nil || !strings.Contains(out, "Logged in") {
		t.Fatalf("login: %v %s", err, out)
	}
	out, err := runCmd(t, dir, "remote", "list")
	if err != nil || !strings.Contains(out, "test@example.com (token)") || !strings.Contains(out, "*") {
		t.Fatalf("list: %v %s", err, out)
	}
	if out, err = runCmd(t, dir, "remote", "whoami"); err != nil || !strings.Contains(out, "test@example.com in _superusers, via token") {
		t.Fatalf("whoami: %v %s", err, out)
	}
	if out, err = runCmd(t, dir, "link", url, "--name", "two", "--token", "pbt_"+strings.Repeat("z", 40)); err == nil {
		t.Fatalf("link with a bad token: %s", out)
	}
	var proj Project
	b, _ := os.ReadFile(filepath.Join(dir, ProjectDir, ProjectFile))
	json.Unmarshal(b, &proj)
	if proj.Remotes["two"] == nil || proj.Default != "prod" {
		t.Fatalf("link kept the remote, default unchanged: %s", b)
	}
}

func runCmd(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	var out bytes.Buffer
	for _, c := range Commands() {
		if c.Name() != args[0] {
			continue
		}
		c.SetOut(&out)
		c.SetErr(&out)
		c.SetIn(strings.NewReader(""))
		c.SetArgs(args[1:])
		c.SetContext(context.Background())
		err := c.Execute()
		return out.String(), err
	}
	t.Fatalf("no command %s", args[0])
	return "", nil
}
