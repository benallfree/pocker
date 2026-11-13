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

// Manifest lists the releases an edge may download: where each is hosted and the sha256 of its zips. Two release
// lines: "versions" are upstream PocketBase (PocketHost's versions.json is a valid manifest as is), "pocketone" are PocketOne
// releases, which have their own version numbers and note the upstream version they are based on.
//
//	{"hosts": {"github": "https://github.com/pocketbase/pocketbase/releases/download/v{version}/{file}",
//	           "pocketone": "https://github.com/pockethost/pocketone/releases/download/v{version}/{file}"},
//	 "versions": {"0.36.9": {"host": "github", "sha256": {"linux_amd64": "…", "linux_arm64": "…"}}},
//	 "pocketone": {"0.0.1": {"host": "pocketone", "base": "0.41.1", "sha256": {"linux_amd64": "…"}}}}
type Manifest struct {
	Hosts    map[string]string        `json:"hosts"`
	Versions map[string]ManifestEntry `json:"versions"`
	PocketOne map[string]ManifestEntry `json:"pocketone"`
}

// ManifestEntry is one release in a [Manifest].
type ManifestEntry struct {
	Host   string            `json:"host"`
	Base   string            `json:"base,omitempty"` // PocketOne: the upstream PocketBase version it is based on
	SHA256 map[string]string `json:"sha256"`         // by platform, e.g. "linux_amd64"
}

// Release lines, the prefix of an instance's version: "pb:0.36.*" (upstream PocketBase) or "p1:0.0.*" (PocketOne).
// A version without a prefix is upstream.
const (
	LinePB = "pb"
	LineP1 = "p1"
)

// MinUpstream is the oldest upstream PocketBase version served: 0.22 and older are sunset.
const MinUpstream = "0.23.0"

// ParseVersion splits "p1:0.0.*" into its line and version (range); no prefix = upstream.
func ParseVersion(v string) (line, want string, err error) {
	v = strings.TrimSpace(v)
	line, want = LinePB, v
	if i := strings.Index(v, ":"); i >= 0 {
		line, want = v[:i], v[i+1:]
	}
	if line != LinePB && line != LineP1 {
		return "", "", fmt.Errorf("pocketbase version %q: unknown release line %q (use pb: or p1:)", v, line)
	}
	return line, strings.TrimPrefix(strings.TrimSpace(want), "v"), nil
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

// Path is where a release's binary lives in the cache (whether or not it is there): upstream at
// <Root>/<version>/<platform>/pocketbase (PocketHost's existing layout), PocketOne at <Root>/ce/<version>/<platform>/….
func (b *Binaries) Path(line, version string) string {
	if line == LineP1 {
		return filepath.Join(b.Root, "p1", version, b.platform(), "pocketone")
	}
	return filepath.Join(b.Root, version, b.platform(), "pocketbase")
}

func (b *Binaries) entries(line string) map[string]ManifestEntry {
	if b.Manifest == nil {
		return nil
	}
	if line == LineP1 {
		return b.Manifest.PocketOne
	}
	return b.Manifest.Versions
}

// Versions lists a line's available versions (cached, or downloadable for this platform), newest first. Upstream
// versions older than [MinUpstream] are left out.
func (b *Binaries) Versions(line string) []string {
	min := semver.MustParse(MinUpstream)
	seen := map[string]bool{}
	var list []*semver.Version
	add := func(v string) {
		sv, err := semver.StrictNewVersion(v)
		if err != nil || sv.Prerelease() != "" || seen[v] || (line == LinePB && sv.LessThan(min)) {
			return
		}
		seen[v] = true
		list = append(list, sv)
	}
	dir := b.Root
	if line == LineP1 {
		dir = filepath.Join(b.Root, "p1")
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				if _, err := os.Stat(b.Path(line, e.Name())); err == nil {
					add(e.Name())
				}
			}
		}
	}
	for v, e := range b.entries(line) {
		if e.SHA256[b.platform()] != "" {
			add(v)
		}
	}
	sort.Sort(sort.Reverse(semver.Collection(list)))
	out := make([]string, len(list))
	for i, v := range list {
		out[i] = v.Original()
	}
	return out
}

// Match returns the line and newest available version satisfying want: "pb:0.36.*", "p1:0.0.1", or an upstream
// version without a prefix; exact versions and ranges ("0.36.*", "~0.36", "^0.30", ">=0.30 <0.40") both work.
func (b *Binaries) Match(want string) (line, version string, err error) {
	line, rng, err := ParseVersion(want)
	if err != nil {
		return "", "", err
	}
	c, err := semver.NewConstraint(rng)
	if err != nil {
		return "", "", fmt.Errorf("pocketbase version %q: %w", want, err)
	}
	for _, v := range b.Versions(line) {
		if c.Check(semver.MustParse(v)) {
			return line, v, nil
		}
	}
	if line == LinePB {
		if sv, err := semver.NewVersion(strings.TrimSuffix(strings.TrimSuffix(rng, ".*"), ".x")); err == nil && sv.LessThan(semver.MustParse(MinUpstream)) {
			return "", "", fmt.Errorf("PocketBase %s is no longer supported (0.22 and older are sunset; use 0.23 or newer)", rng)
		}
	}
	return "", "", fmt.Errorf("no PocketBase version satisfying %q", want)
}

// Resolve matches want and returns its line, version and the path of its binary, downloading it when needed.
func (b *Binaries) Resolve(ctx context.Context, want string) (line, version, path string, err error) {
	line, version, err = b.Match(want)
	if err != nil {
		return "", "", "", err
	}
	path = b.Path(line, version)
	if _, err := os.Stat(path); err == nil {
		return line, version, path, nil
	}
	return line, version, path, b.download(ctx, line, version)
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

func (b *Binaries) download(ctx context.Context, line, version string) error {
	l := b.lock(line + ":" + version)
	l.Lock()
	defer l.Unlock()
	path := b.Path(line, version)
	if _, err := os.Stat(path); err == nil {
		return nil // another request downloaded it meanwhile
	}
	if b.Manifest == nil {
		return fmt.Errorf("pocketbase %s is not cached and there is no manifest to download it from", version)
	}
	e, ok := b.entries(line)[version]
	if !ok {
		return fmt.Errorf("pocketbase %s:%s is not in the manifest", line, version)
	}
	sum := e.SHA256[b.platform()]
	tmpl := b.Manifest.Hosts[e.Host]
	if sum == "" || tmpl == "" {
		return fmt.Errorf("pocketbase %s has no %s build (or no host) in the manifest", version, b.platform())
	}
	name := "pocketbase" // the release's binary, and its zip's prefix: pocketbase_<v>_<platform>.zip / pocketone_…
	if line == LineP1 {
		name = "pocketone"
	}
	file := fmt.Sprintf("%s_%s_%s.zip", name, version, b.platform())
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
		if f.Name != name {
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
	return fmt.Errorf("pocketbase %s: no %s binary in %s", version, name, file)
}
