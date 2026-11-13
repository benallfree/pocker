package docker

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeRelease(t *testing.T, content string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("pocketbase")
	_, _ = w.Write([]byte(content))
	_, _ = zw.Create("CHANGELOG.md")
	_ = zw.Close()
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func TestBinaries(t *testing.T) {
	zipPB, sumPB := fakeRelease(t, "pb 0.30.2")
	zipCE, sumCE := fakeRelease(t, "ce 0.0.2")
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/pb/v0.30.2/pocketbase_0.30.2_linux_amd64.zip":
			_, _ = w.Write(zipPB)
		case "/ce/v0.0.2/pocketbase_0.0.2_linux_amd64.zip":
			_, _ = w.Write(zipCE)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	root := t.TempDir()
	cache := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// already in the cache, as a Node edge keeps it: a supported upstream version and a sunset one
	cache("0.24.0/linux_amd64/pocketbase", "cached 0.24.0")
	cache("0.22.21/linux_amd64/pocketbase", "cached 0.22.21")

	b := &Binaries{Root: root, Platform: "linux_amd64", Manifest: &Manifest{
		Hosts: map[string]string{"gh": srv.URL + "/pb/v{version}/{file}", "pbce": srv.URL + "/ce/v{version}/{file}"},
		Versions: map[string]ManifestEntry{
			"0.30.2": {Host: "gh", SHA256: map[string]string{"linux_amd64": sumPB}},
			"0.30.1": {Host: "gh", SHA256: map[string]string{"linux_amd64": "00"}},
			"0.31.0": {Host: "gh", SHA256: map[string]string{"linux_arm64": "00"}}, // no amd64 build
			"0.22.5": {Host: "gh", SHA256: map[string]string{"linux_amd64": "00"}}, // sunset
		},
		CE: map[string]ManifestEntry{
			"0.0.2": {Host: "pbce", Base: "0.41.1", SHA256: map[string]string{"linux_amd64": sumCE}},
			"0.0.1": {Host: "pbce", Base: "0.41.1", SHA256: map[string]string{"linux_amd64": "00"}},
		},
	}}
	if got := strings.Join(b.Versions(LinePB), ","); got != "0.30.2,0.30.1,0.24.0" {
		t.Fatalf("pb versions (0.22 excluded): %v", got)
	}
	if got := strings.Join(b.Versions(LineCE), ","); got != "0.0.2,0.0.1" {
		t.Fatalf("ce versions: %v", got)
	}
	for want, exp := range map[string]string{
		"0.30.*": "pb:0.30.2", "pb:0.30.*": "pb:0.30.2", "~0.30.1": "pb:0.30.2", "pb:^0.24": "pb:0.24.0", "v0.24.0": "pb:0.24.0",
		"ce:0.0.*": "ce:0.0.2", "ce:0.0.1": "ce:0.0.1", "ce:~0.0.1": "ce:0.0.2",
	} {
		line, v, err := b.Match(want)
		if err != nil || line+":"+v != exp {
			t.Errorf("Match(%q) = %s:%s, %v; want %s", want, line, v, err, exp)
		}
	}
	if _, _, err := b.Match("0.22.*"); err == nil || !strings.Contains(err.Error(), "no longer supported") {
		t.Errorf("0.22: %v", err)
	}
	if _, _, err := b.Match("pb:0.22.21"); err == nil || !strings.Contains(err.Error(), "no longer supported") {
		t.Errorf("cached 0.22.21: %v", err)
	}
	if _, _, err := b.Match("0.31.*"); err == nil {
		t.Error("0.31 has no build for this platform")
	}
	if _, _, err := b.Match("xx:0.1.0"); err == nil {
		t.Error("unknown release line accepted")
	}

	ctx := context.Background()
	if line, v, path, err := b.Resolve(ctx, "0.24.*"); err != nil || line+":"+v != "pb:0.24.0" || hits != 0 || path != filepath.Join(root, "0.24.0", "linux_amd64", "pocketbase") {
		t.Fatalf("cached: %s:%s %s %v (hits %d)", line, v, path, err, hits)
	}
	_, _, path, err := b.Resolve(ctx, "pb:0.30.*")
	if got, _ := os.ReadFile(path); err != nil || string(got) != "pb 0.30.2" || path != filepath.Join(root, "0.30.2", "linux_amd64", "pocketbase") {
		t.Fatalf("pb download at %s: %q %v", path, got, err)
	}
	_, _, path, err = b.Resolve(ctx, "ce:0.0.*")
	if got, _ := os.ReadFile(path); err != nil || string(got) != "ce 0.0.2" || path != filepath.Join(root, "ce", "0.0.2", "linux_amd64", "pocketbase") {
		t.Fatalf("ce download at %s: %q %v", path, got, err)
	}
	if fi, _ := os.Stat(path); fi.Mode()&0o111 == 0 {
		t.Fatal("not executable")
	}
	if _, _, _, err := b.Resolve(ctx, "ce:0.0.2"); err != nil || hits != 2 {
		t.Fatalf("second resolve downloaded again (hits %d, %v)", hits, err)
	}
	// a bad download (404 or checksum) is refused and nothing is cached
	if _, _, _, err := b.Resolve(ctx, "0.30.1"); err == nil {
		t.Fatal("0.30.1 should fail")
	}
	if _, err := os.Stat(b.Path(LinePB, "0.30.1")); err == nil {
		t.Fatal("bad download cached")
	}
}

func TestBinariesChecksum(t *testing.T) {
	z, _ := fakeRelease(t, "evil")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(z) }))
	defer srv.Close()
	b := &Binaries{Root: t.TempDir(), Platform: "linux_amd64", Manifest: &Manifest{
		Hosts:    map[string]string{"h": srv.URL + "/{file}"},
		Versions: map[string]ManifestEntry{"0.30.0": {Host: "h", SHA256: map[string]string{"linux_amd64": strings.Repeat("ab", 32)}}},
	}}
	if _, _, _, err := b.Resolve(context.Background(), "0.30.0"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(b.Path(LinePB, "0.30.0")); err == nil {
		t.Fatal("tampered binary cached")
	}
}
