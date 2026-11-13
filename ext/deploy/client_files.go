package deploy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pockethost/pocketone/ext/remotes"
)

// FileEntry is one entry of a remote listing.
type FileEntry struct {
	Path        string `json:"path"`
	IsDirectory bool   `json:"isDirectory"`
	Size        int64  `json:"size"`
	Mtime       int64  `json:"mtime"`
}

// ListFiles lists a remote folder (recursive: the whole subtree).
func (c *Client) ListFiles(ctx context.Context, path string, recursive bool) ([]FileEntry, bool, error) {
	q := url.Values{"path": {path}}
	if recursive {
		q.Set("recursive", "1")
	}
	var res struct {
		Entries   []FileEntry `json:"entries"`
		Truncated bool        `json:"truncated"`
	}
	err := c.call(ctx, http.MethodGet, "/files?"+q.Encode(), nil, &res)
	return res.Entries, res.Truncated, err
}

// Download writes a remote file to dest (via a temp file, then rename).
func (c *Client) Download(ctx context.Context, path, dest string) error {
	req, err := c.NewRequest(ctx, http.MethodGet, c.Prefix+"/files/raw?"+url.Values{"path": {path}}.Encode(), nil)
	if err != nil {
		return err
	}
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return remotes.DecodeResponse(res, nil)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".pull-tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, res.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

// DefaultPullPaths are pulled when `pull` names none: the code set's folders and root files.
var DefaultPullPaths = []string{"pb_hooks", "pb_migrations", "pb_public", "package.json"}

// PullOptions configure [Pull].
type PullOptions struct {
	Dir    string   // local project root
	Paths  []string // remote paths (default DefaultPullPaths)
	DryRun bool
	Log    func(string)
}

// Pull downloads remote files into the project, keeping their paths. pb_data is only pulled when asked for (and
// the remote refuses while the app runs).
func Pull(ctx context.Context, c *Client, opts PullOptions) (int, error) {
	log := opts.Log
	if log == nil {
		log = func(string) {}
	}
	paths := opts.Paths
	explicit := len(paths) > 0
	if !explicit {
		paths = DefaultPullPaths
	}
	n := 0
	for _, p := range paths {
		p = strings.Trim(p, "/")
		entries, truncated, err := c.ListFiles(ctx, p, true)
		if err != nil {
			if !explicit && remotes.IsStatus(err, http.StatusNotFound) {
				continue // a default folder the remote doesn't have
			}
			return n, fmt.Errorf("%s: %w", p, err)
		}
		if truncated {
			log(fmt.Sprintf("%s: the listing was truncated; pull subfolders separately", p))
		}
		for _, e := range entries {
			if e.IsDirectory {
				continue
			}
			if !explicit && (e.Path == "pb_data" || strings.HasPrefix(e.Path, "pb_data/")) {
				continue
			}
			dest := filepath.Join(opts.Dir, filepath.FromSlash(e.Path))
			if !insideDir(opts.Dir, dest) {
				return n, fmt.Errorf("refusing remote path %q", e.Path)
			}
			if opts.DryRun {
				log("   " + e.Path)
				n++
				continue
			}
			if err := c.Download(ctx, e.Path, dest); err != nil {
				return n, fmt.Errorf("%s: %w", e.Path, err)
			}
			if e.Mtime > 0 {
				t := time.UnixMilli(e.Mtime)
				_ = os.Chtimes(dest, t, t)
			}
			n++
		}
	}
	return n, nil
}

func insideDir(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// LogEntry is one line of the app's log stream.
type LogEntry struct {
	Message string `json:"message"`
	Stream  string `json:"stream"`
	Time    string `json:"time"`
}

// StreamLogs replays the last n log entries, then follows new ones until ctx ends or the stream closes.
func (c *Client) StreamLogs(ctx context.Context, n int, fn func(LogEntry)) error {
	req, err := c.NewRequest(ctx, http.MethodGet, c.Prefix+"/logs?n="+strconv.Itoa(n), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return remotes.DecodeResponse(res, nil)
	}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	event := ""
	var data strings.Builder
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if event == "log" || (event == "" && data.Len() > 0) {
				var e LogEntry
				if json.Unmarshal([]byte(data.String()), &e) == nil {
					fn(e)
				}
			}
			event = ""
			data.Reset()
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	return sc.Err()
}
