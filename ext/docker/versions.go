package docker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// VersionsSource says which releases a docker host serves, each pinned with its downloads and checksums. Built in:
// [YAMLVersions] (a host without a control plane); a mothership bridge provides the fleet's list.
type VersionsSource interface {
	Releases(ctx context.Context) ([]Release, error)
}

// YAMLVersions serves the `docker.versions` minors of pocketbase.yaml: for each tracked minor ("pb:0.36", "p1:0.0"),
// the latest patch recorded in the cache's index by [Update]. It reads only local files, never the network.
type YAMLVersions struct {
	Tracked []string // e.g. ["pb:0.36", "p1:0.0"]; "pb:0.36.*" works too
	Root    string   // the binary cache (its index lives there)
}

// Releases implements VersionsSource.
func (y YAMLVersions) Releases(context.Context) ([]Release, error) {
	all, err := ReadIndex(y.Root)
	if err != nil {
		return nil, err
	}
	var out []Release
	for _, r := range LatestPatches(all) {
		if y.tracks(r) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (y YAMLVersions) tracks(r Release) bool {
	for _, t := range y.Tracked {
		line, minor, err := trackedMinor(t)
		if err == nil && line == r.Line && minorOf(r.Version) == minor {
			return true
		}
	}
	return false
}

// trackedMinor parses "pb:0.36", "pb:0.36.*" or "0.36" into its line and "0.36".
func trackedMinor(t string) (line, minor string, err error) {
	line, v, err := ParseVersion(t)
	if err != nil {
		return "", "", err
	}
	v = strings.TrimSuffix(strings.TrimSuffix(v, ".*"), ".x")
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("docker.versions: %q is not a minor (e.g. pb:0.36, p1:0.0)", t)
	}
	return line, parts[0] + "." + parts[1], nil
}

func minorOf(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return version
	}
	return parts[0] + "." + parts[1]
}

// Discovery finds the releases of upstream PocketBase and of PocketOne and pins them to their downloads and checksums,
// the way PocketHost's Node edge does: tags from the projects' public git refs, downloads from the first host that
// has the release (upstream: ganigeorgiev.com, then the archived GitHub), sha256 from each release's checksums.txt.
type Discovery struct {
	Client        *http.Client
	Platforms     []string // default linux_amd64, linux_arm64
	UpstreamRefs  string   // git refs URL listing upstream tags
	UpstreamHosts []string // download URL templates, tried in order ({version}, {file})
	PocketOneRefs string
	PocketOneHost string
}

// DefaultDiscovery is upstream PocketBase on ganigeorgiev.com (then GitHub) and PocketOne on GitHub.
func DefaultDiscovery() *Discovery {
	return &Discovery{
		Platforms:    []string{"linux_amd64", "linux_arm64"},
		UpstreamRefs: "https://ganigeorgiev.com/git/pocketbase/pocketbase/info/refs?service=git-upload-pack",
		UpstreamHosts: []string{
			"https://ganigeorgiev.com/meta/pocketbase/pocketbase/download/v{version}/{file}",
			"https://github.com/pocketbase/pocketbase/releases/download/v{version}/{file}",
		},
		PocketOneRefs: "https://github.com/pockethost/pocketone.git/info/refs?service=git-upload-pack",
		PocketOneHost: "https://github.com/pockethost/pocketone/releases/download/v{version}/{file}",
	}
}

// tagRe matches a stable release tag (refs/tags/v0.42.2, or its peeled form ^{}); a pre-release (v0.43.0-rc1) doesn't
// match because the version must end at whitespace, a NUL or the end of the text.
var tagRe = regexp.MustCompile(`refs/tags/v([0-9]+\.[0-9]+\.[0-9]+)(\^\{\})?(\s|\x00|$)`)

// Tags lists a line's stable release versions.
func (d *Discovery) Tags(ctx context.Context, line string) ([]string, error) {
	u := d.UpstreamRefs
	if line == LineP1 {
		u = d.PocketOneRefs
	}
	body, err := d.get(ctx, u, 2<<20)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range tagRe.FindAllStringSubmatch(string(body), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out, nil
}

// Latest returns the latest patch of each minor of both lines (upstream from MinUpstream on), pinned. tracked, if
// not empty, limits it to those minors (e.g. ["pb:0.36", "p1:0.0"]).
func (d *Discovery) Latest(ctx context.Context, tracked []string) ([]Release, error) {
	var candidates []Release
	for _, line := range []string{LinePB, LineP1} {
		if !tracksLine(tracked, line) {
			continue // e.g. `update p1:0.0` doesn't depend on the upstream host
		}
		tags, err := d.Tags(ctx, line)
		if err != nil {
			return nil, fmt.Errorf("%s tags: %w", line, err)
		}
		for _, v := range tags {
			candidates = append(candidates, Release{Line: line, Version: v})
		}
	}
	latest := LatestPatches(candidates)
	if len(tracked) > 0 {
		y := YAMLVersions{Tracked: tracked}
		var keep []Release
		for _, r := range latest {
			if y.tracks(r) {
				keep = append(keep, r)
			}
		}
		latest = keep
	}
	out := make([]Release, 0, len(latest))
	for _, r := range latest {
		pinned, err := d.Pin(ctx, r.Line, r.Version)
		if err != nil {
			return nil, err
		}
		out = append(out, pinned)
	}
	return out, nil
}

// tracksLine reports whether tracked (empty = everything) includes a minor of line.
func tracksLine(tracked []string, line string) bool {
	if len(tracked) == 0 {
		return true
	}
	for _, t := range tracked {
		if l, _, err := trackedMinor(t); err == nil && l == line {
			return true
		}
	}
	return false
}

// Pin finds where a release is hosted and records its zips' sha256 for every platform from its checksums.txt.
func (d *Discovery) Pin(ctx context.Context, line, version string) (Release, error) {
	hosts := d.UpstreamHosts
	if line == LineP1 {
		hosts = []string{d.PocketOneHost}
	}
	name := binaryName(line)
	platforms := d.Platforms
	if len(platforms) == 0 {
		platforms = []string{"linux_amd64", "linux_arm64"}
	}
	url := func(tmpl, file string) string {
		return strings.NewReplacer("{version}", version, "{file}", file).Replace(tmpl)
	}
	for _, tmpl := range hosts {
		sums, err := d.get(ctx, url(tmpl, "checksums.txt"), 1<<20)
		if err != nil {
			continue // not on this host
		}
		byFile := map[string]string{}
		sc := bufio.NewScanner(strings.NewReader(string(sums)))
		for sc.Scan() {
			if f := strings.Fields(sc.Text()); len(f) == 2 {
				byFile[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
			}
		}
		r := Release{Line: line, Version: version, Assets: map[string]Asset{}}
		for _, p := range platforms {
			file := fmt.Sprintf("%s_%s_%s.zip", name, version, p)
			if sum := byFile[file]; sum != "" {
				r.Assets[p] = Asset{URL: url(tmpl, file), SHA256: sum}
			}
		}
		if len(r.Assets) == 0 {
			return Release{}, fmt.Errorf("%s:%s: checksums.txt lists no %v builds", line, version, platforms)
		}
		return r, nil
	}
	return Release{}, fmt.Errorf("%s:%s: no host has its checksums.txt", line, version)
}

// get fetches a small document, trying up to three times (release hosts can be briefly slow); a 404 isn't retried.
func (d *Discovery) get(ctx context.Context, u string, max int64) ([]byte, error) {
	var last error
	for attempt := 1; attempt <= 3; attempt++ {
		b, status, err := d.getOnce(ctx, u, max)
		if err == nil {
			return b, nil
		}
		last = err
		if status == http.StatusNotFound || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}
	return nil, last
}

func (d *Discovery) getOnce(ctx context.Context, u string, max int64) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	c := d.Client
	if c == nil {
		c = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, res.StatusCode, fmt.Errorf("GET %s: %d", u, res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, max))
	return b, res.StatusCode, err
}

// Update is `docker update` for a host without a control plane: it finds the latest patch of each tracked minor,
// records them in the cache's index (keeping other entries), and seeds them. It returns the releases now tracked and
// the keys it downloaded.
func Update(ctx context.Context, d *Discovery, b *Binaries, tracked []string) (tracking []Release, added []string, err error) {
	if len(tracked) == 0 {
		return nil, nil, fmt.Errorf("docker.versions is empty: nothing to track")
	}
	latest, err := d.Latest(ctx, tracked)
	if err != nil {
		return nil, nil, err
	}
	index, err := ReadIndex(b.Root)
	if err != nil {
		return nil, nil, err
	}
	byKey := map[string]Release{}
	for _, r := range index {
		byKey[r.Key()] = r
	}
	for _, r := range latest {
		byKey[r.Key()] = r
	}
	merged := make([]Release, 0, len(byKey))
	for _, r := range byKey {
		merged = append(merged, r)
	}
	if err := WriteIndex(b.Root, merged); err != nil {
		return nil, nil, err
	}
	added, err = b.Seed(ctx, latest)
	return latest, added, err
}

// Seed is `docker seed`: it downloads whatever the source lists that the cache doesn't have yet.
func Seed(ctx context.Context, src VersionsSource, b *Binaries) (listed []Release, added []string, err error) {
	listed, err = src.Releases(ctx)
	if err != nil {
		return nil, nil, err
	}
	added, err = b.Seed(ctx, listed)
	return listed, added, err
}
