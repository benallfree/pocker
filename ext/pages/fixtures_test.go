package pages

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
)

// rendering is false until the JS pass exists; cases that need it are skipped.
const rendering = true

type fixtureCase struct {
	Name    string `json:"name"`
	Needs   string `json:"needs"`
	Request struct {
		Method  string            `json:"method"`
		Path    string            `json:"path"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	} `json:"request"`
	Expect struct {
		Status            int               `json:"status"`
		Headers           map[string]string `json:"headers"`
		HeaderContains    map[string]string `json:"headerContains"`
		HeaderAbsent      []string          `json:"headerAbsent"`
		BodyContains      []string          `json:"bodyContains"`
		BodyNotContains   []string          `json:"bodyNotContains"`
		BodyJSON          any               `json:"bodyJSON"`
		BodyEmpty         bool              `json:"bodyEmpty"`
		SetCookieContains []string          `json:"setCookieContains"`
	} `json:"expect"`
}

// newServer builds the app's router the way `serve` does and returns its handler.
func newServer(t *testing.T, dir string) (http.Handler, *Pages) {
	t.Helper()
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Cleanup)
	jsvm.MustRegister(app, jsvm.Config{HooksDir: filepath.Join(t.TempDir(), "pb_hooks")})
	p, err := Register(app, Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	se := &core.ServeEvent{App: app, Router: router}
	var mux http.Handler
	err = app.OnServe().Trigger(se, func(e *core.ServeEvent) error {
		m, err := e.Router.BuildMux()
		mux = m
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return mux, p
}

func TestFixtures(t *testing.T) {
	dirs, _ := filepath.Glob("fixtures/*/cases.json")
	if len(dirs) == 0 {
		t.Fatal("no fixtures")
	}
	for _, casesFile := range dirs {
		fixture := filepath.Dir(casesFile)
		t.Run(filepath.Base(fixture), func(t *testing.T) {
			raw, err := os.ReadFile(casesFile)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Cases []fixtureCase `json:"cases"`
			}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			h, p := newServer(t, filepath.Join(fixture, "pb_pages"))
			if errs := p.Errors(); len(errs) > 0 {
				t.Fatalf("fixture doesn't build: %v", errs)
			}
			for _, c := range doc.Cases {
				t.Run(c.Name, func(t *testing.T) {
					if c.Needs == "render" && !rendering {
						t.Skip("needs the JS pass")
					}
					runCase(t, h, c)
				})
			}
		})
	}
}

func runCase(t *testing.T, h http.Handler, c fixtureCase) {
	method := c.Request.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if c.Request.Body != "" {
		body = strings.NewReader(c.Request.Body)
	}
	req := httptest.NewRequest(method, c.Request.Path, body)
	for k, v := range c.Request.Headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	got, _ := io.ReadAll(res.Body)
	text := string(got)
	ex := c.Expect

	if ex.Status != 0 && res.StatusCode != ex.Status {
		t.Errorf("status %d, want %d; body:\n%s", res.StatusCode, ex.Status, text)
	}
	for k, v := range ex.Headers {
		if g := res.Header.Get(k); g != v {
			t.Errorf("header %s = %q, want %q", k, g, v)
		}
	}
	for k, v := range ex.HeaderContains {
		if _, ok := res.Header[http.CanonicalHeaderKey(k)]; !ok {
			t.Errorf("header %s missing", k)
		} else if !strings.Contains(res.Header.Get(k), v) {
			t.Errorf("header %s = %q, want it to contain %q", k, res.Header.Get(k), v)
		}
	}
	for _, k := range ex.HeaderAbsent {
		if res.Header.Get(k) != "" {
			t.Errorf("header %s should be absent, got %q", k, res.Header.Get(k))
		}
	}
	for _, s := range ex.BodyContains {
		if !strings.Contains(text, s) {
			t.Errorf("body missing %q; body:\n%s", s, text)
		}
	}
	for _, s := range ex.BodyNotContains {
		if strings.Contains(text, s) {
			t.Errorf("body shouldn't contain %q", s)
		}
	}
	if ex.BodyEmpty && len(got) != 0 {
		t.Errorf("body should be empty, got %q", text)
	}
	if ex.BodyJSON != nil {
		var v any
		if err := json.Unmarshal(got, &v); err != nil {
			t.Errorf("body isn't JSON: %v\n%s", err, text)
		} else if !reflect.DeepEqual(v, ex.BodyJSON) {
			t.Errorf("JSON body %v, want %v", v, ex.BodyJSON)
		}
	}
	cookies := res.Header.Values("Set-Cookie")
	for _, s := range ex.SetCookieContains {
		found := false
		for _, ck := range cookies {
			if strings.Contains(ck, s) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no Set-Cookie contains %q (got %q)", s, cookies)
		}
	}
}
