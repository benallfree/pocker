package pages

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

// hashCache remembers static files' fingerprints by path, size and modification time.
type hashCache struct {
	mu sync.Mutex
	m  map[string]hashEntry
}

type hashEntry struct {
	size  int64
	mtime time.Time
	hash  string
}

func (c *hashCache) reset() {
	c.mu.Lock()
	c.m = nil
	c.mu.Unlock()
}

// get returns the fingerprint of file (slash path relative to root), or "" if it can't be read.
func (c *hashCache) get(root *os.Root, file string) string {
	st, err := root.Stat(file)
	if err != nil || !st.Mode().IsRegular() {
		return ""
	}
	c.mu.Lock()
	if e, ok := c.m[file]; ok && e.size == st.Size() && e.mtime.Equal(st.ModTime()) {
		c.mu.Unlock()
		return e.hash
	}
	c.mu.Unlock()

	f, err := root.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	hash := hex.EncodeToString(h.Sum(nil))[:FingerprintLen]

	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]hashEntry{}
	}
	c.m[file] = hashEntry{size: st.Size(), mtime: st.ModTime(), hash: hash}
	c.mu.Unlock()
	return hash
}

// treeStamp is a cheap fingerprint of a folder tree: every walked entry's path, size and modification time. It
// changes whenever a file is added, removed, renamed or edited.
func treeStamp(dir string) (string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	h := sha256.New()
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p != "." && d.IsDir() && (d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) {
			return fs.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00", p, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// serveStatic writes a static file from pb_pages.
func (p *Pages) serveStatic(w http.ResponseWriter, r *http.Request, root *os.Root, route *Route, fingerprinted bool) error {
	f, err := root.Open(route.File)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}

	h := w.Header()
	if ct := mime.TypeByExtension(path.Ext(route.File)); ct != "" {
		h.Set("Content-Type", ct)
	}
	if hash := p.hashes.get(root, route.File); hash != "" {
		h.Set("ETag", `"`+hash+`"`)
	}
	if fingerprinted {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, path.Base(route.File), st.ModTime(), f)
	return nil
}

// Asset returns the URL for a static file in pb_pages with its fingerprint. from is the folder (slash path relative
// to pb_pages) the reference is made from; a path starting with "/" is relative to pb_pages. A path that isn't a
// static file is returned unchanged.
func (p *Pages) Asset(from, ref string) string {
	t := p.Table()
	if t == nil {
		return ref
	}
	target := ref
	if !strings.HasPrefix(ref, "/") {
		target = "/" + path.Join(urlDirOf(from), ref)
	}
	route, _, _ := t.Match(target, nil)
	if route == nil || route.Kind != KindStatic {
		return ref
	}
	root, err := os.OpenRoot(p.dir)
	if err != nil {
		return ref
	}
	defer root.Close()
	hash := p.hashes.get(root, route.File)
	if hash == "" {
		return ref
	}
	clean := strings.TrimSuffix(target, "/")
	i := strings.LastIndexByte(clean, '/')
	return clean[:i+1] + fingerprint(clean[i+1:], hash)
}

// urlDirOf turns a physical folder (slash path relative to pb_pages, "." for the top) into its URL folder: groups
// dropped.
func urlDirOf(dir string) string {
	if dir == "." || dir == "" {
		return ""
	}
	var keep []string
	for _, part := range strings.Split(dir, "/") {
		if !isGroup(part) {
			keep = append(keep, part)
		}
	}
	return strings.Join(keep, "/")
}
