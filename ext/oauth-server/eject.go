package oauthserver

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

//go:embed eject/oauth
var ejectFiles embed.FS

// EjectSubdir is where ejected templates go inside the pages folder: a _private folder, so ext/pages never serves
// them as routes; the server renders them with [PageData] as ctx.data.
const EjectSubdir = "_private/oauth"

// pagesDir is what ext/pages offers (duck-typed): its pb_pages folder.
type pagesDir interface{ Dir() string }

// PagesDir returns the app's pb_pages folder when ext/pages is registered.
func (s *Server) PagesDir() (string, bool) {
	p, ok := s.app.Store().Get("extPages").(pagesDir)
	if !ok {
		return "", false
	}
	return p.Dir(), true
}

// Eject writes the built-in pages as PocketPages templates into <pagesDir>/_private/oauth (sign-in, one-time code,
// consent, device code, done, error, and the shared head/foot/hidden partials). Existing files are refused, and
// nothing is written, unless force. It returns the files written.
func Eject(pagesDir string, force bool) ([]string, error) {
	var names []string
	err := fs.WalkDir(ejectFiles, "eject/oauth", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			names = append(names, filepath.Base(p))
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	dest := filepath.Join(pagesDir, filepath.FromSlash(EjectSubdir))
	if !force {
		var exists []string
		for _, n := range names {
			if _, err := os.Stat(filepath.Join(dest, n)); err == nil {
				exists = append(exists, filepath.Join(dest, n))
			}
		}
		if len(exists) > 0 {
			return nil, &ExistsError{Files: exists}
		}
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	var written []string
	for _, n := range names {
		b, err := ejectFiles.ReadFile("eject/oauth/" + n)
		if err != nil {
			return written, err
		}
		p := filepath.Join(dest, n)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return written, err
		}
		written = append(written, p)
	}
	return written, nil
}

// ExistsError: an eject would overwrite these files.
type ExistsError struct{ Files []string }

func (e *ExistsError) Error() string {
	return fmt.Sprintf("%d file(s) already exist (use --force to overwrite): %v", len(e.Files), e.Files)
}

var errNoPages = errors.New("this binary has no ext/pages, so ejected templates would never render; " +
	"register pages.MustRegister(app, ...) or customize with oauthserver.Config.Render")
