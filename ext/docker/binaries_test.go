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
	zip1, sum1 := fakeRelease(t, "v1")
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if strings.HasSuffix(r.URL.Path, "/v0.30.2/pocketbase_0.30.2_linux_amd64.zip") {
			_, _ = w.Write(zip1)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	root := t.TempDir()
	// a version already in the cache (as a Node edge keeps it), not in the manifest
	if err := os.MkdirAll(filepath.Join(root, "0.22.21", "linux_amd64"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "0.22.21", "linux_amd64", "pocketbase"), []byte("cached"), 0o755)

	b := &Binaries{Root: root, Platform: "linux_amd64", Manifest: &Manifest{
		Hosts: map[string]string{"h": srv.URL + "/v{version}/{file}"},
		Versions: map[string]ManifestEntry{
			"0.30.2": {Host: "h", SHA256: map[string]string{"linux_amd64": sum1}},
			"0.30.1": {Host: "h", SHA256: map[string]string{"linux_amd64": "00"}},
			"0.31.0": {Host: "h", SHA256: map[string]string{"linux_arm64": "00"}}, // no amd64 build
		},
	}}
	if got := b.Versions(); strings.Join(got, ",") != "0.30.2,0.30.1,0.22.21" {
		t.Fatalf("versions: %v", got)
	}
	for want, exp := range map[string]string{"0.30.*": "0.30.2", "~0.30.1": "0.30.2", "0.22.21": "0.22.21", "v0.22.*": "0.22.21", "^0.30": "0.30.2"} {
		if got, err := b.Match(want); err != nil || got != exp {
			t.Errorf("Match(%q) = %q, %v; want %q", want, got, err, exp)
		}
	}
	if _, err := b.Match("0.31.*"); err == nil {
		t.Error("0.31 has no build for this platform")
	}

	ctx := context.Background()
	v, path, err := b.Resolve(ctx, "0.22.*")
	if err != nil || v != "0.22.21" || hits != 0 {
		t.Fatalf("cached: %s %s %v (hits %d)", v, path, err, hits)
	}
	v, path, err = b.Resolve(ctx, "0.30.*")
	if err != nil || v != "0.30.2" {
		t.Fatalf("download: %s %v", v, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "v1" || path != filepath.Join(root, "0.30.2", "linux_amd64", "pocketbase") {
		t.Fatalf("binary at %s: %q", path, got)
	}
	if fi, _ := os.Stat(path); fi.Mode()&0o111 == 0 {
		t.Fatal("not executable")
	}
	if _, _, err := b.Resolve(ctx, "0.30.2"); err != nil || hits != 1 {
		t.Fatalf("second resolve downloaded again (hits %d, %v)", hits, err)
	}
	// a checksum that doesn't match is refused and nothing is cached
	if _, _, err := b.Resolve(ctx, "0.30.1"); err == nil || !strings.Contains(err.Error(), "404") && !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("0.30.1: %v", err)
	}
	if _, err := os.Stat(b.Path("0.30.1")); err == nil {
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
	if _, _, err := b.Resolve(context.Background(), "0.30.0"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(b.Path("0.30.0")); err == nil {
		t.Fatal("tampered binary cached")
	}
}
