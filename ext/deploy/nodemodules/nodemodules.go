// Package nodemodules collects a project's production node_modules tree as a flat list of files, so it can be
// deployed as plain files (the remote never runs npm install). It's a port of the pockethost npm CLI's
// nodeModules.ts and makes the same choices:
//
//   - Starts from dependencies + optionalDependencies in the project's package.json (never devDependencies), then
//     follows each package's dependencies, optionalDependencies and resolvable peerDependencies.
//   - Resolves packages the way Node does: walk up from the requiring package's real folder through node_modules
//     folders. That covers hoisted npm/bun installs, pnpm's .pnpm store and workspace links.
//   - Symlinks are followed and disappear: every entry points at a real file (Source) under a logical Path.
//   - npm layout: a package goes to node_modules/<name> unless another version is already visible to its requirer;
//     then it nests (node_modules/a/node_modules/<name>).
package nodemodules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/pockethost/pocketone/ext/deploy/internal/glob"
)

// File is one file of the tree.
type File struct {
	// Path is the logical POSIX path from the project root, e.g. node_modules/marked/lib/marked.cjs.
	Path string
	// Source is the real file on disk (symlinks resolved).
	Source string
}

// Package is one placed package.
type Package struct {
	Name, Version string
	Path          string // logical folder, e.g. node_modules/marked
	Source        string // real folder
}

// Missing is a required dependency that isn't installed.
type Missing struct {
	Name, RequiredBy string
}

// Result is the collected tree.
type Result struct {
	Files    []File
	Packages []Package
	// Missing: required dependencies not found on disk (run the package manager's install first).
	Missing []Missing
}

type packageJSON struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Files                []string          `json:"files"`
	Dependencies         map[string]string `json:"dependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
}

func readPackageJSON(dir string) packageJSON {
	var p packageJSON
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err == nil {
		_ = json.Unmarshal(b, &p)
	}
	return p
}

// ResolvePackageDir is Node's lookup: <dir>/node_modules/<name> for fromDir and each ancestor. It returns the real
// path, or "".
func ResolvePackageDir(name, fromDir string) string {
	for dir := fromDir; ; {
		candidate := filepath.Join(dir, "node_modules", filepath.FromSlash(name))
		if _, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil {
			if real, err := filepath.EvalSymlinks(candidate); err == nil {
				return real
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// isLinkedSource: a package outside any node_modules folder is a local link (workspace, file:, link:).
func isLinkedSource(realDir string) bool {
	return !slices.Contains(strings.Split(filepath.ToSlash(realDir), "/"), "node_modules")
}

func isSkippedName(name string) bool {
	return name == "node_modules" || strings.HasPrefix(name, ".git")
}

// walkPackage lists every file of a package folder (relative, POSIX), following symlinks, skipping nested
// node_modules and .git*.
func walkPackage(realDir string) []string {
	var out []string
	seen := map[string]bool{}
	var walk func(dir, rel string)
	walk = func(dir, rel string) {
		real, err := filepath.EvalSymlinks(dir)
		if err != nil || seen[real] {
			return
		}
		seen[real] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			if isSkippedName(name) {
				continue
			}
			abs := filepath.Join(dir, name)
			st, err := os.Stat(abs) // follows symlinks
			if err != nil {
				continue // dangling symlink
			}
			childRel := name
			if rel != "" {
				childRel = rel + "/" + name
			}
			if st.IsDir() {
				walk(abs, childRel)
			} else if st.Mode().IsRegular() {
				out = append(out, childRel)
			}
		}
	}
	walk(realDir, "")
	return out
}

var readmeRE = regexp.MustCompile(`(?i)^(readme|license|licence)(\.|$)`)

// linkedPackageFiles: what a linked (workspace) package would publish: package.json, README/LICENSE and its
// "files" entries. Without "files", the whole folder.
func linkedPackageFiles(realDir string, pkg packageJSON) []string {
	all := walkPackage(realDir)
	if len(pkg.Files) == 0 {
		return all
	}
	var patterns []string
	for _, f := range pkg.Files {
		clean := strings.TrimSuffix(strings.TrimPrefix(f, "./"), "/")
		patterns = append(patterns, clean, clean+"/**")
	}
	var out []string
	for _, f := range all {
		if glob.Any(patterns, f) || f == "package.json" || readmeRE.MatchString(f) {
			out = append(out, f)
		}
	}
	return out
}

func depNames(pkg packageJSON, isRoot bool) (required, optional []string) {
	for n := range pkg.Dependencies {
		required = append(required, n)
	}
	for n := range pkg.OptionalDependencies {
		optional = append(optional, n)
	}
	if !isRoot {
		for n := range pkg.PeerDependencies {
			optional = append(optional, n)
		}
	}
	// JS objects keep insertion order; Go maps don't. Sorting keeps placement deterministic.
	sort.Strings(required)
	sort.Strings(optional)
	return required, optional
}

// Collect gathers the production node_modules tree of the project in projectDir.
func Collect(projectDir string) (*Result, error) {
	rootDir, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		return nil, err
	}
	rootDir, _ = filepath.Abs(rootDir)
	placed := map[string]string{} // logical dir -> real dir
	res := &Result{}

	// lookup: the first package named name that Node would find requiring from logical dir from
	lookup := func(name, from string) (logical, real string, ok bool) {
		dir := from
		for {
			candidate := "node_modules/" + name
			if dir != "" {
				candidate = dir + "/node_modules/" + name
			}
			if r, found := placed[candidate]; found {
				return candidate, r, true
			}
			if dir == "" {
				return "", "", false
			}
			// step up past this package's node_modules/<name> (scoped names have two segments)
			if idx := strings.LastIndex(dir, "/node_modules/"); idx >= 0 {
				dir = dir[:idx]
			} else {
				dir = ""
			}
		}
	}

	type item struct {
		logical, real string
		isRoot        bool
	}
	// breadth-first so shallow dependencies claim the top level
	queue := []item{{"", rootDir, true}}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		pkg := readPackageJSON(it.real)
		required, optional := depNames(pkg, it.isRoot)
		requiredBy := "package.json"
		if !it.isRoot {
			requiredBy = pkg.Name
			if requiredBy == "" {
				requiredBy = it.logical
			}
		}
		for _, group := range []struct {
			names    []string
			optional bool
		}{{required, false}, {optional, true}} {
			for _, name := range group.names {
				depReal := ResolvePackageDir(name, it.real)
				if depReal == "" {
					if !group.optional {
						res.Missing = append(res.Missing, Missing{Name: name, RequiredBy: requiredBy})
					}
					continue
				}
				_, foundReal, found := lookup(name, it.logical)
				if found && foundReal == depReal {
					continue
				}
				target := "node_modules/" + name // nothing visible: hoist
				if found {
					// another version is visible: nest under the requirer
					if it.logical != "" {
						target = it.logical + "/node_modules/" + name
					}
				}
				if _, taken := placed[target]; taken {
					continue
				}
				placed[target] = depReal
				dep := readPackageJSON(depReal)
				res.Packages = append(res.Packages, Package{Name: name, Version: dep.Version, Path: target, Source: depReal})
				queue = append(queue, item{target, depReal, false})
			}
		}
	}

	for _, p := range res.Packages {
		var rels []string
		if isLinkedSource(p.Source) {
			rels = linkedPackageFiles(p.Source, readPackageJSON(p.Source))
		} else {
			rels = walkPackage(p.Source)
		}
		for _, rel := range rels {
			res.Files = append(res.Files, File{Path: p.Path + "/" + rel, Source: filepath.Join(p.Source, filepath.FromSlash(rel))})
		}
	}
	sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].Path < res.Files[j].Path })
	sort.Slice(res.Packages, func(i, j int) bool { return res.Packages[i].Path < res.Packages[j].Path })
	return res, nil
}

// HasRuntimeDependencies reports whether the project's package.json declares runtime dependencies.
func HasRuntimeDependencies(projectDir string) bool {
	p := readPackageJSON(projectDir)
	return len(p.Dependencies)+len(p.OptionalDependencies) > 0
}
