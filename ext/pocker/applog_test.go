package pocker

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A hook that throws shows up in the instance log (the dashboard's Logs tab) with the status the client got and a
// position in the customer's own file, not <cwd>/pb.js.
func TestHookErrorReachesInstanceLog(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pb_hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "// line 1\n" +
		"routerAdd(\"GET\", \"/boom\", (e) => {\n" +
		"  const x = 1\n" +
		"  throw new Error(\"kaboom\")\n" +
		"})\n"
	if err := os.WriteFile(filepath.Join(dir, "pb_hooks", "main.pb.js"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newTestManager(t, false, Instance{ID: "a", Hosts: []string{"a.test"}, Dir: dir})
	defer m.Shutdown()

	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://a.test/boom", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}

	logPath := filepath.Join(dir, "logs", "exec.log")
	var got string
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(logPath)
		got = string(b)
		if strings.Contains(got, "/boom") || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, want := range []string{`ERROR GET /boom 400`, `kaboom`, `pb_hooks/main.pb.js:4:9`, `"stream":"stderr"`} {
		if !strings.Contains(got, want) {
			t.Errorf("instance log lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(strings.ReplaceAll(got, "main.pb.js:", ""), "pb.js:") {
		t.Errorf("instance log still names pb.js:\n%s", got)
	}
}
