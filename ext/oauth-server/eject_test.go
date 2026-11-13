package oauthserver

import (
	"bytes"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"github.com/pockethost/pocketone/ext/pages"
	"github.com/pockethost/pocketone/ext/remotes"
	"github.com/pockethost/pocketone/ext/tokens"
)

func TestEjectRendersThroughPages(t *testing.T) {
	pollInterval = 1
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	pagesDir := filepath.Join(t.TempDir(), "pb_pages")
	jsvm.MustRegister(app, jsvm.Config{HooksDir: t.TempDir()})
	pages.MustRegister(app, pages.Config{Dir: pagesDir})
	tokens.MustRegister(app, tokens.Config{})
	remotes.MustRegister(app, remotes.Config{})
	srv := MustRegister(app, Config{Branding: Branding{Name: "Acme Cloud"}})
	if err := app.OnBootstrap().Trigger(&core.BootstrapEvent{App: app}, func(*core.BootstrapEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	tokens.EnsureSchema(app)

	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		cmd := NewCommand(srv)
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), err
	}
	out, err := run("eject")
	if err != nil || strings.Count(out, "wrote ") != 9 || !strings.Contains(out, filepath.Join(pagesDir, "_private", "oauth", "signin.ejs")) {
		t.Fatalf("eject: %v\n%s", err, out)
	}
	if _, err := run("eject"); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("second eject: %v", err)
	}
	var ee *ExistsError
	if _, err := Eject(pagesDir, false); !errors.As(err, &ee) || len(ee.Files) != 9 {
		t.Fatalf("exists: %v", err)
	}
	// customize the ejected sign-in page; --force restores it
	signin := filepath.Join(pagesDir, "_private", "oauth", "signin.ejs")
	b, _ := os.ReadFile(signin)
	os.WriteFile(signin, []byte(strings.Replace(string(b), "<h1>Sign in to", "<h1>EJECTED Sign in to", 1)), 0o644)

	base := serveApp(t, app)
	_, challenge := pkce()
	q := url.Values{"response_type": {"code"}, "client_id": {CLIClientId}, "redirect_uri": {"http://127.0.0.1:9/cb"}, "state": {"s"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	br := newBrowser(t)
	br.get(base + "/oauth/authorize?" + q.Encode())
	if br.status != 200 || !br.has("EJECTED Sign in to Acme Cloud") || !br.has(`name="csrf"`) {
		t.Fatalf("ejected sign-in: %d %s", br.status, br.page)
	}
	br.submit("password", map[string]string{"email": "test@example.com", "password": "wrong"})
	if br.status != 401 || !br.has("Wrong email or password") {
		t.Fatalf("ejected error status: %d %s", br.status, br.page)
	}
	br.submit("password", map[string]string{"email": "test@example.com", "password": "1234567890"})
	if !br.has("wants to access Acme Cloud") || !br.has("Full access") {
		t.Fatalf("ejected consent: %s", br.page)
	}
	res := br.submit("allow", nil)
	if loc := res.Header.Get("Location"); res.StatusCode != 302 || !strings.Contains(loc, "code=") || !strings.Contains(loc, "state=s") {
		t.Fatalf("allow through ejected consent: %d %s", res.StatusCode, loc)
	}

	if out, err := run("eject", "--force"); err != nil || !strings.Contains(out, "Ejected 9") {
		t.Fatalf("force: %v %s", err, out)
	}
	if b, _ := os.ReadFile(signin); strings.Contains(string(b), "EJECTED") {
		t.Fatal("--force didn't overwrite")
	}
	// deleting the templates goes back to the built-in pages
	os.RemoveAll(filepath.Join(pagesDir, "_private"))
	br2 := newBrowser(t)
	br2.get(base + "/oauth/device/verify")
	if !br2.has("Connect a device") || br2.has("ejected") {
		t.Fatalf("built-in after delete: %s", br2.page)
	}
}

func TestEjectWithoutPages(t *testing.T) {
	e := newEnv(t, Config{})
	cmd := NewCommand(e.srv)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"eject"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "no ext/pages") {
		t.Fatalf("%v", err)
	}
}
