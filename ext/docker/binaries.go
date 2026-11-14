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

// Release lines, the prefix of an instance's version: "pb:0.36.*" (upstream PocketBase) or "p1:0.0.*" (PocketOne).
// A version without a prefix is upstream.
const (
	LinePB = "pb"
	LineP1 = "p1"
)

// MinUpstream is the oldest upstream PocketBase version served: 0.22 and older are sunset.
const MinUpstream = "0.23.0"

// Release is one PocketBase or PocketOne release a docker host may run, pinned to its downloads and checksums.
type Release struct {
	Line    string           `json:"line"`           // LinePB or LineP1
	Version string           `json:"version"`        // e.g. "0.36.9"
	Base    string           `json:"base,omitempty"` // PocketOne: the upstream PocketBase version it's based on
	Assets  map[string]Asset `json:"assets"`         // by platform, e.g. "linux_amd64"
}

// Asset is a release's zip for one platform.
type Asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// Key is "pb:0.36.9" / "p1:0.0.1".
func (r Release) Key() string { return r.Line + ":" + r.Version }

// binaryName is the release's binary inside its zip, and the zip's prefix (pocketbase_<v>_<platform>.zip).
func binaryName(line string) string {
	if line == LineP1 {
		return "pocketone"
	}
	return "pocketbase"
}

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

// Pick returns the newest release in rs that satisfies want: "pb:0.36.*", "p1:0.0.1", or an upstream version without
// a prefix; exact versions and ranges ("0.36.*", "~0.36", "^0.30") both work. It is the one place that decides which
// release an instance runs.
func Pick(rs []Release, want string) (Release, error) {
	line, rng, err := ParseVersion(want)
	if err != nil {
		return Release{}, err
	}
	c, err := semver.NewConstraint(rng)
	if err != nil {
		return Release{}, fmt.Errorf("pocketbase version %q: %w", want, err)
	}
	var best *semver.Version
	var pick Release
	for _, r := range rs {
		if r.Line != line {
			continue
		}
		v, err := semver.StrictNewVersion(r.Version)
		if err != nil || !c.Check(v) || (line == LinePB && v.LessThan(semver.MustParse(MinUpstream))) {
			continue
		}
		if best == nil || v.GreaterThan(best) {
			best, pick = v, r
		}
	}
	if best != nil {
		return pick, nil
	}
	if line == LinePB {
		if sv, err := semver.NewVersion(strings.TrimSuffix(strings.TrimSuffix(rng, ".*"), ".x")); err == nil && sv.LessThan(semver.MustParse(MinUpstream)) {
			return Release{}, fmt.Errorf("PocketBase %s is no longer supported (0.22 and older are sunset; use 0.23 or newer)", rng)
		}
	}
	return Release{}, fmt.Errorf("PocketBase %q is not offered on this host", want)
}

// LatestPatches keeps, per line, the newest patch of each minor (upstream from MinUpstream on). It is the one place
// that culls a release list: discovery, `docker update` and a mothership's versions list all go through it.
func LatestPatches(rs []Release) []Release {
	min := semver.MustParse(MinUpstream)
	best := map[string]Release{}
	bestV := map[string]*semver.Version{}
	for _, r := range rs {
		v, err := semver.StrictNewVersion(r.Version)
		if err != nil || v.Prerelease() != "" || (r.Line == LinePB && v.LessThan(min)) {
			continue
		}
		k := fmt.Sprintf("%s:%d.%d", r.Line, v.Major(), v.Minor())
		if cur, ok := bestV[k]; !ok || v.GreaterThan(cur) {
			best[k], bestV[k] = r, v
		}
	}
	out := make([]Release, 0, len(best))
	for _, r := range best {
		out = append(out, r)
	}
	SortReleases(out)
	return out
}

// SortReleases orders releases by line, then version (oldest first).
func SortReleases(rs []Release) {
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Line != rs[j].Line {
			return rs[i].Line < rs[j].Line
		}
		a, ea := semver.NewVersion(rs[i].Version)
		b, eb := semver.NewVersion(rs[j].Version)
		if ea != nil || eb != nil {
			return rs[i].Version < rs[j].Version
		}
		return a.LessThan(b)
	})
}

// Binaries is a cache of release binaries, one per release, laid out as PocketHost's edges keep them: upstream at
// <Root>/<version>/<platform>/pocketbase (so an existing cache works as is), PocketOne at
// <Root>/p1/<version>/<platform>/pocketone. Fetch downloads a release and checks it against its sha256.
type Binaries struct {
	Root     string
	Platform string // e.g. "linux_amd64"; default linux_<GOARCH>
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

// Path is where a release's binary lives in the cache (whether or not it is there).
func (b *Binaries) Path(line, version string) string {
	if line == LineP1 {
		return filepath.Join(b.Root, "p1", version, b.platform(), "pocketone")
	}
	return filepath.Join(b.Root, version, b.platform(), "pocketbase")
}

// Has reports whether a release's binary is in the cache.
func (b *Binaries) Has(r Release) bool {
	_, err := os.Stat(b.Path(r.Line, r.Version))
	return err == nil
}

// Seed fetches every release in rs that isn't cached yet, one at a time, and returns the keys it added. It stops at
// the first failure (what it added so far stays cached).
func (b *Binaries) Seed(ctx context.Context, rs []Release) (added []string, err error) {
	for _, r := range rs {
		if b.Has(r) {
			continue
		}
		if err := b.Fetch(ctx, r); err != nil {
			return added, err
		}
		added = append(added, r.Key())
	}
	return added, nil
}

func (b *Binaries) lock(key string) *sync.Mutex {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.busy == nil {
		b.busy = map[string]*sync.Mutex{}
	}
	m, ok := b.busy[key]
	if !ok {
		m = &sync.Mutex{}
		b.busy[key] = m
	}
	return m
}

// Fetch downloads a release for this platform, checks its sha256 and puts its binary in the cache. A release
// already cached is left alone.
func (b *Binaries) Fetch(ctx context.Context, r Release) error {
	l := b.lock(r.Key())
	l.Lock()
	defer l.Unlock()
	path := b.Path(r.Line, r.Version)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	a, ok := r.Assets[b.platform()]
	if !ok || a.URL == "" || a.SHA256 == "" {
		return fmt.Errorf("%s has no %s build", r.Key(), b.platform())
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	c := b.Client
	if c == nil {
		c = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
	}
	res, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", r.Key(), err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s %d", r.Key(), a.URL, res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 256<<20))
	if err != nil {
		return fmt.Errorf("download %s: %w", r.Key(), err)
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != strings.ToLower(a.SHA256) {
		return fmt.Errorf("%s: checksum mismatch (got %x, expected %s)", r.Key(), got, a.SHA256)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("%s: %w", r.Key(), err)
	}
	name := binaryName(r.Line)
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
		tmp, err := os.CreateTemp(filepath.Dir(path), ".binary-*")
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
	return fmt.Errorf("%s: no %s binary in %s", r.Key(), name, a.URL)
}

// IndexFile is where `docker update` records the releases a host tracks, inside the binary cache.
const IndexFile = "releases.json"

// ReadIndex reads the releases recorded in a cache's index (none when there is no index yet).
func ReadIndex(root string) ([]Release, error) {
	b, err := os.ReadFile(filepath.Join(root, IndexFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rs []Release
	if err := json.Unmarshal(b, &rs); err != nil {
		return nil, fmt.Errorf("%s: %w", IndexFile, err)
	}
	return rs, nil
}

// WriteIndex records releases in a cache's index (written to a temp file and renamed, so readers never see half).
func WriteIndex(root string, rs []Release) error {
	SortReleases(rs)
	b, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(root, ".releases-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	tmp.Close()
	return os.Rename(tmp.Name(), filepath.Join(root, IndexFile))
}
