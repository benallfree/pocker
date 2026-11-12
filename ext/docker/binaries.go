package docker

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/semver/v3"
)

// Manifest lists the PocketBase releases an edge may download: where each is hosted and the sha256 of its zips.
// Same format as PocketHost's versions.json:
//
//	{"hosts": {"github": "https://github.com/pocketbase/pocketbase/releases/download/v{version}/{file}"},
//	 "versions": {"0.22.21": {"host": "github", "sha256": {"linux_amd64": "…", "linux_arm64": "…"}}}}
type Manifest struct {
	Hosts    map[string]string        `json:"hosts"`
	Versions map[string]ManifestEntry `json:"versions"`
}

// ManifestEntry is one release in a [Manifest].
type ManifestEntry struct {
	Host   string            `json:"host"`
	SHA256 map[string]string `json:"sha256"` // by platform, e.g. "linux_amd64"
}

// LoadManifest reads a manifest file.
func LoadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, nil
}

// Binaries is a cache of official PocketBase binaries, one per version, laid out as PocketHost's edges keep them
// (<Root>/<version>/<platform>/pocketbase), so an existing cache works as is. Versions missing from the cache are
// downloaded on first use from the manifest and checked against its sha256.
type Binaries struct {
	Root     string
	Manifest *Manifest // nil = cached versions only
	Platform string    // e.g. "linux_amd64"; default linux_<GOARCH>
	Client   *http.Client

	mu   sync.Mutex
	busy map[string]*sync.Mutex
}

func (b *Binaries) platform() string {
	if b.Platform != "" {
		return b.Platform
	}
	return "linux_" + runtime.GOARCH
}

// Path is where a version's binary lives in the cache (whether or not it is there).
func (b *Binaries) Path(version string) string {
	return filepath.Join(b.Root, version, b.platform(), "pocketbase")
}

// Versions lists the versions available (cached, or downloadable for this platform), newest first.
func (b *Binaries) Versions() []string {
	seen := map[string]bool{}
	var list []*semver.Version
	add := func(v string) {
		sv, err := semver.StrictNewVersion(v)
		if err != nil || sv.Prerelease() != "" || seen[v] {
			return
		}
		seen[v] = true
		list = append(list, sv)
	}
	if entries, err := os.ReadDir(b.Root); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				if _, err := os.Stat(b.Path(e.Name())); err == nil {
					add(e.Name())
				}
			}
		}
	}
	if b.Manifest != nil {
		for v, e := range b.Manifest.Versions {
			if e.SHA256[b.platform()] != "" {
				add(v)
			}
		}
	}
	sort.Sort(sort.Reverse(semver.Collection(list)))
	out := make([]string, len(list))
	for i, v := range list {
		out[i] = v.Original()
	}
	return out
}

// Match returns the newest available version satisfying want: an exact version ("0.22.21") or a range ("0.22.*",
// "~0.22", "^0.36", ">=0.30 <0.40").
func (b *Binaries) Match(want string) (string, error) {
	want = strings.TrimPrefix(strings.TrimSpace(want), "v")
	c, err := semver.NewConstraint(want)
	if err != nil {
		return "", fmt.Errorf("pocketbase version %q: %w", want, err)
	}
	for _, v := range b.Versions() {
		if c.Check(semver.MustParse(v)) {
			return v, nil
		}
	}
	return "", fmt.Errorf("no PocketBase version satisfying %q", want)
}

// Resolve matches want and returns the version and the path of its binary, downloading it when needed.
func (b *Binaries) Resolve(ctx context.Context, want string) (version, path string, err error) {
	version, err = b.Match(want)
	if err != nil {
		return "", "", err
	}
	path = b.Path(version)
	if _, err := os.Stat(path); err == nil {
		return version, path, nil
	}
	return version, path, b.download(ctx, version)
}

func (b *Binaries) lock(version string) *sync.Mutex {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.busy == nil {
		b.busy = map[string]*sync.Mutex{}
	}
	m, ok := b.busy[version]
	if !ok {
		m = &sync.Mutex{}
		b.busy[version] = m
	}
	return m
}

func (b *Binaries) download(ctx context.Context, version string) error {
	l := b.lock(version)
	l.Lock()
	defer l.Unlock()
	path := b.Path(version)
	if _, err := os.Stat(path); err == nil {
		return nil // another request downloaded it meanwhile
	}
	if b.Manifest == nil {
		return fmt.Errorf("pocketbase %s is not cached and there is no manifest to download it from", version)
	}
	e, ok := b.Manifest.Versions[version]
	if !ok {
		return fmt.Errorf("pocketbase %s is not in the manifest", version)
	}
	sum := e.SHA256[b.platform()]
	tmpl := b.Manifest.Hosts[e.Host]
	if sum == "" || tmpl == "" {
		return fmt.Errorf("pocketbase %s has no %s build (or no host) in the manifest", version, b.platform())
	}
	file := fmt.Sprintf("pocketbase_%s_%s.zip", version, b.platform())
	u := strings.NewReplacer("{version}", version, "{file}", file).Replace(tmpl)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	c := b.Client
	if c == nil {
		c = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
	}
	res, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("download pocketbase %s: %w", version, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download pocketbase %s: %s %d", version, u, res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 256<<20))
	if err != nil {
		return fmt.Errorf("download pocketbase %s: %w", version, err)
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != strings.ToLower(sum) {
		return fmt.Errorf("pocketbase %s: checksum mismatch (got %x, manifest %s)", version, got, sum)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("pocketbase %s: %w", version, err)
	}
	for _, f := range zr.File {
		if f.Name != "pocketbase" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(path), ".pocketbase-*")
		if err != nil {
			return err
		}
		if _, err := io.Copy(tmp, io.LimitReader(rc, 512<<20)); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return err
		}
		tmp.Close()
		if err := os.Chmod(tmp.Name(), 0o755); err != nil {
			os.Remove(tmp.Name())
			return err
		}
		return os.Rename(tmp.Name(), path)
	}
	return fmt.Errorf("pocketbase %s: no pocketbase binary in %s", version, file)
}
