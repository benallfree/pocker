package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pockethost/pocketone/ext/pocker"
	"github.com/pockethost/pocketone/ext/s3redirect"
	"github.com/pockethost/pocketone/ext/s3redirect/pockerplugin"
)

// With ext/s3redirect on the same server, downloads of default-bucket files become signed redirects to the
// instance's folder, without any wiring between the two plugins, and the signed URL serves the bytes.
func TestS3RedirectSeesDefaultBucket(t *testing.T) {
	b := startMinio(t)
	st := New(Config{Bucket: b, Logger: quiet})
	rd := pockerplugin.New(s3redirect.Config{Logger: quiet})
	var appA core.App
	m := pocker.NewManager(fixedSource{{ID: "a", Hosts: []string{"a.test"}}}, pocker.ManagerOptions{
		PBRoot: t.TempDir(), Logger: quiet,
		Init: func(inst pocker.Instance, app core.App) error {
			if err := st.InitApp(inst, app); err != nil {
				return err
			}
			return rd.InitApp(inst, app)
		},
		Setup: func(_ pocker.Instance, app core.App) error { appA = app; return nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	defer m.Shutdown()
	for !m.Ready() {
		time.Sleep(10 * time.Millisecond)
	}
	if err := m.StartInstance(ctx, "a"); err != nil {
		t.Fatal(err)
	}

	col := core.NewBaseCollection("docs")
	col.ViewRule = new(string)
	col.Fields.Add(&core.FileField{Name: "img", MaxSelect: 1, MaxSize: 1 << 20, Thumbs: []string{"10x10"}})
	if err := appA.Save(col); err != nil {
		t.Fatal(err)
	}
	want := pngBytes(t)
	f, err := filesystem.NewFileFromBytes(want, "pic.png")
	if err != nil {
		t.Fatal(err)
	}
	rec := core.NewRecord(col)
	rec.Set("img", f)
	if err := appA.Save(rec); err != nil {
		t.Fatal(err)
	}
	name := rec.GetString("img")

	client := &http.Client{Transport: &http.Transport{}, Timeout: 5 * time.Second}
	for _, q := range []string{"", "?thumb=10x10"} {
		w := httptest.NewRecorder()
		m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://a.test/api/files/docs/"+rec.Id+"/"+name+q, nil))
		if w.Code != http.StatusFound {
			t.Fatalf("%q: status %d, want a redirect", q, w.Code)
		}
		loc, err := url.Parse(w.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		wantPrefix := "/shared/eu/instances/a/storage/" + col.Id + "/" + rec.Id + "/"
		if !strings.HasPrefix(loc.Path, wantPrefix) {
			t.Fatalf("%q: redirect path %s, want under %s", q, loc.Path, wantPrefix)
		}
		res, err := client.Get(loc.String())
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || len(body) == 0 {
			t.Fatalf("%q: signed URL: %d %s", q, res.StatusCode, body)
		}
		if q == "" && !bytes.Equal(body, want) {
			t.Fatal("signed URL served different bytes")
		}
	}
}
