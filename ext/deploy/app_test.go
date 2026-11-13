package deploy

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
	"github.com/pockethost/pocketone/ext/remotes"
	"github.com/pockethost/pocketone/ext/tokens"
)

// serveApp runs app's router (OnServe included) on a local test server.
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

type ceRemote struct {
	app      *tests.TestApp
	url      string
	dir      string
	restarts atomic.Int32
	tk       *tokens.Tokens
}

func newCERemote(t *testing.T) *ceRemote {
	t.Helper()
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Cleanup)
	r := &ceRemote{app: app, dir: t.TempDir()}
	r.tk = tokens.MustRegister(app, tokens.Config{Collections: []string{"_superusers", "users"}})
	remotes.MustRegister(app, remotes.Config{})
	MustRegister(app, Config{Dir: r.dir, Restart: func(core.App) error { r.restarts.Add(1); return nil }})
	// the test app is already bootstrapped: run what OnBootstrap would
	if err := app.OnBootstrap().Trigger(&core.BootstrapEvent{App: app}, func(e *core.BootstrapEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := tokens.EnsureSchema(app); err != nil {
		t.Fatal(err)
	}
	r.url = serveApp(t, app)
	return r
}

func (r *ceRemote) token(t *testing.T, collection, email string, scopes ...string) string {
	t.Helper()
	owner, err := r.app.FindAuthRecordByEmail(collection, email)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := r.tk.Create(owner, tokens.CreateOptions{Name: "ci", Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func (r *ceRemote) client(t *testing.T, token string) *Client {
	t.Helper()
	rc := &remotes.Client{Remote: &remotes.Remote{Name: "ce", URL: r.url}, Token: token, TokenSource: "test", HTTP: remotes.NewHTTPClient()}
	c, err := NewClient(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCEToCE(t *testing.T) {
	r := newCERemote(t)
	ctx := context.Background()
	c := r.client(t, r.token(t, "_superusers", "test@example.com", ScopeDeploy))
	if c.Prefix != PockerPrefix {
		t.Fatalf("discovered %q", c.Prefix)
	}
	if w, err := c.DeployWhoami(ctx); err != nil || w.Via != "token" {
		t.Fatalf("whoami %+v %v", w, err)
	}

	project := t.TempDir()
	put := func(rel, content string) {
		p := filepath.Join(project, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	put("pb_hooks/main.pb.js", "routerAdd()")
	put("pb_public/index.html", "v1")
	put("pb_data/data.db", "local")
	deploy := func() *CommitResult {
		t.Helper()
		res, err := Deploy(ctx, c, DeployOptions{CollectOptions: CollectOptions{Dir: project}, Commit: "abc", Message: "test"})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := deploy()
	if res.WrittenCount != 2 || !res.Restarted || r.restarts.Load() != 1 {
		t.Fatalf("first %+v restarts %d", res, r.restarts.Load())
	}
	if b, _ := os.ReadFile(filepath.Join(r.dir, "pb_hooks/main.pb.js")); string(b) != "routerAdd()" {
		t.Fatal("hook not written")
	}
	if _, err := os.Stat(filepath.Join(r.dir, "pb_data/data.db")); err == nil {
		t.Fatal("pb_data shipped")
	}
	put("pb_public/index.html", "v2")
	if res = deploy(); res.WrittenCount != 1 || res.Restarted || r.restarts.Load() != 1 {
		t.Fatalf("pb_public-only deploy restarted: %+v", res)
	}
	if !deploy().NothingChanged {
		t.Fatal("no-op")
	}
	recs, err := r.app.FindRecordsByFilter(DeploysCollection, "status = 'success'", "-created", 0, 0)
	if err != nil || len(recs) != 2 || recs[0].GetString("commit") != "abc" || recs[0].GetString("token_label") != "ci" {
		t.Fatalf("history %d %v", len(recs), err)
	}

	// read-only token: pull and logs, no deploys
	ro := r.client(t, r.token(t, "_superusers", "test@example.com", ScopeRead))
	if _, err := ro.Check(ctx, map[string]string{}, false); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only check: %v", err)
	}
	pullDir := t.TempDir()
	if n, err := Pull(ctx, ro, PullOptions{Dir: pullDir}); err != nil || n != 2 {
		t.Fatalf("pull %d %v", n, err)
	}
	if b, _ := os.ReadFile(filepath.Join(pullDir, "pb_public/index.html")); string(b) != "v2" {
		t.Fatal("pulled content")
	}

	// logs come from the app's _logs
	l := &core.Log{Message: "hello from the app", Level: 0}
	l.Data = map[string]any{"k": "v"}
	l.Id = core.GenerateDefaultRandomId()
	l.Created = types.NowDateTime()
	if err := r.app.AuxSave(l); err != nil {
		t.Fatal(err)
	}
	lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var got []string
	ro.StreamLogs(lctx, 5, func(e LogEntry) {
		if strings.Contains(e.Message, "hello from the app") {
			got = append(got, e.Message)
			cancel()
		}
	})
	if len(got) == 0 {
		t.Fatal("no app log line")
	}

	// who may not deploy
	user := r.client(t, r.token(t, "users", "test@example.com"))
	if _, err := user.DeployWhoami(ctx); err == nil || !strings.Contains(err.Error(), "Only superusers") {
		t.Fatalf("user token: %v", err)
	}
	anon := r.client(t, "")
	if _, err := anon.DeployWhoami(ctx); err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("anonymous: %v", err)
	}
	other := r.token(t, "_superusers", "test@example.com")
	if w, err := r.client(t, other).DeployWhoami(ctx); err != nil || w.Via != "token" {
		t.Fatalf("unscoped superuser token: %v", err)
	}
}

func TestCEToCEWithImpersonateLogin(t *testing.T) {
	r := newCERemote(t)
	rc := &remotes.Client{Remote: &remotes.Remote{Name: "ce", URL: r.url}, HTTP: remotes.NewHTTPClient()}
	su, _ := r.app.FindAuthRecordByEmail(core.CollectionNameSuperusers, "test@example.com")
	jwt, err := su.NewStaticAuthToken(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	rc.Token = jwt
	c, err := NewClient(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if w, err := c.DeployWhoami(context.Background()); err != nil || w.Via != "jwt" {
		t.Fatalf("%+v %v", w, err)
	}
}

// The standard binary serves pb_public at "GET /{path...}"; the deploy routes must not conflict with it.
func TestRoutesCoexistWithStaticCatchAll(t *testing.T) {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	MustRegister(app, Config{Dir: t.TempDir(), Restart: func(core.App) error { return nil }})
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/{path...}", func(e *core.RequestEvent) error { return e.String(200, "static") })
		return se.Next()
	})
	router, _ := apis.NewRouter(app)
	err = app.OnServe().Trigger(&core.ServeEvent{App: app, Router: router}, func(e *core.ServeEvent) error {
		_, err := e.Router.BuildMux()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
