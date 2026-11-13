package jsembed

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"github.com/dop251/goja"
)

// recorder collects values passed to the JS global record(v) from every runtime.
type recorder struct {
	mu   sync.Mutex
	vals []string
}

func (r *recorder) onInit(vm *goja.Runtime) {
	vm.Set("record", func(v string) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.vals = append(r.vals, v)
	})
}

func (r *recorder) has(v string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.vals, v)
}

func (r *recorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.vals)
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func mapFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, content := range files {
		m[name] = &fstest.MapFile{Data: []byte(content)}
	}
	return m
}

// evalOnPool runs code on one of the app's pooled executors and returns the result as a string.
func evalOnPool(t *testing.T, app core.App, code string) string {
	t.Helper()
	var out string
	err := jsvm.Run(app, func(vm *goja.Runtime) error {
		v, err := vm.RunScript("pb.js", code)
		if err != nil {
			return err
		}
		out = v.String()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// tryRequire returns "ok:<exports.name>" or "err:<message>" for require(expr) on a pooled executor.
func tryRequire(t *testing.T, app core.App, expr string) string {
	t.Helper()
	return evalOnPool(t, app, `(() => { try { return "ok:" + require(`+expr+`).name } catch (e) { return "err:" + e } })()`)
}

// embeddedLib is a module tree under __hooks: nested paths and relative requires inside modules.
var embeddedLib = map[string]string{
	"lib/nested/a.js": `const b = require("./b.js"); const c = require("../c.js"); module.exports = { name: "a", b: b.name, c: c.name };`,
	"lib/nested/b.js": `module.exports = { name: "b" };`,
	"lib/c.js":        `module.exports = { name: "c" };`,
	"top.js":          `module.exports = { name: "top" };`,
}

func withLib(files map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range embeddedLib {
		out[k] = v
	}
	for k, v := range files {
		out[k] = v
	}
	return out
}

func TestEmbeddedNilKeepsDiskBehavior(t *testing.T) {
	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	hooksDir := t.TempDir()
	migrationsDir := t.TempDir()
	writeFiles(t, hooksDir, withLib(map[string]string{
		"main.pb.js":  `const a = require(__hooks + "/lib/nested/a.js"); record("hook:" + a.name + a.b + a.c); record("hooks=" + __hooks);`,
		"empty.pb.js": ``,
	}))
	writeFiles(t, migrationsDir, map[string]string{
		"1_m.js": `record("migration:" + require(__hooks + "/top.js").name);`,
	})

	rec := &recorder{}
	if err := jsvm.Register(app, Config(nil, nil, jsvm.Config{HooksDir: hooksDir, MigrationsDir: migrationsDir, OnInit: rec.onInit})); err != nil {
		t.Fatal(err)
	}

	absHooks, _ := filepath.Abs(hooksDir)
	for _, want := range []string{"hook:abc", "hooks=" + absHooks, "migration:top"} {
		if !rec.has(want) {
			t.Fatalf("missing %q in %v", want, rec.list())
		}
	}

	// the types directive is still prepended to empty hook files on disk
	raw, _ := os.ReadFile(filepath.Join(hooksDir, "empty.pb.js"))
	if !strings.Contains(string(raw), "/// <reference path=") {
		t.Fatalf("expected the types directive in the empty hook file, got %q", raw)
	}

	// require still reads from the disk
	if got := tryRequire(t, app, `__hooks + "/lib/c.js"`); got != "ok:c" {
		t.Fatalf("got %q", got)
	}
}

func TestEmbeddedHooksAndMigrationsFromFS(t *testing.T) {
	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	// on-disk dirs with files that must be ignored
	hooksDir := t.TempDir()
	migrationsDir := t.TempDir()
	writeFiles(t, hooksDir, map[string]string{
		"disk.pb.js":  `record("disk hook ran");`,
		"empty.pb.js": ``,
		"top.js":      `module.exports = { name: "disk top" };`,
	})
	writeFiles(t, migrationsDir, map[string]string{"1_disk.js": `record("disk migration ran");`})

	hooksFS := mapFS(withLib(map[string]string{
		"main.pb.js": `
			const a = require(__hooks + "/lib/nested/a.js");
			record("hook:" + a.name + a.b + a.c);
			record("rel:" + require("./lib/c.js").name);
			record("hooks=" + __hooks);
		`,
		"empty.pb.js": ``,
	}))
	migrationsFS := mapFS(map[string]string{
		"1_m.js":    `record("migration:" + require(__hooks + "/lib/nested/a.js").name + require("./top.js").name);`,
		"README.md": `not a migration`,
	})

	rec := &recorder{}
	err := jsvm.Register(app, Config(hooksFS, migrationsFS, jsvm.Config{
		HooksDir:      hooksDir,
		MigrationsDir: migrationsDir,
		HooksWatch:    true,
		HooksPoolSize: 2,
		OnInit:        rec.onInit,
	}))
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"hook:abc", "rel:c", "hooks=" + Root, "migration:atop"} {
		if !rec.has(want) {
			t.Fatalf("missing %q in %v", want, rec.list())
		}
	}
	for _, v := range rec.list() {
		if strings.Contains(v, "disk") {
			t.Fatalf("on-disk file was used: %v", rec.list())
		}
	}

	// nothing written to disk
	raw, _ := os.ReadFile(filepath.Join(hooksDir, "empty.pb.js"))
	if len(raw) != 0 {
		t.Fatalf("embedded mode wrote to the on-disk HooksDir: %q", raw)
	}

	// a pooled executor resolves from the FS
	if got := tryRequire(t, app, `__hooks + "/lib/nested/a.js"`); got != "ok:a" {
		t.Fatalf("got %q", got)
	}
	if got := tryRequire(t, app, `"./top.js"`); got != "ok:top" {
		t.Fatalf("got %q", got)
	}
}

func TestEmbeddedRequireIsSealed(t *testing.T) {
	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	// a real file outside the virtual root, required by its absolute path
	diskDir := t.TempDir()
	writeFiles(t, diskDir, map[string]string{
		"secret.js":                  `module.exports = { name: "secret" };`,
		"node_modules/dpkg/index.js": `module.exports = { name: "dpkg" };`,
	})
	secret := filepath.ToSlash(filepath.Join(diskDir, "secret.js"))

	hooksFS := mapFS(withLib(map[string]string{
		"node_modules/epkg/index.js": `module.exports = { name: "epkg" };`,
		"lib/escape.js":              `module.exports = require("../../x.js");`,
	}))
	// no hook files: the lazy executors still use the sealed registry
	if err := jsvm.Register(app, Config(hooksFS, nil, jsvm.Config{OnInit: (&recorder{}).onInit})); err != nil {
		t.Fatal(err)
	}

	for _, expr := range []string{
		`__hooks + "/../x.js"`,
		`__hooks + "/../pb_hooks/../../` + strings.TrimPrefix(secret, "/") + `"`,
		`"../x.js"`,
		`__hooks + "/lib/escape.js"`,
		`"` + secret + `"`,
		`"epkg"`,
		`"dpkg"`,
		`__hooks + "/node_modules/epkg/index.js"`,
		`__hooks + "/missing.js"`,
		`__hooks + "/lib"`,
	} {
		got := tryRequire(t, app, expr)
		if !strings.HasPrefix(got, "err:") || !strings.Contains(got, "Invalid module") {
			t.Errorf("require(%s): expected module not found, got %q", expr, got)
		}
	}

	// $os file reads of __hooks fail: the virtual root doesn't exist on disk
	got := evalOnPool(t, app, `(() => { try { $os.readFile(__hooks + "/top.js"); return "read" } catch (e) { return "err" } })()`)
	if got != "err" {
		t.Fatalf("expected $os.readFile of the virtual root to fail, got %q", got)
	}
}

func TestEmbeddedRealDirAtVirtualRootIsIgnored(t *testing.T) {
	realRoot := t.TempDir()
	writeFiles(t, realRoot, map[string]string{
		"only-on-disk.js": `module.exports = { name: "disk" };`,
		"top.js":          `module.exports = { name: "disk top" };`,
	})
	old := root
	root = filepath.ToSlash(realRoot)
	defer func() { root = old }()

	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	if err := jsvm.Register(app, Config(mapFS(embeddedLib), nil, jsvm.Config{})); err != nil {
		t.Fatal(err)
	}

	if got := tryRequire(t, app, `__hooks + "/only-on-disk.js"`); !strings.HasPrefix(got, "err:") {
		t.Fatalf("a real file at the virtual root was loaded: %q", got)
	}
	if got := tryRequire(t, app, `__hooks + "/top.js"`); got != "ok:top" {
		t.Fatalf("got %q", got)
	}
}

func TestEmbeddedRouteAndCronRun(t *testing.T) {
	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	hooksFS := mapFS(map[string]string{
		"main.pb.js": `
			routerAdd("GET", "/embedded", (e) => {
				const greet = require(` + "`${__hooks}/lib/greet.js`" + `);
				return e.string(200, greet.hello("route") + "|" + require("./lib/greet.js").hello("rel"));
			});
			cronAdd("embeddedJob", "0 0 1 1 *", () => {
				require(__hooks + "/lib/greet.js").hit();
			});
		`,
		"lib/greet.js": `module.exports = { name: "greet", hello: (w) => "hello " + w, hit: () => record("cron ran") };`,
	})

	rec := &recorder{}
	if err := jsvm.Register(app, Config(hooksFS, nil, jsvm.Config{OnInit: rec.onInit})); err != nil {
		t.Fatal(err)
	}

	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	serveEvent := &core.ServeEvent{App: app, Router: router}
	err = app.OnServe().Trigger(serveEvent, func(e *core.ServeEvent) error {
		mux, err := e.Router.BuildMux()
		if err != nil {
			return err
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest("GET", "/embedded", nil))
		if rr.Code != 200 || rr.Body.String() != "hello route|hello rel" {
			t.Fatalf("route: got %d %q", rr.Code, rr.Body.String())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var ran bool
	for _, j := range app.Cron().Jobs() {
		if j.Id() == "embeddedJob" {
			j.Run()
			ran = true
		}
	}
	if !ran {
		t.Fatal("cron job embeddedJob not registered")
	}
	if !rec.has("cron ran") {
		t.Fatalf("cron handler didn't run the embedded module: %v", rec.list())
	}
}
