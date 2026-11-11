package pocker

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"golang.org/x/crypto/bcrypt"
)

func readLog(t *testing.T, path string) []map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []map[string]string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]string
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad log line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

func TestPlatformValuesStayOutOfJS(t *testing.T) {
	root := t.TempDir()
	hooks := `
routerAdd("GET", "/env", (e) => e.json(200, {
  adminSync: $os.getenv("ADMIN_SYNC"),
  token: $os.getenv("backup_token"),
  procEnv: JSON.stringify(process.env),
}))
routerAdd("GET", "/say", (e) => {
  console.log("hello", { a: 1 }, 42)
  console.error(new Error("boom"))
  return e.json(200, {})
})
`
	for _, id := range []string{"a", "b", "broken"} {
		if err := os.MkdirAll(filepath.Join(root, id, "pb_hooks"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(root, id, "pb_hooks/main.pb.js"), []byte(hooks), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "broken", "pb_hooks/main.pb.js"), []byte("this is not javascript {"), 0o644); err != nil {
		t.Fatal(err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("s3cret-password"), 4)
	if err != nil {
		t.Fatal(err)
	}
	a := Instance{
		ID:         "a",
		Hosts:      []string{"a.example.com"},
		Secrets:    map[string]string{"VISIBLE": "yes"},
		Superusers: []Superuser{{Email: "owner@example.com", PasswordHash: string(hash)}},
		Private:    map[string]string{"backup_token": "t0k-private"},
	}
	b := Instance{ID: "b", Hosts: []string{"b.example.com"}}
	broken := Instance{ID: "broken", Hosts: []string{"broken.example.com"}}

	m := NewManager(staticSource{a, b, broken}, ManagerOptions{
		PBRoot: root,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		// host Go code: a backup route checked against a private token
		Setup: func(inst Instance, app core.App) error {
			app.OnServe().BindFunc(func(e *core.ServeEvent) error {
				e.Router.GET("/api/ph/backup", func(re *core.RequestEvent) error {
					want := inst.Private["backup_token"]
					if want == "" || re.Request.Header.Get("x-ph-backup-token") != want {
						return re.JSON(http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
					}
					return re.JSON(http.StatusOK, map[string]bool{"ok": true})
				})
				return e.Next()
			})
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	defer m.Shutdown()
	for deadline := time.Now().Add(5 * time.Second); ; {
		if _, ok := m.Lookup("a.example.com"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("instances never loaded")
		}
		time.Sleep(10 * time.Millisecond)
	}

	do := func(method, host, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		return w
	}

	// JS sees its own env only: no ADMIN_SYNC, no private values
	w := do("GET", "a.example.com", "/env", "", nil)
	var env map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env["adminSync"] != "" || env["token"] != "" || strings.Contains(env["procEnv"], "t0k-private") ||
		!strings.Contains(env["procEnv"], "VISIBLE") {
		t.Fatalf("JS env leaked platform values or lost its own: %+v", env)
	}

	// the provider's superuser can log in
	w = do("POST", "a.example.com", "/api/collections/_superusers/auth-with-password",
		`{"identity":"owner@example.com","password":"s3cret-password"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("superuser login: %d %s", w.Code, w.Body.String())
	}

	// the host's Go route uses the private token
	if w := do("GET", "a.example.com", "/api/ph/backup", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("backup without token: %d", w.Code)
	}
	if w := do("GET", "a.example.com", "/api/ph/backup", "", map[string]string{"x-ph-backup-token": "t0k-private"}); w.Code != http.StatusOK {
		t.Errorf("backup with token: %d %s", w.Code, w.Body.String())
	}

	// console output goes to each instance's own log
	do("GET", "a.example.com", "/say", "", nil)
	do("GET", "b.example.com", "/say", "", nil)
	do("GET", "b.example.com", "/say", "", nil)
	logA := readLog(t, filepath.Join(root, "a", "logs", "exec.log"))
	logB := readLog(t, filepath.Join(root, "b", "logs", "exec.log"))
	if len(logA) != 2 || len(logB) != 4 {
		t.Fatalf("expected 2 lines for a and 4 for b, got %d and %d", len(logA), len(logB))
	}
	if logA[0]["stream"] != "stdout" || logA[0]["message"] != `hello {"a":1} 42` || logA[0]["time"] == "" {
		t.Errorf("console.log line: %+v", logA[0])
	}
	if logA[1]["stream"] != "stderr" || !strings.Contains(logA[1]["message"], "boom") {
		t.Errorf("console.error line: %+v", logA[1])
	}

	// a startup failure is written to that instance's log
	if w := do("GET", "broken.example.com", "/api/health", "", nil); w.Code != http.StatusBadGateway {
		t.Errorf("broken instance: %d", w.Code)
	}
	logBroken := readLog(t, filepath.Join(root, "broken", "logs", "exec.log"))
	if len(logBroken) == 0 || !strings.Contains(logBroken[0]["message"], "Instance failed to start") {
		t.Errorf("startup failure not logged: %+v", logBroken)
	}
}

func TestInstanceLogRotates(t *testing.T) {
	dir := t.TempDir()
	l := &instanceLog{path: filepath.Join(dir, "logs", "exec.log"), maxSize: 300}
	for i := 0; i < 10; i++ {
		l.Write("stdout", strings.Repeat("x", 60))
	}
	st, err := os.Stat(l.path)
	if err != nil || st.Size() > 300 {
		t.Fatalf("exec.log not bounded: %v %v", st, err)
	}
	if _, err := os.Stat(l.path + ".1"); err != nil {
		t.Fatal("expected exec.log.1 after rotation")
	}
}

func TestEncryptionKeyIsExplicit(t *testing.T) {
	root := t.TempDir()
	writeHooks(t, root, "enc", `routerAdd("GET", "/env", (e) => e.json(200, {
  key: $os.getenv("PB_ENCRYPTION_KEY"),
  all: JSON.stringify(process.env),
}))`)
	const key = "0123456789abcdef0123456789abcdef"
	inst := Instance{
		ID:            "enc",
		EncryptionKey: key,
		Secrets:       map[string]string{"PB_ENCRYPTION_KEY": "just-an-app-variable"},
	}
	running, err := startApp(appOptions{PBRoot: root, Defaults: DefaultLimits()}, inst)
	if err != nil {
		t.Fatal(err)
	}
	// the settings are stored encrypted with encryption_key
	if running.app.EncryptionKey() != key {
		t.Fatalf("app encryption key: %q", running.app.EncryptionKey())
	}
	w := httptest.NewRecorder()
	running.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/env", nil))
	var got map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["key"] != "just-an-app-variable" || strings.Contains(got["all"], key) {
		t.Fatalf("JS must see secrets only, never encryption_key: %+v", got)
	}
	stopApp(running)

	// the encrypted settings only load with the same key
	running, err = startApp(appOptions{PBRoot: root, Defaults: DefaultLimits()}, inst)
	if err != nil {
		t.Fatalf("restart with the same key: %v", err)
	}
	stopApp(running)
	inst.EncryptionKey = "ffffffffffffffffffffffffffffffff"
	if _, err := startApp(appOptions{PBRoot: root, Defaults: DefaultLimits()}, inst); err == nil {
		t.Fatal("a different encryption_key must not open the encrypted settings")
	}
}
