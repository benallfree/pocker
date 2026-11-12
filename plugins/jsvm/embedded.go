package jsvm

// POCKER: embedded mode (Config.HooksFS / Config.MigrationsFS). Hooks and migrations are read from an fs.FS, and
// require() under __hooks resolves inside a sealed virtual namespace that is never looked up on disk.

import (
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dop251/goja_nodejs/require"
)

// EmbeddedHooksDir is the value of __hooks in embedded mode (Config.HooksFS set). It is a virtual root: require()
// paths under it are served from Config.HooksFS and the disk is never consulted.
const EmbeddedHooksDir = "/pb_embedded/pb_hooks"

// embeddedHooksDir is EmbeddedHooksDir; tests point it at a real directory to prove the disk is ignored.
var embeddedHooksDir = EmbeddedHooksDir

// embeddedRequireRegistry returns a require registry sealed to fsys mounted at embeddedHooksDir.
//
// The resolver joins and cleans paths as text (no symlink evaluation, no disk). A base outside the virtual root,
// such as the working directory that .pb.js handlers are compiled under, is anchored at the root, so a relative
// require from a handler resolves under __hooks. The loader serves only paths under the root from fsys; everything
// else (absolute disk paths, `..` escapes, node_modules lookups) is "module not found".
func embeddedRequireRegistry(fsys fs.FS) *require.Registry {
	root := embeddedHooksDir

	resolver := func(base, name string) string {
		name = filepath.ToSlash(name)
		if path.IsAbs(name) {
			return path.Clean(name)
		}
		base = filepath.ToSlash(base)
		if base != root && !strings.HasPrefix(base, root+"/") {
			base = root
		}
		return path.Join(base, name)
	}

	loader := func(p string) ([]byte, error) {
		p = filepath.ToSlash(p)
		rel, ok := strings.CutPrefix(p, root+"/")
		if !ok || !fs.ValidPath(rel) || rel == "node_modules" || strings.HasPrefix(rel, "node_modules/") ||
			strings.Contains(rel, "/node_modules/") || strings.HasSuffix(rel, "/node_modules") {
			return nil, require.ModuleFileDoesNotExistError
		}
		info, err := fs.Stat(fsys, rel)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, require.ModuleFileDoesNotExistError
			}
			return nil, err
		}
		if info.IsDir() {
			return nil, require.ModuleFileDoesNotExistError
		}
		return fs.ReadFile(fsys, rel)
	}

	return require.NewRegistry(require.WithPathResolver(resolver), require.WithLoader(loader))
}

// fsFilesContent is filesContent for the root of an fs.FS: the direct files matching pattern and their content.
func fsFilesContent(fsys fs.FS, pattern string) (map[string][]byte, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string][]byte{}, nil
		}
		return nil, err
	}

	var exp *regexp.Regexp
	if pattern != "" {
		if exp, err = regexp.Compile(pattern); err != nil {
			return nil, err
		}
	}

	result := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() || (exp != nil && !exp.MatchString(e.Name())) {
			continue
		}
		raw, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		result[e.Name()] = raw
	}
	return result, nil
}
