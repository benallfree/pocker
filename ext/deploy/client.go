package deploy

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pockethost/pocketone/ext/deploy/internal/glob"
	"github.com/pockethost/pocketone/ext/deploy/nodemodules"
	"github.com/pockethost/pocketone/ext/remotes"
)

// API paths. A PocketOne remote serves PockerPrefix; PocketHost edges serve LegacyPrefix (the npm CLI's), which the client
// falls back to.
const (
	PockerPrefix = "/_pocker/deploy/v1"
	LegacyPrefix = "/_ph/deploy/v1"
)

// UploadBatchBytes is the raw size of one upload request (gzip shrinks it; Cloudflare allows 100 MB).
var UploadBatchBytes int64 = 32 << 20

// DefaultIncludes and DefaultExcludes are the npm CLI's deploy set.
var (
	DefaultIncludes = []string{"pb_*", "pb_*/**/*", "package.json", "bun.lockb", "bun.lock", "patches", "patches/**/*"}
	DefaultExcludes = []string{"pb_data", "pb_data/**/*"}
)

// LocalFile is one file of a deploy set.
type LocalFile struct {
	Path   string // logical path from the project root
	Hash   string // sha256 hex
	Size   int64
	Source string // real file on disk (differs from Path for node_modules entries)
}

// DeploySet is what a deploy would send.
type DeploySet struct {
	Files []LocalFile
	// Skipped: files the include patterns matched that aren't deployable (outside the code set, symlinks).
	Skipped []string
	// NodeModules is the collected production tree, nil when the deploy doesn't manage node_modules.
	NodeModules *nodemodules.Result
}

// CollectOptions pick the files.
type CollectOptions struct {
	Dir     string   // project root (default: the current folder)
	Include []string // default DefaultIncludes
	Exclude []string // added to DefaultExcludes
	// NodeModules: ship the production node_modules tree and let the remote manage its root node_modules. Nil:
	// when the project has a package.json.
	NodeModules *bool
}

// Collect lists and hashes the deploy set: files matching the include patterns and not the exclude patterns,
// within the code set (pb_* except pb_data, package.json, bun.lock(b), patches/), plus the production node_modules
// tree. Dotfiles ship (pb_public/.well-known); .git* files and symlinks don't.
func Collect(opts CollectOptions) (*DeploySet, error) {
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	include := opts.Include
	if len(include) == 0 {
		include = DefaultIncludes
	}
	exclude := append(slices.Clone(DefaultExcludes), opts.Exclude...)

	set := &DeploySet{}
	err = filepath.WalkDir(dir, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			if abs == dir {
				return err
			}
			return nil
		}
		rel, _ := filepath.Rel(dir, abs)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "node_modules" || strings.HasPrefix(name, ".git") || glob.Any(exclude, rel) || !glob.AnyUnder(include, rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !glob.Any(include, rel) || glob.Any(exclude, rel) {
			return nil
		}
		if !d.Type().IsRegular() || !isCodePath(rel) {
			set.Skipped = append(set.Skipped, rel)
			return nil
		}
		h, size, err := hashFile(abs)
		if err != nil {
			return err
		}
		set.Files = append(set.Files, LocalFile{Path: rel, Hash: h, Size: size, Source: abs})
		return nil
	})
	if err != nil {
		return nil, err
	}

	manage := opts.NodeModules
	if manage == nil {
		_, err := os.Stat(filepath.Join(dir, "package.json"))
		has := err == nil
		manage = &has
	}
	if *manage {
		nm, err := nodemodules.Collect(dir)
		if err != nil {
			return nil, err
		}
		set.NodeModules = nm
		for _, f := range nm.Files {
			h, size, err := hashFile(f.Source)
			if err != nil {
				return nil, err
			}
			set.Files = append(set.Files, LocalFile{Path: f.Path, Hash: h, Size: size, Source: f.Source})
		}
	}
	return set, nil
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Manifest maps each path to its hash, as the API expects.
func (s *DeploySet) Manifest() map[string]string {
	m := make(map[string]string, len(s.Files))
	for _, f := range s.Files {
		m[f.Path] = f.Hash
	}
	return m
}

// Describe returns human lines about the node_modules part, plus a warning for missing packages.
func (s *DeploySet) Describe() []string {
	nm := s.NodeModules
	if nm == nil {
		return nil
	}
	var lines []string
	if len(nm.Packages) == 0 {
		lines = append(lines, "node_modules: no packages (any on the remote will be removed)")
	} else {
		lines = append(lines, fmt.Sprintf("node_modules: %d package(s), %d file(s)", len(nm.Packages), len(nm.Files)))
	}
	if len(nm.Missing) > 0 {
		var parts []string
		for _, m := range nm.Missing {
			parts = append(parts, m.Name+" (required by "+m.RequiredBy+")")
		}
		lines = append(lines, fmt.Sprintf("%d required package(s) not installed: %s. Run your package manager's install first.",
			len(nm.Missing), strings.Join(parts, ", ")))
	}
	return lines
}

// CheckComplete fails when required packages are missing.
func (s *DeploySet) CheckComplete() error {
	if s.NodeModules != nil && len(s.NodeModules.Missing) > 0 {
		return fmt.Errorf("not deploying: %d required package(s) are missing from node_modules", len(s.NodeModules.Missing))
	}
	return nil
}

// WriteTo copies the deploy set into outDir (empty or new) as plain files: logical paths, symlinks resolved.
func (s *DeploySet) WriteTo(outDir string) error {
	if entries, err := os.ReadDir(outDir); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s must be empty or not exist yet", outDir)
	}
	for _, f := range s.Files {
		dest := filepath.Join(outDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := copyFile(f.Source, dest); err != nil {
			return err
		}
	}
	return nil
}

// EncodeBundle builds an upload body: "PHB1" + [32-byte sha256][uint32 BE length][bytes] per entry (gzip it).
func EncodeBundle(entries [][]byte) []byte {
	var b bytes.Buffer
	b.Write(bundleMagic)
	for _, data := range entries {
		sum := sha256.Sum256(data)
		b.Write(sum[:])
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(data)))
		b.Write(n[:])
		b.Write(data)
	}
	return b.Bytes()
}

// BatchBySize groups files into batches of at most max raw bytes (a larger single file gets its own batch).
func BatchBySize(files []LocalFile, max int64) [][]LocalFile {
	var out [][]LocalFile
	var cur []LocalFile
	var curBytes int64
	for _, f := range files {
		if len(cur) > 0 && curBytes+f.Size > max {
			out = append(out, cur)
			cur, curBytes = nil, 0
		}
		cur = append(cur, f)
		curBytes += f.Size
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// ---------------------------------------------------------------------------------------------------------------
// API client

// Client is the deploy API of one remote.
type Client struct {
	*remotes.Client
	// Prefix is the API path (PockerPrefix or LegacyPrefix).
	Prefix string
}

// NewClient finds the remote's deploy API: the path its /_pocker/whoami advertises, else the PocketHost path.
func NewClient(ctx context.Context, rc *remotes.Client) (*Client, error) {
	w, err := rc.Whoami(ctx)
	switch {
	case errors.Is(err, remotes.ErrNoWhoami):
		return &Client{Client: rc, Prefix: LegacyPrefix}, nil
	case err != nil:
		return nil, err
	}
	p := w.Features["deploy"]
	if p == "" {
		return nil, fmt.Errorf("%s doesn't offer a deploy API (no deploy plugin)", rc.Remote.URL)
	}
	return &Client{Client: rc, Prefix: strings.TrimSuffix(p, "/")}, nil
}

// Whoami is the deploy API's own whoami.
type Whoami struct {
	Instance struct {
		Id        string `json:"id"`
		Subdomain string `json:"subdomain"`
	} `json:"instance"`
	Via    string   `json:"via"`
	Token  *string  `json:"token"`
	Scopes []string `json:"scopes"`
}

func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	err := c.JSON(ctx, method, c.Prefix+path, in, out)
	var ae *remotes.APIError
	if errors.As(err, &ae) {
		switch ae.Status {
		case http.StatusUnauthorized:
			ae.Message = fmt.Sprintf("authentication failed (%s): %s", orStr(c.TokenSource, "no login"), ae.Message)
		case http.StatusNotFound:
			if path == "/whoami" || path == "/check" {
				ae.Message = fmt.Sprintf("%s: not found, or this login can't reach it (%s)", c.Remote.URL, ae.Message)
			}
		}
	}
	return err
}

func orStr(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// DeployWhoami checks the login can reach the deploy API.
func (c *Client) DeployWhoami(ctx context.Context) (*Whoami, error) {
	var w Whoami
	return &w, c.call(ctx, http.MethodGet, "/whoami", nil, &w)
}

// Check returns the hashes the remote doesn't have.
func (c *Client) Check(ctx context.Context, manifest map[string]string, nodeModules bool) ([]string, error) {
	var res struct {
		Missing []string `json:"missing"`
	}
	err := c.call(ctx, http.MethodPost, "/check", map[string]any{"files": manifest, "nodeModules": nodeModules}, &res)
	return res.Missing, err
}

// Upload sends one gzipped bundle.
func (c *Client) Upload(ctx context.Context, gz []byte) error {
	return c.call(ctx, http.MethodPost, "/blobs", gz, nil)
}

// CommitResult is the remote's answer to a commit.
type CommitResult struct {
	DeployId            *string  `json:"deployId"`
	FileCount           int      `json:"fileCount"`
	Written             []string `json:"written"`
	Deleted             []string `json:"deleted"`
	WrittenCount        int      `json:"writtenCount"`
	DeletedCount        int      `json:"deletedCount"`
	Unchanged           int      `json:"unchanged"`
	TotalBytes          int64    `json:"totalBytes"`
	ReplacedNodeModules bool     `json:"replacedNodeModules"`
	NothingChanged      bool     `json:"nothingChanged"`
	WasRunning          bool     `json:"wasRunning"`
	Restarted           bool     `json:"restarted"`
	StartError          string   `json:"startError,omitempty"`
}

// Commit asks the remote to swap the files in.
func (c *Client) Commit(ctx context.Context, body map[string]any) (*CommitResult, error) {
	var res CommitResult
	return &res, c.call(ctx, http.MethodPost, "/commit", body, &res)
}

// DeployOptions configure [Deploy].
type DeployOptions struct {
	CollectOptions
	Commit  string // recorded commit (default: $GITHUB_SHA, $CI_COMMIT_SHA, or git HEAD)
	Message string
	Source  string // recorded source (default: github-actions, ci, or pocketbase-cli)
	Verbose bool
	Log     func(string)
}

// Deploy sends the manifest, uploads only what the remote lacks, then commits. The remote swaps the files in and
// restarts the app when hooks, migrations or packages changed.
func Deploy(ctx context.Context, c *Client, opts DeployOptions) (*CommitResult, error) {
	log := opts.Log
	if log == nil {
		log = func(string) {}
	}
	set, err := Collect(opts.CollectOptions)
	if err != nil {
		return nil, err
	}
	for _, l := range set.Describe() {
		log(l)
	}
	if err := set.CheckComplete(); err != nil {
		return nil, err
	}
	if len(set.Skipped) > 0 {
		msg := fmt.Sprintf("Skipping %d file(s) outside the deployable set (pb_* except pb_data, package.json, bun.lock(b), patches/)", len(set.Skipped))
		if opts.Verbose {
			msg += ": " + strings.Join(set.Skipped, ", ")
		}
		log(msg)
	}
	manifest := set.Manifest()
	byHash := map[string]LocalFile{}
	for _, f := range set.Files {
		byHash[f.Hash] = f
	}
	log(fmt.Sprintf("Deploying %d files to %s (%s)", len(set.Files), c.Remote.URL, orStr(c.TokenSource, "no login")))

	uploadedCount, uploadedBytes := 0, int64(0)
	upload := func(hashes []string) error {
		var send []LocalFile
		seen := map[string]bool{}
		var bytesTotal int64
		for _, h := range hashes {
			if f, ok := byHash[h]; ok && !seen[h] {
				seen[h] = true
				send = append(send, f)
				bytesTotal += f.Size
			}
		}
		if len(send) == 0 {
			return nil
		}
		log(fmt.Sprintf("Uploading %d file(s) (%s)", len(send), humanBytes(bytesTotal)))
		for _, batch := range BatchBySize(send, UploadBatchBytes) {
			entries := make([][]byte, len(batch))
			for i, f := range batch {
				b, err := os.ReadFile(f.Source)
				if err != nil {
					return err
				}
				sum := sha256.Sum256(b)
				if hex.EncodeToString(sum[:]) != f.Hash {
					return fmt.Errorf("%s changed during deploy; run it again", f.Path)
				}
				entries[i] = b
			}
			var gz bytes.Buffer
			zw := gzip.NewWriter(&gz)
			zw.Write(EncodeBundle(entries))
			zw.Close()
			if err := c.Upload(ctx, gz.Bytes()); err != nil {
				return err
			}
			if opts.Verbose {
				for _, f := range batch {
					log("   " + f.Path)
				}
			}
		}
		uploadedCount += len(send)
		uploadedBytes += bytesTotal
		return nil
	}

	managesNM := set.NodeModules != nil
	missing, err := c.Check(ctx, manifest, managesNM)
	if err != nil {
		return nil, err
	}
	if err := upload(missing); err != nil {
		return nil, err
	}
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	body := map[string]any{
		"files": manifest, "nodeModules": managesNM,
		"commit": orStr(opts.Commit, detectCommit(dir)), "message": opts.Message,
		"source": orStr(opts.Source, detectSource()), "uploadedCount": uploadedCount, "uploadedBytes": uploadedBytes,
	}
	res, err := c.Commit(ctx, body)
	var ae *remotes.APIError
	// blobs can be garbage-collected by a concurrent deploy between upload and commit: upload again once
	if errors.As(err, &ae) && ae.Status == http.StatusConflict {
		if raw, ok := ae.Body["missing"].([]any); ok {
			var again []string
			for _, h := range raw {
				if s, ok := h.(string); ok {
					again = append(again, s)
				}
			}
			if err := upload(again); err != nil {
				return nil, err
			}
			body["uploadedCount"], body["uploadedBytes"] = uploadedCount, uploadedBytes
			res, err = c.Commit(ctx, body)
		}
	}
	if err != nil {
		return nil, err
	}
	if opts.Verbose {
		for _, p := range res.Written {
			log("   + " + p)
		}
		for _, p := range res.Deleted {
			log("   - " + p)
		}
	}
	if res.NothingChanged {
		log("Up to date: no changes.")
		return res, nil
	}
	id := ""
	if res.DeployId != nil {
		id = " " + *res.DeployId
	}
	restart := ""
	switch {
	case res.Restarted:
		restart = " Restarted."
	case res.StartError != "":
		restart = " The app failed to start: " + res.StartError + " (see 'pocketbase logs')."
	}
	log(fmt.Sprintf("Deployed%s: %d changed, %d deleted, %d unchanged.%s", id, res.WrittenCount, res.DeletedCount, res.Unchanged, restart))
	return res, nil
}

func detectCommit(dir string) string {
	for _, k := range []string{"GITHUB_SHA", "CI_COMMIT_SHA"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func detectSource() string {
	switch {
	case os.Getenv("GITHUB_ACTIONS") != "":
		return "github-actions"
	case os.Getenv("CI") != "":
		return "ci"
	}
	return "pocketbase-cli"
}

func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}
