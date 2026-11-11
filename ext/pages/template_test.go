package pages

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
)

func TestRenderTemplate(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "_private", "oauth"), 0o755)
	os.WriteFile(filepath.Join(dir, "index.ejs"), []byte("home"), 0o644)
	os.WriteFile(filepath.Join(dir, "_private", "oauth", "signin.ejs"),
		[]byte(`<h1><%= data.appName %></h1><%- include('/oauth/part', { x: data.csrf }) %>`), 0o644)
	os.WriteFile(filepath.Join(dir, "_private", "oauth", "part.ejs"), []byte(`<i><%= x %></i>`), 0o644)

	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	jsvm.MustRegister(app, jsvm.Config{HooksDir: t.TempDir()})
	p := MustRegister(app, Config{Dir: dir})
	if err := app.OnBootstrap().Trigger(&core.BootstrapEvent{App: app}, func(*core.BootstrapEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	p.refresh(true)

	ev := func(method string) (*core.RequestEvent, *httptest.ResponseRecorder) {
		rec := httptest.NewRecorder()
		e := &core.RequestEvent{App: app}
		e.Request = httptest.NewRequest(method, "/oauth/authorize", nil)
		e.Response = rec
		return e, rec
	}
	e, rec := ev(http.MethodPost)
	handled, err := p.RenderTemplate(e, "oauth/signin", 401, map[string]any{"appName": "Acme <Cloud>", "csrf": "tok"})
	if err != nil || !handled {
		t.Fatalf("%v %v", handled, err)
	}
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), "<h1>Acme &lt;Cloud&gt;</h1><i>tok</i>") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	e, rec = ev(http.MethodGet)
	if handled, _ := p.RenderTemplate(e, "oauth/consent", 0, nil); handled || rec.Body.Len() != 0 {
		t.Fatal("missing template handled")
	}
	if handled, _ := p.RenderTemplate(e, "../index", 0, nil); handled {
		t.Fatal("escaped _private")
	}
}
