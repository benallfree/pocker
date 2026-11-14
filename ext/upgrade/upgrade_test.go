package upgrade

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeptName(t *testing.T) {
	cases := []struct{ exe, v, want string }{
		{"/usr/local/bin/pocketone", "0.0.3", "/usr/local/bin/pocketone-0.0.3"},
		{"/usr/local/bin/pocketone", "v0.0.3", "/usr/local/bin/pocketone-0.0.3"},
		{"/usr/local/bin/pocketone", "(untracked)", "/usr/local/bin/pocketone.old"},
		{"/usr/local/bin/pocketone", "", "/usr/local/bin/pocketone.old"},
		{"/usr/local/bin/pocketone", "0.0.3-next", "/usr/local/bin/pocketone.old"},
		{`C:/bin/pocketone.exe`, "0.1.0", `C:/bin/pocketone-0.1.0.exe`},
	}
	for _, c := range cases {
		got := filepath.ToSlash(KeptName(filepath.FromSlash(c.exe), c.v))
		if got != c.want {
			t.Errorf("KeptName(%q, %q) = %q, want %q", c.exe, c.v, got, c.want)
		}
	}
}

func TestHomebrewFormula(t *testing.T) {
	if got := homebrewFormula("/opt/homebrew/Cellar/pocketone/0.0.3/bin/pocketone"); got != "pocketone" {
		t.Errorf("got %q", got)
	}
	if got := homebrewFormula("/usr/local/bin/pocketone"); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestCompareVersions(t *testing.T) {
	if compareVersions("0.0.3", "0.0.4") != 1 || compareVersions("0.1.0", "0.0.9") != -1 || compareVersions("1.0", "1.0.0") != 0 {
		t.Fatal("compareVersions is wrong")
	}
}

// fakeReleases serves a GitHub-style latest release "v0.0.4" with one darwin/arm64 archive holding a "pocketone"
// executable whose content is body. badSum corrupts checksums.txt.
func fakeReleases(t *testing.T, body string, badSum bool) *httptest.Server {
	t.Helper()
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, _ := zw.Create("pocketone")
	w.Write([]byte(body))
	zw.Close()
	archive := zbuf.Bytes()
	sum := sha256.Sum256(archive)
	hexSum := hex.EncodeToString(sum[:])
	if badSum {
		hexSum = strings.Repeat("0", 64)
	}
	const name = "pocketone_0.0.4_darwin_arm64.zip"

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/pockethost/pocketone/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Release{Tag: "v0.0.4", Body: "notes", Assets: []Asset{
			{Name: name, DownloadURL: srv.URL + "/dl/" + name},
			{Name: "checksums.txt", DownloadURL: srv.URL + "/dl/checksums.txt"},
		}})
	})
	mux.HandleFunc("/dl/"+name, func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/dl/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n%s  other.zip\n", hexSum, name, strings.Repeat("1", 64))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func testPlugin(t *testing.T, srv *httptest.Server, current, exe string) *plugin {
	cfg := Config{BaseURL: srv.URL}
	cfg.defaults()
	return &plugin{cfg: cfg, current: current, executable: func() (string, error) { return exe, nil },
		goos: "darwin", goarch: "arm64"}
}

func TestUpgradeKeepsEveryOldVersion(t *testing.T) {
	srv := fakeReleases(t, "new", false)
	dir := t.TempDir()
	exe := filepath.Join(dir, "pocketone")
	// an older kept copy from a previous upgrade must survive
	os.WriteFile(filepath.Join(dir, "pocketone-0.0.2"), []byte("older"), 0o755)
	os.WriteFile(exe, []byte("old"), 0o755)

	if err := testPlugin(t, srv, "0.0.3", exe).upgrade(false); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{exe: "new", exe + "-0.0.3": "old", exe + "-0.0.2": "older"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", filepath.Base(path), got, err, want)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Errorf("expected exactly 3 files (no temp left behind), got %d", len(entries))
	}
}

func TestUpgradeAlreadyLatest(t *testing.T) {
	srv := fakeReleases(t, "new", false)
	exe := filepath.Join(t.TempDir(), "pocketone")
	os.WriteFile(exe, []byte("current"), 0o755)
	if err := testPlugin(t, srv, "0.0.4", exe).upgrade(false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "current" {
		t.Errorf("executable changed: %q", got)
	}
}

func TestUpgradeRejectsBadChecksum(t *testing.T) {
	srv := fakeReleases(t, "new", true)
	dir := t.TempDir()
	exe := filepath.Join(dir, "pocketone")
	os.WriteFile(exe, []byte("old"), 0o755)
	err := testPlugin(t, srv, "0.0.3", exe).upgrade(false)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum mismatch, got %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Errorf("executable changed: %q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("expected only the executable left, got %d entries", len(entries))
	}
}

func TestUpgradeDevBuildKeptAsOld(t *testing.T) {
	srv := fakeReleases(t, "new", false)
	exe := filepath.Join(t.TempDir(), "pocketone")
	os.WriteFile(exe, []byte("dev"), 0o755)
	if err := testPlugin(t, srv, "(untracked)", exe).upgrade(false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe + ".old"); string(got) != "dev" {
		t.Errorf("dev build not kept as .old: %q", got)
	}
}
