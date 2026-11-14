package pocker

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeHook(t *testing.T, dir, body string) {
	t.Helper()
	src := `routerAdd("GET", "/v", (e) => e.string(200, "` + body + `"))`
	if err := os.WriteFile(filepath.Join(dir, "pb_hooks", "main.pb.js"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func get(m *Manager, host string) string {
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://"+host+"/v", nil))
	b, _ := io.ReadAll(w.Result().Body)
	return string(b)
}

// Changing a running app's pb_hooks restarts only that app, which then serves the new code.
func TestHooksChangeRestartsOnlyThatApp(t *testing.T) {
	da, db := t.TempDir(), t.TempDir()
	for _, d := range []string{da, db} {
		if err := os.MkdirAll(filepath.Join(d, "pb_hooks"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeHook(t, da, "a1")
	writeHook(t, db, "b1")
	a := Instance{ID: "a", Hosts: []string{"a.test"}, Dir: da}
	b := Instance{ID: "b", Hosts: []string{"b.test"}, Dir: db}
	m := newTestManager(t, false, a, b)
	defer m.Shutdown()

	if got := get(m, "a.test"); got != "a1" {
		t.Fatalf("a before: %q", got)
	}
	if got := get(m, "b.test"); got != "b1" {
		t.Fatalf("b before: %q", got)
	}
	rb, _ := m.app(b)

	writeHook(t, da, "a2")
	deadline := time.Now().Add(10 * time.Second)
	for get(m, "a.test") != "a2" {
		if time.Now().After(deadline) {
			t.Fatalf("a still serves %q after its hooks changed", get(m, "a.test"))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if rb2, _ := m.app(b); rb2 != rb {
		t.Error("b was restarted too")
	}
	if got := get(m, "b.test"); got != "b1" {
		t.Errorf("b after: %q", got)
	}
}

