// Package jsembed runs an app's JS hooks and migrations from an fs.FS (for example files embedded in the binary)
// instead of pb_hooks and pb_migrations on disk:
//
//	jsvm.MustRegister(app, jsvm.Config{Extensions: []jsvm.Extension{jsembed.Hooks(hooksFS), jsembed.Migrations(migrationsFS)}})
//
// .pb.js files come only from hooksFS's root (HooksDir is ignored, nothing is written to disk, no watching) and
// migrations only from migrationsFS's root; list only the half you embed.
//
// require() under __hooks resolves inside a sealed virtual namespace, [Root], that is never looked up on disk: paths
// under it are served from hooksFS, a module's relative requires stay inside, a relative require from a .pb.js
// handler is anchored at __hooks, and everything else (absolute disk paths, `..` escapes, node_modules) is "module not
// found". JS that reads files itself ($os.readFile(__hooks + ...)) gets "no such file": the root doesn't exist on disk.
//
// They are jsvm extensions (HookFiles, Require and MigrationFiles methods); PocketBase itself is unchanged.
package jsembed

import (
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
	"github.com/dop251/goja_nodejs/require"
)

// Root is the value of __hooks with an embedded hooks FS. It is a virtual root: require() paths under it are served
// from the FS and the disk is never consulted.
const Root = "/pb_embedded/pb_hooks"

// root is Root; tests point it at a real directory to prove the disk is ignored.
var root = Root

// Hooks is a jsvm extension serving the hook files from fsys's root, with require() under __hooks sealed to fsys.
// HooksDir is then ignored, nothing is written to disk and hook watching is off.
func Hooks(fsys fs.FS) jsvm.Extension { return hooks{fsys} }

// Migrations is a jsvm extension serving the migration files from fsys's root; MigrationsDir is then ignored.
func Migrations(fsys fs.FS) jsvm.Extension { return migrations{fsys} }

type hooks struct{ fs fs.FS }

func (h hooks) HookFiles(pattern string) (map[string][]byte, error) {
	return filesContent(h.fs, pattern)
}
func (h hooks) Require() (string, *require.Registry) { return root, requireRegistry(h.fs) }

type migrations struct{ fs fs.FS }

func (m migrations) MigrationFiles(pattern string) (map[string][]byte, error) {
	return filesContent(m.fs, pattern)
}

// requireRegistry returns a require registry sealed to fsys mounted at root.
//
// The resolver joins and cleans paths as text (no symlink evaluation, no disk). A base outside the virtual root,
// such as the working directory that .pb.js handlers are compiled under, is anchored at the root, so a relative
// require from a handler resolves under __hooks. The loader serves only paths under the root from fsys; everything
// else (absolute disk paths, `..` escapes, node_modules lookups) is "module not found".
func requireRegistry(fsys fs.FS) *require.Registry {
	vroot := root

	resolver := func(base, name string) string {
		name = filepath.ToSlash(name)
		if path.IsAbs(name) {
			return path.Clean(name)
		}
		base = filepath.ToSlash(base)
		if base != vroot && !strings.HasPrefix(base, vroot+"/") {
			base = vroot
		}
		return path.Join(base, name)
	}

	loader := func(p string) ([]byte, error) {
		p = filepath.ToSlash(p)
		rel, ok := strings.CutPrefix(p, vroot+"/")
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

// filesContent returns the direct files at fsys's root matching pattern, with their content (like jsvm does for a
// directory).
func filesContent(fsys fs.FS, pattern string) (map[string][]byte, error) {
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
