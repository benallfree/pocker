package s3redirect

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem"
)

func TestResponseHeaders(t *testing.T) {
	cases := []struct {
		url, path, name, disp, ct string
	}{
		{"/f", "c/r/photo.png", "photo.png", `inline; filename="photo.png"`, ""},
		{"/f?download=1", "c/r/photo.png", "photo.png", `attachment; filename="photo.png"`, ""},
		{"/f", "c/r/doc.pdf", "doc.pdf", `inline; filename="doc.pdf"`, ""},
		{"/f", "c/r/page.html", "page.html", `attachment; filename="page.html"`, ""},
		{"/f", "c/r/logo.svg", "logo.svg", `attachment; filename="logo.svg"`, "image/svg+xml"},
		{"/f", "c/r/thumbs_p.png/100x100_p.png", "100x100_p.png", `inline; filename="100x100_p.png"`, ""},
	}
	for _, c := range cases {
		r, _ := http.NewRequest(http.MethodGet, c.url, nil)
		d, ct := responseHeaders(r, c.path, c.name)
		if d != c.disp || ct != c.ct {
			t.Errorf("%s %s: got %q %q, want %q %q", c.url, c.path, d, ct, c.disp, c.ct)
		}
	}
}

// fileApp is a test app with a "docs" collection (public file field "file", protected "secret") and one record that
// references files by name (nothing is uploaded: the redirect never touches the bucket).
func fileApp(t testing.TB, s3On bool, cfg Config, ac AppConfig) *tests.TestApp {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	col := core.NewBaseCollection("docs")
	col.ViewRule = ptr(`@request.auth.id != ""`) // records need a login; the public field ignores it, the protected one needs a file token
	col.Fields.Add(&core.FileField{Name: "file", MaxSelect: 1, MaxSize: 1 << 20})
	col.Fields.Add(&core.FileField{Name: "secret", MaxSelect: 1, MaxSize: 1 << 20, Protected: true})
	if err := app.Save(col); err != nil {
		t.Fatal(err)
	}
	rec := core.NewRecord(col)
	rec.Id = "rec000000000001"
	rec.Set("file", "photo_abc.png")
	rec.Set("secret", "plan_abc.pdf")
	if err := app.SaveNoValidate(rec); err != nil {
		t.Fatal(err)
	}
	if s3On {
		s := app.Settings()
		s.S3 = core.S3Config{Enabled: true, Endpoint: "https://files.example.com", Bucket: "bkt", Region: "auto", AccessKey: "AK", Secret: "SK"}
		if err := app.Save(s); err != nil {
			t.Fatal(err)
		}
	}
	cfg.now = func() time.Time { return time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC) }
	if err := New(cfg).SetupApp(app, ac); err != nil {
		t.Fatal(err)
	}
	return app
}

func ptr[T any](v T) *T { return &v }

func TestDownloadRedirects(t *testing.T) {
	colID := func(app core.App) string {
		c, _ := app.FindCollectionByNameOrId("docs")
		return c.Id
	}
	expectRedirect := func(wantKey, wantDisp string) func(testing.TB, *tests.TestApp, *http.Response) {
		return func(t testing.TB, app *tests.TestApp, res *http.Response) {
			loc, err := url.Parse(res.Header.Get("Location"))
			if err != nil || loc.Host != "bkt.files.example.com" {
				t.Fatalf("bad Location %q", res.Header.Get("Location"))
			}
			if want := "/" + colID(app) + "/rec000000000001/" + wantKey; loc.Path != want {
				t.Fatalf("path %s, want %s", loc.Path, want)
			}
			q := loc.Query()
			if q.Get("X-Amz-Expires") != "300" || q.Get("X-Amz-Signature") == "" {
				t.Fatalf("not signed for 5m: %v", q)
			}
			if q.Get("response-content-disposition") != wantDisp {
				t.Fatalf("disposition %q, want %q", q.Get("response-content-disposition"), wantDisp)
			}
			if cc := res.Header.Get("Cache-Control"); cc != "private, max-age=240" {
				t.Fatalf("Cache-Control %q", cc)
			}
		}
	}

	scenarios := []tests.ApiScenario{
		{
			Name:           "public file redirects",
			Method:         http.MethodGet,
			URL:            "/api/files/docs/rec000000000001/photo_abc.png",
			TestAppFactory: func(t testing.TB) *tests.TestApp { return fileApp(t, true, Config{}, AppConfig{}) },
			ExpectedStatus: http.StatusFound,
			ExpectedEvents: map[string]int{"OnFileDownloadRequest": 1},
			AfterTestFunc:  expectRedirect("photo_abc.png", `inline; filename="photo_abc.png"`),
		},
		{
			Name:           "download=1 forces attachment",
			Method:         http.MethodGet,
			URL:            "/api/files/docs/rec000000000001/photo_abc.png?download=1",
			TestAppFactory: func(t testing.TB) *tests.TestApp { return fileApp(t, true, Config{}, AppConfig{}) },
			ExpectedStatus: http.StatusFound,
			ExpectedEvents: map[string]int{"OnFileDownloadRequest": 1},
			AfterTestFunc:  expectRedirect("photo_abc.png", `attachment; filename="photo_abc.png"`),
		},
		{
			Name:            "protected file without token is refused before any redirect",
			Method:          http.MethodGet,
			URL:             "/api/files/docs/rec000000000001/plan_abc.pdf",
			TestAppFactory:  func(t testing.TB) *tests.TestApp { return fileApp(t, true, Config{}, AppConfig{}) },
			ExpectedStatus:  http.StatusNotFound,
			ExpectedContent: []string{`"data":{}`},
		},
		{
			Name:            "S3 off streams as usual (local file missing: 404, no redirect)",
			Method:          http.MethodGet,
			URL:             "/api/files/docs/rec000000000001/photo_abc.png",
			TestAppFactory:  func(t testing.TB) *tests.TestApp { return fileApp(t, false, Config{}, AppConfig{}) },
			ExpectedStatus:  http.StatusNotFound,
			ExpectedEvents:  map[string]int{"OnFileDownloadRequest": 1},
			ExpectedContent: []string{`"data":{}`},
		},
		{
			Name:            "disabled for the app streams as usual",
			Method:          http.MethodGet,
			URL:             "/api/files/docs/rec000000000001/photo_abc.png",
			TestAppFactory:  func(t testing.TB) *tests.TestApp { return fileApp(t, true, Config{}, AppConfig{Enabled: ptr(false)}) },
			ExpectedStatus:  http.StatusNotFound, // S3 endpoint is fake, so streaming fails
			ExpectedEvents:  map[string]int{"OnFileDownloadRequest": 1},
			ExpectedContent: []string{`"data":{}`},
		},
		{
			Name:   "a filesystem supplied through OnFilesystemCreate is signed for like the app's own S3 (ext/storage)",
			Method: http.MethodGet,
			URL:    "/api/files/docs/rec000000000001/photo_abc.png",
			TestAppFactory: func(t testing.TB) *tests.TestApp {
				app := fileApp(t, false, Config{}, AppConfig{})
				app.OnFilesystemCreate().BindFunc(func(e *core.FilesystemCreateEvent) error {
					if e.Kind == core.FilesystemKindStorage {
						fsys, err := filesystem.NewS3("other", "auto", "https://files.example.com", "a", "s", false)
						if err != nil {
							return err
						}
						e.Filesystem = fsys
					}
					return e.Next()
				})
				return app
			},
			ExpectedStatus: http.StatusFound,
			ExpectedEvents: map[string]int{"OnFileDownloadRequest": 1},
			AfterTestFunc: func(t testing.TB, app *tests.TestApp, res *http.Response) {
				loc, _ := url.Parse(res.Header.Get("Location"))
				if loc.Host != "other.files.example.com" || loc.Path != "/"+colID(app)+"/rec000000000001/photo_abc.png" {
					t.Fatalf("Location %s", loc)
				}
			},
		},
	}
	for _, s := range scenarios {
		s.Test(t)
	}
}

func TestProtectedFileWithTokenRedirects(t *testing.T) {
	app := fileApp(t, true, Config{TTL: time.Minute}, AppConfig{})
	defer app.Cleanup()
	su, err := app.FindAuthRecordByEmail(core.CollectionNameSuperusers, "test@example.com")
	if err != nil {
		t.Fatal(err)
	}
	token, err := su.NewFileToken()
	if err != nil {
		t.Fatal(err)
	}
	(&tests.ApiScenario{
		Name:                  "protected file with a file token redirects",
		Method:                http.MethodGet,
		URL:                   "/api/files/docs/rec000000000001/plan_abc.pdf?token=" + token,
		TestAppFactory:        func(testing.TB) *tests.TestApp { return app },
		DisableTestAppCleanup: true,
		ExpectedStatus:        http.StatusFound,
		ExpectedEvents:        map[string]int{"*": 0, "OnFileDownloadRequest": 1},
		AfterTestFunc: func(t testing.TB, _ *tests.TestApp, res *http.Response) {
			loc, _ := url.Parse(res.Header.Get("Location"))
			q := loc.Query()
			if q.Get("X-Amz-Expires") != "60" || q.Get("response-content-disposition") != `inline; filename="plan_abc.pdf"` {
				t.Fatalf("unexpected %v", q)
			}
			if strings.Contains(loc.RawQuery, token) {
				t.Fatal("file token leaked into the bucket URL")
			}
			if res.Header.Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("missing Referrer-Policy")
			}
		},
	}).Test(t)
}
