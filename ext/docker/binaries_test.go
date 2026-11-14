package docker

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeRelease(t *testing.T, content string, binary ...string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	name := "pocketbase"
	if len(binary) > 0 {
		name = binary[0]
	}
	w, _ := zw.Create(name)
	_, _ = w.Write([]byte(content))
	_, _ = zw.Create("CHANGELOG.md")
	_ = zw.Close()
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func rel(line, v string) Release { return Release{Line: line, Version: v} }

func keys(rs []Release) string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Key())
	}
	return strings.Join(out, ",")
}

// The one place that culls: newest patch per minor and line, upstream from 0.23 on.
func TestLatestPatches(t *testing.T) {
	in := []Release{rel("pb", "0.43.0-rc1"), rel("p1", "0.0.3-beta.1"), rel("pb", "0.22.30"), rel("pb", "0.23.1"), rel("pb", "0.23.12"), rel("pb", "0.36.9"), rel("pb", "0.36.10"),
		rel("pb", "0.42.0"), rel("pb", "0.42.2"), rel("p1", "0.0.1"), rel("p1", "0.0.2"), rel("p1", "0.1.0"), rel("pb", "bogus")}
	if got := keys(LatestPatches(in)); got != "p1:0.0.2,p1:0.1.0,pb:0.23.12,pb:0.36.10,pb:0.42.2" {
		t.Fatalf("got %s", got)
	}
}

// The one place that matches an instance's version to a release.
func TestPick(t *testing.T) {
	rs := []Release{rel("pb", "0.23.12"), rel("pb", "0.36.9"), rel("pb", "0.36.10"), rel("p1", "0.0.1"), rel("p1", "0.0.2"),
		rel("pb", "0.36.11-rc1"), rel("p1", "0.0.3-beta.1")} // pre-releases in a list are never picked
	for want, exp := range map[string]string{
		"0.36.*": "pb:0.36.10", "pb:0.36.*": "pb:0.36.10", "pb:0.36.9": "pb:0.36.9", "~0.23.1": "pb:0.23.12",
		"p1:0.0.*": "p1:0.0.2", "p1:0.0.1": "p1:0.0.1", "v0.23.12": "pb:0.23.12",
	} {
		if r, err := Pick(rs, want); err != nil || r.Key() != exp {
			t.Errorf("Pick(%q) = %s, %v; want %s", want, r.Key(), err, exp)
		}
	}
	for want, msg := range map[string]string{"0.22.*": "no longer supported", "pb:0.30.*": "not offered", "p1:0.1.*": "not offered", "xx:1.0": "unknown release line"} {
		if _, err := Pick(rs, want); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("Pick(%q): %v, want %q", want, err, msg)
		}
	}
}

func TestFetchSeedIndex(t *testing.T) {
	zipPB, sumPB := fakeRelease(t, "pb 0.30.2")
	zipP1, sumP1 := fakeRelease(t, "p1 0.0.2", "pocketone")
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/pb.zip":
			_, _ = w.Write(zipPB)
		case "/p1.zip":
			_, _ = w.Write(zipP1)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	b := &Binaries{Root: t.TempDir(), Platform: "linux_amd64"}
	pb := Release{Line: "pb", Version: "0.30.2", Assets: map[string]Asset{"linux_amd64": {URL: srv.URL + "/pb.zip", SHA256: sumPB}}}
	p1 := Release{Line: "p1", Version: "0.0.2", Base: "0.41.1", Assets: map[string]Asset{"linux_amd64": {URL: srv.URL + "/p1.zip", SHA256: sumP1}}}
	added, err := b.Seed(context.Background(), []Release{pb, p1})
	if err != nil || strings.Join(added, ",") != "pb:0.30.2,p1:0.0.2" {
		t.Fatalf("seed: %v %v", added, err)
	}
	if got, _ := os.ReadFile(filepath.Join(b.Root, "0.30.2", "linux_amd64", "pocketbase")); string(got) != "pb 0.30.2" {
		t.Fatalf("pb binary: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(b.Root, "p1", "0.0.2", "linux_amd64", "pocketone")); string(got) != "p1 0.0.2" {
		t.Fatalf("p1 binary: %q", got)
	}
	if added, _ := b.Seed(context.Background(), []Release{pb, p1}); len(added) != 0 || hits != 2 {
		t.Fatalf("second seed downloaded again: %v (hits %d)", added, hits)
	}
	bad := Release{Line: "pb", Version: "0.30.3", Assets: map[string]Asset{"linux_amd64": {URL: srv.URL + "/pb.zip", SHA256: strings.Repeat("ab", 32)}}}
	if err := b.Fetch(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "checksum mismatch") || b.Has(bad) {
		t.Fatalf("tampered: %v (cached %v)", err, b.Has(bad))
	}
	if err := b.Fetch(context.Background(), Release{Line: "pb", Version: "0.30.4", Assets: map[string]Asset{"linux_arm64": {}}}); err == nil {
		t.Fatal("a release without this platform's build was fetched")
	}

	// the index and the YAML source: latest patch of each tracked minor in the index, nothing from the network
	if err := WriteIndex(b.Root, []Release{p1, pb, rel("pb", "0.30.1"), rel("pb", "0.36.9")}); err != nil {
		t.Fatal(err)
	}
	y := YAMLVersions{Tracked: []string{"pb:0.30", "p1:0.0.*"}, Root: b.Root}
	rs, err := y.Releases(context.Background())
	if err != nil || keys(rs) != "p1:0.0.2,pb:0.30.2" {
		t.Fatalf("yaml source: %s %v", keys(rs), err)
	}
	if rs, _ := (YAMLVersions{Tracked: []string{"pb:0.30"}, Root: t.TempDir()}).Releases(context.Background()); len(rs) != 0 {
		t.Fatalf("no index should list nothing: %v", rs)
	}
}

// Discovery against a fake release host: tags from git refs, the latest patch per minor, checksums.txt per release,
// the first host that has a release; Update records and seeds the tracked minors.
func TestDiscoveryAndUpdate(t *testing.T) {
	zipNew, sumNew := fakeRelease(t, "pb 0.42.2")
	zipOld, sumOld := fakeRelease(t, "pb 0.36.9")
	zipP1, sumP1 := fakeRelease(t, "p1 0.0.2", "pocketone")
	mux := http.NewServeMux()
	mux.HandleFunc("/upstream/refs", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "001e# service=git-upload-pack\n0000 refs/tags/v0.22.30 refs/tags/v0.36.8 refs/tags/v0.36.9 refs/tags/v0.36.9^{} refs/tags/v0.42.1 refs/tags/v0.42.2 refs/tags/v0.43.0-rc1 refs/tags/v0.42.3-beta.2\n")
	})
	mux.HandleFunc("/p1/refs", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "refs/tags/v0.0.1 refs/tags/v0.0.2\n") })
	// the new host has 0.42.x only; the archive has the older ones
	mux.HandleFunc("/new/v0.42.2/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  pocketbase_0.42.2_linux_amd64.zip\n", sumNew)
	})
	mux.HandleFunc("/new/v0.42.2/pocketbase_0.42.2_linux_amd64.zip", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(zipNew) })
	mux.HandleFunc("/old/v0.36.9/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  pocketbase_0.36.9_linux_amd64.zip\n", sumOld)
	})
	mux.HandleFunc("/old/v0.36.9/pocketbase_0.36.9_linux_amd64.zip", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(zipOld) })
	mux.HandleFunc("/p1rel/v0.0.2/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  pocketone_0.0.2_linux_amd64.zip\n", sumP1)
	})
	mux.HandleFunc("/p1rel/v0.0.2/pocketone_0.0.2_linux_amd64.zip", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(zipP1) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	d := &Discovery{Platforms: []string{"linux_amd64"}, UpstreamRefs: srv.URL + "/upstream/refs", PocketOneRefs: srv.URL + "/p1/refs",
		UpstreamHosts: []string{srv.URL + "/new/v{version}/{file}", srv.URL + "/old/v{version}/{file}"}, PocketOneHost: srv.URL + "/p1rel/v{version}/{file}"}

	latest, err := d.Latest(context.Background(), nil)
	if err != nil || keys(latest) != "p1:0.0.2,pb:0.36.9,pb:0.42.2" {
		t.Fatalf("latest: %s %v", keys(latest), err)
	}
	for _, r := range latest {
		a := r.Assets["linux_amd64"]
		if a.URL == "" || len(a.SHA256) != 64 {
			t.Fatalf("%s not pinned: %+v", r.Key(), r.Assets)
		}
	}
	if latest[2].Assets["linux_amd64"].URL != srv.URL+"/new/v0.42.2/pocketbase_0.42.2_linux_amd64.zip" ||
		latest[1].Assets["linux_amd64"].URL != srv.URL+"/old/v0.36.9/pocketbase_0.36.9_linux_amd64.zip" {
		t.Fatalf("hosts: %+v", latest)
	}

	b := &Binaries{Root: t.TempDir(), Platform: "linux_amd64"}
	tracking, added, err := Update(context.Background(), d, b, []string{"pb:0.42", "p1:0.0"})
	if err != nil || keys(tracking) != "p1:0.0.2,pb:0.42.2" || strings.Join(added, ",") != "p1:0.0.2,pb:0.42.2" {
		t.Fatalf("update: %s %v %v", keys(tracking), added, err)
	}
	rs, _ := YAMLVersions{Tracked: []string{"pb:0.42", "p1:0.0"}, Root: b.Root}.Releases(context.Background())
	if keys(rs) != "p1:0.0.2,pb:0.42.2" {
		t.Fatalf("index after update: %s", keys(rs))
	}
	var out bytes.Buffer
	if err := RunSeed(context.Background(), &out, YAMLVersions{Tracked: []string{"pb:0.42", "p1:0.0"}, Root: b.Root}, b); err != nil || !strings.Contains(out.String(), "pb:0.42.2    cached") {
		t.Fatalf("seed report: %v\n%s", err, out.String())
	}
}

// Against the real release hosts (LIVE_DISCOVERY=1): the latest patch of every minor of both lines, pinned.
func TestLiveDiscovery(t *testing.T) {
	if os.Getenv("LIVE_DISCOVERY") == "" {
		t.Skip("set LIVE_DISCOVERY=1 to query ganigeorgiev.com and GitHub")
	}
	latest, err := DefaultDiscovery().Latest(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range latest {
		t.Logf("%-12s %s", r.Key(), r.Assets["linux_amd64"].URL)
	}
	if len(latest) < 2 {
		t.Fatalf("too few releases: %s", keys(latest))
	}
}
