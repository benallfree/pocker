package upgrade

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Release is a GitHub release (only the fields this package uses).
type Release struct {
	Tag    string  `json:"tag_name"`
	Body   string  `json:"body"`
	Assets []Asset `json:"assets"`
}

// Asset is one file attached to a [Release].
type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

// Version is the release's version without the leading "v".
func (r *Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

func (r *Release) asset(name string) (*Asset, error) {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("release %s has no %s", r.Tag, name)
}

// FetchRelease fetches the latest release (tag "") or the release with the given tag ("0.0.3" or "v0.0.3").
func (c *Config) FetchRelease(ctx context.Context, tag string) (*Release, error) {
	path := "releases/latest"
	if tag != "" {
		path = "releases/tags/v" + strings.TrimPrefix(tag, "v")
	}
	url := fmt.Sprintf("%s/repos/%s/%s/%s", c.BaseURL, c.Owner, c.Repo, path)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	r := &Release{}
	if err := json.Unmarshal(body, r); err != nil {
		return nil, fmt.Errorf("decoding release info: %w", err)
	}
	if r.Tag == "" {
		return nil, errors.New("release info has no tag")
	}
	return r, nil
}

// DownloadArchive downloads the release archive for this platform into dir, checks it against the release's
// checksums.txt and returns its path.
func (c *Config) DownloadArchive(ctx context.Context, r *Release, goos, goarch, dir string) (string, error) {
	suffix := archiveSuffix(goos, goarch)
	if suffix == "" {
		return "", fmt.Errorf("no release builds for %s/%s", goos, goarch)
	}
	name := fmt.Sprintf("%s_%s%s", c.ArchivePrefix, r.Version(), suffix)
	a, err := r.asset(name)
	if err != nil {
		return "", err
	}
	sums, err := r.asset("checksums.txt")
	if err != nil {
		return "", err
	}
	sumsBody, err := c.get(ctx, sums.DownloadURL)
	if err != nil {
		return "", err
	}
	want, err := checksumFor(sumsBody, name)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, name)
	got, err := c.download(ctx, a.DownloadURL, dest)
	if err != nil {
		return "", err
	}
	if got != want {
		_ = os.Remove(dest)
		return "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", name, got, want)
	}
	return dest, nil
}

func (c *Config) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %d %s", url, res.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// download saves url to dest and returns its sha256 (hex).
func (c *Config) download(ctx context.Context, url, dest string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %d", url, res.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), res.Body); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), f.Close()
}

// checksumFor finds name in a goreleaser checksums.txt ("<sha256>  <name>" per line).
func checksumFor(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && fields[1] == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt has no entry for %s", name)
}

func archiveSuffix(goos, goarch string) string {
	switch goos + "/" + goarch {
	case "linux/amd64", "linux/arm64", "linux/ppc64le", "linux/s390x",
		"darwin/amd64", "darwin/arm64", "windows/amd64", "windows/arm64":
		return "_" + goos + "_" + goarch + ".zip"
	case "linux/arm":
		return "_linux_armv7.zip"
	}
	return ""
}

// compareVersions returns 1 if b is newer than a, -1 if a is newer, 0 if equal ("1.2.3" form, missing parts = 0).
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(as), len(bs)); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x < y {
			return 1
		}
		if x > y {
			return -1
		}
	}
	return 0
}
