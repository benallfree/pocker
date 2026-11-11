package pitr

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	_ "ganigeorgiev.com/pocketbase/pocketbase/migrations"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// testApp is a single PocketBase app with the plugin, served in-process the way `pocketbase serve` builds its router.
type testApp struct {
	app     *core.BaseApp
	handler http.Handler
}

func startTestApp(t *testing.T, p *Plugin, dir string) *testApp {
	t.Helper()
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: dir})
	if err := p.SetupApp(app, AppConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	if err := app.RunAllMigrations(); err != nil {
		t.Fatal(err)
	}
	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	var handler http.Handler
	err = app.OnServe().Trigger(&core.ServeEvent{App: app, Router: router, Server: &http.Server{}}, func(e *core.ServeEvent) error {
		mux, err := e.Router.BuildMux()
		handler = mux
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return &testApp{app: app, handler: handler}
}

func (ta *testApp) stop() {
	_ = ta.app.OnTerminate().Trigger(&core.TerminateEvent{App: ta.app}, func(e *core.TerminateEvent) error {
		return e.App.ResetBootstrapState()
	})
}

func notesCollection(t *testing.T, app core.App) {
	t.Helper()
	col := core.NewBaseCollection("notes")
	col.Fields.Add(&core.TextField{Name: "title"})
	if err := app.Save(col); err != nil {
		t.Fatal(err)
	}
}

func addNotes(t *testing.T, app core.App, prefix string, n int) {
	t.Helper()
	col, err := app.FindCollectionByNameOrId("notes")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		rec := core.NewRecord(col)
		rec.Set("title", fmt.Sprintf("%s%03d", prefix, i))
		if err := app.Save(rec); err != nil {
			t.Fatal(err)
		}
	}
}

func countNotes(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM notes`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// mark waits past a second boundary and returns a moment strictly between the writes before and after it.
func mark() time.Time {
	time.Sleep(1100 * time.Millisecond)
	t := time.Now()
	time.Sleep(1100 * time.Millisecond)
	return t
}

func superuserToken(t *testing.T, app core.App) string {
	t.Helper()
	su := core.NewRecord(mustCollection(t, app, core.CollectionNameSuperusers))
	su.SetEmail("admin@example.com")
	su.SetPassword("1234567890")
	if err := app.Save(su); err != nil {
		t.Fatal(err)
	}
	tok, err := su.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func mustCollection(t *testing.T, app core.App, name string) *core.Collection {
	c, err := app.FindCollectionByNameOrId(name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func do(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSingleAppLifecycleAndStagedRestore(t *testing.T) {
	dir := t.TempDir()
	p := New(Config{Logger: quiet})
	defer p.Close(context.Background())

	ta := startTestApp(t, p, dir)
	notesCollection(t, ta.app)
	addNotes(t, ta.app, "a", 50)
	if err := p.storeSync(ta.app); err != nil {
		t.Fatal(err)
	}
	t1 := mark()
	if _, err := ta.app.DB().NewQuery(`DELETE FROM notes`).Execute(); err != nil {
		t.Fatal(err)
	}
	addNotes(t, ta.app, "b", 5)

	// nothing of the plugin lands next to data.db (it would end up in backup zips)
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.Contains(e.Name(), "litestream") && e.Name() != DirName {
			t.Errorf("unexpected %s in pb_data", e.Name())
		}
	}

	// routes: superuser only
	if rec := do(t, ta.handler, http.MethodGet, "/api/pitr", "", ""); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous status: %d", rec.Code)
	}
	tok := superuserToken(t, ta.app)
	rec := do(t, ta.handler, http.MethodGet, "/api/pitr", tok, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"window"`) {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, ta.handler, http.MethodPost, "/api/pitr/restore", tok, `{"at":"2000-01-01T00:00:00Z"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("out of window: %d %s", rec.Code, rec.Body)
	}

	// stage directly (the route would also restart the process, which a test can't)
	a := p.appFor(ta.app)
	if err := a.Stage(context.Background(), t1); err != nil {
		t.Fatal(err)
	}
	if n := countNotes(t, filepath.Join(dir, "data.db")); n != 5 {
		t.Fatalf("staging touched the live DB: %d notes", n)
	}

	// "restart": terminate (flush + detach), start again: the staged DB is swapped in before it opens
	ta.stop()
	ta = startTestApp(t, p, dir)
	defer ta.stop()
	if n, _ := ta.app.CountRecords("notes"); n != 50 {
		t.Fatalf("after restore: %d notes, want 50", n)
	}
	if n := countNotes(t, filepath.Join(dir, DirName, beforeName)); n != 5 {
		t.Fatalf("kept DB: %d notes, want 5", n)
	}
	last := readLastRestore(dir)
	if last == nil || last.Error != "" || !last.At.Equal(t1) {
		t.Fatalf("last restore: %+v", last)
	}

	// replication carries on; undo = restore to just before the restore
	addNotes(t, ta.app, "c", 3)
	if err := p.storeSync(ta.app); err != nil {
		t.Fatal(err)
	}
	side := filepath.Join(t.TempDir(), "latest.db")
	if err := restoreTo(context.Background(), p.appFor(ta.app), time.Now(), side); err != nil {
		t.Fatal(err)
	}
	if n := countNotes(t, side); n != 53 {
		t.Fatalf("latest: %d notes, want 53", n)
	}
	undo := filepath.Join(t.TempDir(), "undo.db")
	if err := restoreTo(context.Background(), p.appFor(ta.app), last.RestoredAt.Add(-time.Millisecond), undo); err != nil {
		t.Fatal(err)
	}
	if n := countNotes(t, undo); n != 5 {
		t.Fatalf("undo point: %d notes, want 5 (the pre-restore state)", n)
	}
}

func TestBackupZipLeavesHistoryOut(t *testing.T) {
	dir := t.TempDir()
	p := New(Config{Logger: quiet})
	defer p.Close(context.Background())
	ta := startTestApp(t, p, dir)
	defer ta.stop()
	notesCollection(t, ta.app)
	addNotes(t, ta.app, "a", 5)
	if err := p.storeSync(ta.app); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.CreateBackup(context.Background(), "b.zip"); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("unzip", "-l", filepath.Join(dir, core.LocalBackupsDirName, "b.zip")).CombinedOutput()
	if err != nil {
		t.Skipf("unzip: %v", err)
	}
	if strings.Contains(string(out), DirName) {
		t.Errorf("backup zip contains %s:\n%s", DirName, out)
	}
	if !strings.Contains(string(out), "data.db") {
		t.Errorf("backup zip has no data.db:\n%s", out)
	}
}

func TestDisabledAddsNothing(t *testing.T) {
	dir := t.TempDir()
	off := false
	p := New(Config{Enabled: &off, Logger: quiet})
	defer p.Close(context.Background())
	ta := startTestApp(t, p, dir)
	defer ta.stop()
	tok := superuserToken(t, ta.app)
	if rec := do(t, ta.handler, http.MethodGet, "/api/pitr", tok, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled: GET /api/pitr = %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, DirName)); err == nil {
		t.Error("disabled app got a history folder")
	}
}

// ---- bucket (MinIO) ----

func startMinio(t *testing.T) *Bucket {
	t.Helper()
	bin, err := exec.LookPath("minio")
	if err != nil {
		t.Skip("minio not installed")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	cmd := exec.Command(bin, "server", "--quiet", "--address", addr, "--console-address", "127.0.0.1:0", t.TempDir())
	cmd.Env = append(os.Environ(), "MINIO_ROOT_USER=pitrtest", "MINIO_ROOT_PASSWORD=pitrtest-secret")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	endpoint := "http://" + addr
	for i := 0; ; i++ {
		if resp, err := http.Get(endpoint + "/minio/health/ready"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if i > 100 {
			t.Fatal("minio did not start")
		}
		time.Sleep(100 * time.Millisecond)
	}
	// create the bucket with a signed PUT through PocketBase's own client is not exposed; use mc
	mc, err := exec.LookPath("mc")
	if err != nil {
		t.Skip("mc not installed")
	}
	cfgDir := t.TempDir()
	run := func(args ...string) {
		c := exec.Command(mc, append([]string{"--config-dir", cfgDir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("mc %v: %v\n%s", args, err, out)
		}
	}
	run("alias", "set", "t", endpoint, "pitrtest", "pitrtest-secret")
	run("mb", "t/pitr")
	return &Bucket{Endpoint: endpoint, Bucket: "pitr", Region: "us-east-1", AccessKey: "pitrtest", Secret: "pitrtest-secret", ForcePathStyle: true, Prefix: "eu/", Label: "EU"}
}

func TestBucketDestination(t *testing.T) {
	b := startMinio(t)
	p := New(Config{Bucket: b, Logger: quiet})
	defer p.Close(context.Background())

	// two apps with their own keys, as under a multitenant host
	dirA, dirB := t.TempDir(), t.TempDir()
	start := func(dir, key string) *testApp {
		app := core.NewBaseApp(core.BaseAppConfig{DataDir: dir})
		if err := p.SetupApp(app, AppConfig{Key: key}); err != nil {
			t.Fatal(err)
		}
		if err := app.Bootstrap(); err != nil {
			t.Fatal(err)
		}
		_ = app.RunAllMigrations()
		router, _ := apis.NewRouter(app)
		_ = app.OnServe().Trigger(&core.ServeEvent{App: app, Router: router, Server: &http.Server{}}, func(e *core.ServeEvent) error { return nil })
		return &testApp{app: app}
	}
	ta := start(dirA, "instances/a/")
	tb := start(dirB, "instances/b/")
	defer tb.stop()
	notesCollection(t, ta.app)
	notesCollection(t, tb.app)
	addNotes(t, ta.app, "a", 20)
	addNotes(t, tb.app, "b", 7)
	for _, x := range []*testApp{ta, tb} {
		if err := p.storeSync(x.app); err != nil {
			t.Fatal(err)
		}
	}
	t1 := mark()
	if _, err := ta.app.DB().NewQuery(`DELETE FROM notes`).Execute(); err != nil {
		t.Fatal(err)
	}
	if err := p.storeSync(ta.app); err != nil {
		t.Fatal(err)
	}

	// nothing local: history is in the bucket, under each app's own prefix
	if _, err := os.Stat(filepath.Join(dirA, DirName, "replica")); err == nil {
		t.Error("bucket mode wrote a local replica")
	}
	fsys, _ := p.bucketFS()
	objs, err := fsys.List("eu/instances/")
	if err != nil || len(objs) == 0 {
		t.Fatalf("bucket objects: %d %v", len(objs), err)
	}
	for _, o := range objs {
		if !strings.HasPrefix(o.Key, "eu/instances/a/litestream/") && !strings.HasPrefix(o.Key, "eu/instances/b/litestream/") {
			t.Errorf("key outside the app folders: %s", o.Key)
		}
	}

	// timestamp restore from the bucket (uses the stored timestamps)
	side := filepath.Join(t.TempDir(), "a.db")
	if err := restoreTo(context.Background(), p.appFor(ta.app), t1, side); err != nil {
		t.Fatal(err)
	}
	if n := countNotes(t, side); n != 20 {
		t.Fatalf("bucket restore to t1: %d notes, want 20", n)
	}
	sideB := filepath.Join(t.TempDir(), "b.db")
	if err := restoreTo(context.Background(), p.appFor(tb.app), time.Now(), sideB); err != nil {
		t.Fatal(err)
	}
	if n := countNotes(t, sideB); n != 7 {
		t.Fatalf("b restored %d notes, want 7 (fencing)", n)
	}

	// in place from the bucket
	if err := p.appFor(ta.app).Stage(context.Background(), t1); err != nil {
		t.Fatal(err)
	}
	ta.stop()
	ta = start(dirA, "instances/a/")
	defer ta.stop()
	if n, _ := ta.app.CountRecords("notes"); n != 20 {
		t.Fatalf("in place from bucket: %d notes", n)
	}
}
