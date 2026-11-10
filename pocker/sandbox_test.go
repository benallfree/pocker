package pocker

import (
	"context"
	"encoding/json"
	sqlite3 "modernc.org/sqlite/lib"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Each route tries one escape from instance "a" towards instance "b" or the host, and reports {ok, v, err}.
// PocketBase compiles every handler from its own source text, so each one is self-contained.
var attacks = [][2]string{
	{"cmd", `String($os.cmd("id").output())`},
	{"exec", `String($os.exec("id").output())`},
	{"exit", `$os.exit(1)`},
	{"args", `$os.args.length`},
	{"envOwn", `$os.getenv("A_SECRET")`},
	{"envHost", `$os.getenv("HOME")`},
	{"procEnvHost", `process.env.HOME === undefined ? "" : process.env.HOME`},
	{"readOwn", `String($os.readFile(__hooks + "/main.pb.js")).length`},
	{"readOtherAbs", `String($os.readFile(OTHER + "/pb_data/secret.txt"))`},
	{"readOtherRel", `String($os.readFile("../b/pb_data/secret.txt"))`},
	{"readHost", `String($os.readFile("/etc/hostname"))`},
	{"readSymlink", `String($os.readFile("pb_public/escape/secret.txt"))`},
	{"writeHost", `$os.writeFile("/tmp/pocker-sandbox-pwn", "x", 0o644)`},
	{"writeOther", `$os.writeFile(OTHER + "/pb_data/pwned", "x", 0o644)`},
	{"removeOther", `$os.removeAll(OTHER)`},
	{"renameOut", `$os.rename(__hooks + "/main.pb.js", "/tmp/pocker-moved")`},
	{"dirFS", `$os.dirFS("/etc")`},
	{"openRoot", `$os.openRoot("/")`},
	{"glob", `$filepath.glob("/etc/*")`},
	{"globOwn", `$filepath.glob(__hooks + "/*.js").length`},
	{"walk", `$filepath.walkDir("/", (p, d, err) => { throw new Error("walked " + p) })`},
	{"fileFromPath", `$filesystem.fileFromPath("/etc/hostname")`},
	{"fsLocal", `$filesystem.local("/")`},
	{"template", `$template.loadFiles("/etc/hostname").render({})`},
	{"static", `$apis.static("/etc", false)`},
	{"requireOther", `require(OTHER + "/pb_hooks/lib.js")`},
	{"requireOwn", `require(__hooks + "/lib.js").name`},
	{"restart", `typeof $app.restart`},
	{"bootstrap", `typeof $app.bootstrap`},
	{"rawDB", `typeof $app.concurrentDB().db`},
	{"onServe", `typeof $app.onServe`},
	{"bindHook", `typeof $app.onRecordCreate().bindFunc`},
	{"cron", `typeof $app.cron`},
	{"unsafeWithoutHooks", `typeof $app.unsafeWithoutHooks`},
	{"attach", `$app.db().newQuery("ATTACH DATABASE '" + OTHER + "/pb_data/data.db' AS x").execute()`},
	{"attachSneaky", `$app.db().newQuery("/* x */ aTtAcH '" + OTHER + "/pb_data/data.db' as y").execute()`},
	{"vacuumIntoHost", `$app.db().newQuery("VACUUM INTO '/tmp/pocker-sandbox-copy.db'").execute()`},
	{"storePragma", `$app.db().newQuery("PRAGMA temp_store_directory = '/tmp'").execute()`},
	{"vacuumIntoOwn", `($app.db().newQuery("VACUUM INTO '" + $app.dataDir() + "/copy.db'").execute(), "done")`},
	{"sqlReadfile", `$app.db().newQuery("SELECT readfile('/etc/hostname') AS x").execute()`},
	{"sqlWritefile", `$app.db().newQuery("SELECT writefile('/tmp/pocker-sql-pwn', 'x') AS x").execute()`},
	{"sqlFsdir", `$app.db().newQuery("SELECT name FROM fsdir('/') LIMIT 1").execute()`},
	{"sqlLoadExtension", `$app.db().newQuery("SELECT load_extension('/tmp/x.so') AS x").execute()`},
	{"sqlAttachURI", `$app.db().newQuery("ATTACH 'file:" + OTHER + "/pb_data/data.db?mode=ro' AS z").execute()`},
	{"sqlRunaway", `$app.db().newQuery("WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c) SELECT max(x) AS m FROM c").execute()`},
	{"plainVacuum", `($app.db().newQuery("VACUUM").execute(), "done")`},
	{"literalAttachText", `($app.db().newQuery("SELECT 'attach' AS w").execute(), "done")`},
	{"sleepCapped", `{ const s = Date.now(); sleep(60000); return Date.now() - s }`},
	{"loop", `{ while (true) {} }`},
}

func attackHooks() string {
	var b strings.Builder
	for _, a := range attacks {
		b.WriteString(`routerAdd("GET", "/t/` + a[0] + `", (e) => {
  const OTHER = $os.getenv("OTHER_DIR")
  try {
    const v = (() => ` + a[1] + `)()
    return e.json(200, { ok: true, v: v === undefined ? null : v })
  } catch (err) {
    return e.json(200, { ok: false, err: String(err) })
  }
})
`)
	}
	return b.String()
}

type attackResult struct {
	OK  bool   `json:"ok"`
	V   any    `json:"v"`
	Err string `json:"err"`
}

func TestSandboxBlocksEscapes(t *testing.T) {
	root := t.TempDir()
	aDir := filepath.Join(root, "a")
	bDir := filepath.Join(root, "b")
	for _, d := range []string{"a/pb_hooks", "a/pb_public", "b/pb_data", "b/pb_hooks"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(aDir, "pb_hooks/main.pb.js"), []byte(attackHooks()), 0o644))
	must(os.WriteFile(filepath.Join(aDir, "pb_hooks/lib.js"), []byte(`module.exports = { name: "own lib" }`), 0o644))
	must(os.WriteFile(filepath.Join(bDir, "pb_data/secret.txt"), []byte("b's secret"), 0o644))
	must(os.WriteFile(filepath.Join(bDir, "pb_hooks/lib.js"), []byte(`module.exports = { name: "b's lib" }`), 0o644))
	// a symlink planted in a's folder (e.g. through an upload) pointing at b's data
	must(os.Symlink(filepath.Join(bDir, "pb_data"), filepath.Join(aDir, "pb_public/escape")))
	t.Setenv("HOME", "/root/host-home")

	defer func(d time.Duration) { maxQueryDuration = d }(maxQueryDuration)
	maxQueryDuration = time.Second

	running, err := startApp(appOptions{PBRoot: root, MaxJSExecution: time.Second}, Instance{
		ID:  "a",
		Env: map[string]string{"A_SECRET": "a's own value", "OTHER_DIR": bDir},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stopApp(running)

	call := func(name string) (attackResult, int, time.Duration) {
		r := httptest.NewRequest(http.MethodGet, "/t/"+name, nil)
		w := httptest.NewRecorder()
		start := time.Now()
		running.handler.ServeHTTP(w, r)
		var res attackResult
		_ = json.Unmarshal(w.Body.Bytes(), &res)
		return res, w.Code, time.Since(start)
	}

	blocked := []string{
		"cmd", "exec", "exit", "readOtherAbs", "readOtherRel", "readHost", "readSymlink", "writeHost", "writeOther",
		"removeOther", "renameOut", "dirFS", "openRoot", "glob", "walk", "fileFromPath", "fsLocal", "template",
		"static", "requireOther", "attach", "attachSneaky", "vacuumIntoHost", "storePragma",
		"sqlReadfile", "sqlWritefile", "sqlFsdir", "sqlLoadExtension", "sqlAttachURI",
	}
	for _, name := range blocked {
		res, code, _ := call(name)
		if code != 200 || res.OK {
			t.Errorf("%s: expected blocked, got status=%d %+v", name, code, res)
		}
	}

	// the SQL refusals come from SQLite's authorizer, not from something incidental
	for _, name := range []string{"attach", "attachSneaky", "vacuumIntoHost", "storePragma"} {
		if res, _, _ := call(name); !strings.Contains(res.Err, "not authorized") && !strings.Contains(res.Err, "authorization denied") {
			t.Errorf("%s: expected SQLite's authorizer to refuse it, got %q", name, res.Err)
		}
	}

	hidden := []string{"restart", "bootstrap", "rawDB", "onServe", "bindHook", "cron", "unsafeWithoutHooks"}
	for _, name := range hidden {
		res, _, _ := call(name)
		if !res.OK || res.V != "undefined" {
			t.Errorf("%s: expected undefined, got %+v", name, res)
		}
	}

	expect := map[string]any{
		"args":        float64(0),
		"envOwn":      "a's own value",
		"envHost":     "",
		"procEnvHost": "",
		"requireOwn":  "own lib",
	}
	for name, want := range expect {
		res, _, _ := call(name)
		if !res.OK || res.V != want {
			t.Errorf("%s: want %v, got %+v", name, want, res)
		}
	}

	for _, name := range []string{"readOwn", "globOwn", "vacuumIntoOwn", "plainVacuum", "literalAttachText"} {
		res, code, _ := call(name)
		if code != 200 || !res.OK {
			t.Errorf("%s: expected allowed, got status=%d %+v", name, code, res)
		}
	}

	if _, _, took := call("sleepCapped"); took > 12*time.Second {
		t.Errorf("sleepCapped: expected the 10s cap, took %s", took)
	}

	// PocketBase's own backups use VACUUM INTO pb_data; the SQL guard must let them through
	if err := running.app.CreateBackup(context.Background(), "sandbox-test.zip"); err != nil {
		t.Errorf("backup through the SQL guard: %v", err)
	}

	if res, _, took := call("sqlRunaway"); res.OK || took > 5*time.Second {
		t.Errorf("sqlRunaway: expected the query to be interrupted, got %+v after %s", res, took)
	}

	if _, code, took := call("loop"); code == 200 || took > 5*time.Second {
		t.Errorf("loop: expected an interrupted request, got status=%d after %s", code, took)
	}
	// the app still answers after the interrupt
	if res, code, _ := call("envOwn"); code != 200 || !res.OK {
		t.Errorf("app unhealthy after interrupt: status=%d %+v", code, res)
	}

	for _, p := range []string{"/tmp/pocker-sql-pwn", "/tmp/pocker-sandbox-pwn", "/tmp/pocker-moved", "/tmp/pocker-sandbox-copy.db", filepath.Join(bDir, "pb_data/pwned")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s exists: an escape wrote outside the app", p)
			_ = os.Remove(p)
		}
	}
	if _, err := os.Stat(filepath.Join(bDir, "pb_data/secret.txt")); err != nil {
		t.Errorf("b's data was touched: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(aDir, "pb_hooks/main.pb.js")); !strings.Contains(string(b), "routerAdd") {
		t.Error("a's hooks file was moved")
	}
}

func TestAuthorizePolicy(t *testing.T) {
	dir := "/data/a/pb_data"
	cases := []struct {
		action     int32
		arg1, arg2 string
		want       int32
	}{
		{sqlite3.SQLITE_ATTACH, "", "", sqlite3.SQLITE_OK},                                  // temp db (plain VACUUM)
		{sqlite3.SQLITE_ATTACH, "/data/a/pb_data/copy.db", "", sqlite3.SQLITE_OK},           // VACUUM INTO own pb_data
		{sqlite3.SQLITE_ATTACH, "/data/b/pb_data/data.db", "", sqlite3.SQLITE_DENY},         // another app
		{sqlite3.SQLITE_ATTACH, "/data/a/pb_data/../../b/data.db", "", sqlite3.SQLITE_DENY}, // dot-dot
		{sqlite3.SQLITE_ATTACH, "file:/tmp/x.db?mode=rwc", "", sqlite3.SQLITE_DENY},
		{sqlite3.SQLITE_PRAGMA, "TEMP_STORE_DIRECTORY", "/tmp", sqlite3.SQLITE_DENY},
		{sqlite3.SQLITE_PRAGMA, "table_info", "users", sqlite3.SQLITE_OK},
		{sqlite3.SQLITE_FUNCTION, "", "load_extension", sqlite3.SQLITE_DENY},
		{sqlite3.SQLITE_FUNCTION, "", "lower", sqlite3.SQLITE_OK},
		{sqlite3.SQLITE_SELECT, "", "", sqlite3.SQLITE_OK},
	}
	for _, c := range cases {
		if got := authorize(c.action, c.arg1, c.arg2, dir); got != c.want {
			t.Errorf("authorize(%d, %q, %q) = %d, want %d", c.action, c.arg1, c.arg2, got, c.want)
		}
	}
}
