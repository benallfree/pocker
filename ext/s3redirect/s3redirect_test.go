package s3redirect

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"github.com/pockethost/pocketbase/ext/pocker"
)

// AWS's documented presigned URL example (IAM user guide, "Authenticating Requests: Using Query Parameters").
func TestPresignGetAWSExample(t *testing.T) {
	tg := &Target{
		Endpoint:  "s3.amazonaws.com",
		Bucket:    "examplebucket",
		Region:    "us-east-1",
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		Secret:    "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	got, err := tg.PresignGet("test.txt", 86400*time.Second, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://examplebucket.s3.amazonaws.com/test.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256" +
		"&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request" +
		"&X-Amz-Date=20130524T000000Z&X-Amz-Expires=86400&X-Amz-SignedHeaders=host" +
		"&X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestPresignGetPathStyleAndPrefix(t *testing.T) {
	tg := &Target{Endpoint: "http://127.0.0.1:9000/", Bucket: "b", AccessKey: "k", Secret: "s", ForcePathStyle: true, Prefix: "instances/abc/storage/"}
	got, err := tg.PresignGet("col/rec/a b+c.png", time.Minute, time.Unix(0, 0), url.Values{"response-content-disposition": {`inline; filename="a b+c.png"`}})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "http" || u.Host != "127.0.0.1:9000" || u.EscapedPath() != "/b/instances/abc/storage/col/rec/a%20b%2Bc.png" {
		t.Fatalf("unexpected URL %s (path %s)", got, u.EscapedPath())
	}
	q := u.Query()
	if q.Get("X-Amz-Expires") != "60" || !strings.Contains(q.Get("X-Amz-Credential"), "/us-east-1/s3/aws4_request") {
		t.Fatalf("unexpected query %v", q)
	}
	if q.Get("response-content-disposition") != `inline; filename="a b+c.png"` {
		t.Fatalf("override not carried: %v", q)
	}
}

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
			Name:   "Target overrides the app's own S3 (ext/storage)",
			Method: http.MethodGet,
			URL:    "/api/files/docs/rec000000000001/photo_abc.png",
			TestAppFactory: func(t testing.TB) *tests.TestApp {
				return fileApp(t, false, Config{Target: func(core.App) *Target {
					return &Target{Endpoint: "files.example.com", Bucket: "bkt", AccessKey: "a", Secret: "s", Prefix: "instances/x/storage/"}
				}}, AppConfig{})
			},
			ExpectedStatus: http.StatusFound,
			ExpectedEvents: map[string]int{"OnFileDownloadRequest": 1},
			AfterTestFunc: func(t testing.TB, app *tests.TestApp, res *http.Response) {
				loc, _ := url.Parse(res.Header.Get("Location"))
				if want := "/instances/x/storage/" + colID(app) + "/rec000000000001/photo_abc.png"; loc.Path != want {
					t.Fatalf("path %s, want %s", loc.Path, want)
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

func TestInstanceConfig(t *testing.T) {
	ac, err := instanceConfig(pocker.Instance{ID: "a", Plugins: map[string]any{Name: map[string]any{"enabled": false, "ttl": "2m"}}})
	if err != nil {
		t.Fatal(err)
	}
	if ac.Enabled == nil || *ac.Enabled || ac.TTL != 2*time.Minute {
		t.Fatalf("got %+v", ac)
	}
	if _, err := instanceConfig(pocker.Instance{ID: "a", Plugins: map[string]any{Name: map[string]any{"target": "x"}}}); err == nil {
		t.Fatal("per-instance target must be refused")
	}
}
