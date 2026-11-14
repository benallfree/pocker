// Command plugins is CI for PocketOne's plugin modules (ext/*, examples/pocketone): which modules a change affects,
// and the rules every plugin module follows. Run it from the repository root:
//
//	go run ./.github/plugins affected <base> <head>   # module dirs to build and test, one per line
//	go run ./.github/plugins check [dir...]            # plugin rules (default: every module)
//
// A module is affected when a file in it changes (Markdown aside), or when a module it reaches through a `replace`
// is affected. A change to core (the root module) or to this tool affects every module.
//
// The rules (`check`), per module:
//   - a module with a pluginkit plugin (a `Register(app core.App, _ pluginkit.Config) error` method) has a test
//     that calls conformance.Run (docs/plugins.md).
package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "affected":
		if len(os.Args) != 4 {
			usage()
		}
		dirs, err := affected(os.Args[2], os.Args[3])
		if err != nil {
			fail(err)
		}
		for _, d := range dirs {
			fmt.Println(d)
		}
	case "check":
		dirs := os.Args[2:]
		if len(dirs) == 0 {
			mods, err := modules()
			if err != nil {
				fail(err)
			}
			dirs = mods
		}
		if problems := check(dirs); len(problems) > 0 {
			for _, p := range problems {
				fmt.Println("::error::" + p)
			}
			os.Exit(1)
		}
		fmt.Printf("ok: %d modules follow the plugin rules\n", len(dirs))
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: plugins affected <base> <head> | plugins check [dir...]")
	os.Exit(2)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "plugins:", err)
	os.Exit(1)
}

// modules lists the plugin-side module dirs (relative, slash-separated): every go.mod under ext/ and examples/.
func modules() ([]string, error) {
	var out []string
	for _, root := range []string{"ext", "examples"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "node_modules" || d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			if !d.IsDir() && d.Name() == "go.mod" {
				out = append(out, filepath.ToSlash(filepath.Dir(path)))
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

// owner is the module a file belongs to: the deepest module dir containing it, or "." (core).
func owner(file string, mods []string) string {
	best := "."
	for _, m := range mods {
		if strings.HasPrefix(file, m+"/") && len(m) > len(best) {
			best = m
		}
	}
	return best
}

var replaceLine = regexp.MustCompile(`=>\s*(\.[^\s]*)`)

// deps maps each module dir to the module dirs it reaches through local `replace` directives ("." is core).
func deps(mods []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, m := range mods {
		f, err := os.Open(filepath.Join(m, "go.mod"))
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if !strings.Contains(line, "=>") {
				continue
			}
			sub := replaceLine.FindStringSubmatch(line)
			if sub == nil {
				continue
			}
			target := filepath.ToSlash(filepath.Clean(filepath.Join(m, sub[1])))
			out[m] = append(out[m], target)
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func affected(base, head string) ([]string, error) {
	mods, err := modules()
	if err != nil {
		return nil, err
	}
	b, err := exec.Command("git", "diff", "--name-only", base, head).Output()
	if err != nil {
		return nil, fmt.Errorf("git diff %s %s: %w", base, head, err)
	}
	hit := map[string]bool{}
	all := false
	for _, f := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if f == "" || strings.HasSuffix(f, ".md") {
			continue
		}
		o := owner(f, mods)
		if o != "." {
			hit[o] = true
			continue
		}
		// the root module: core code, or this CI. Root-level files that aren't code (docs, .claude/) don't count.
		if strings.HasPrefix(f, ".github/plugins/") || f == ".github/workflows/plugins.yml" || isCore(f) {
			all = true
		}
	}
	if all {
		return mods, nil
	}
	// a module is affected when anything it replaces (transitively) is
	dep, err := deps(mods)
	if err != nil {
		return nil, err
	}
	var reaches func(m string, seen map[string]bool) bool
	reaches = func(m string, seen map[string]bool) bool {
		if hit[m] {
			return true
		}
		if seen[m] {
			return false
		}
		seen[m] = true
		for _, d := range dep[m] {
			if reaches(d, seen) {
				return true
			}
		}
		return false
	}
	var out []string
	for _, m := range mods {
		if reaches(m, map[string]bool{}) {
			out = append(out, m)
		}
	}
	return out, nil
}

// isCore matches .github/core-audit/core-diff.sh: everything PocketOne doesn't own.
func isCore(f string) bool {
	for _, p := range []string{"ext/", "examples/pocketone/", "docs/", ".github/", ".claude/"} {
		if strings.HasPrefix(f, p) {
			return false
		}
	}
	switch f {
	case "README.md", "AGENTS.md", "CLAUDE.md":
		return false
	}
	return true
}

var (
	pluginMethod = regexp.MustCompile(`(?m)^func \(\w+ \*?\w+\) Register\(\w+ core\.App, \w+ pluginkit\.Config\) error`)
	conformance  = regexp.MustCompile(`conformance\.(Run|CheckLifecycle)\(`)
)

// check applies the plugin rules to each module dir.
func check(dirs []string) []string {
	mods, _ := modules()
	var problems []string
	for _, dir := range dirs {
		dir = strings.TrimSuffix(filepath.ToSlash(dir), "/")
		var plugins []string
		tested := false
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			p := filepath.ToSlash(path)
			if d.IsDir() {
				if p != dir && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".") || owner(p+"/x", mods) != dir) {
					return filepath.SkipDir // another module, or not code
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.HasSuffix(p, "_test.go") {
				tested = tested || conformance.Match(src)
			} else if pluginMethod.Match(src) {
				plugins = append(plugins, p)
			}
			return nil
		})
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", dir, err))
			continue
		}
		if len(plugins) > 0 && !tested {
			problems = append(problems, fmt.Sprintf("%s: implements pluginkit.Plugin (%s) but has no test calling conformance.Run; add conformance_test.go (docs/plugins.md)", dir, strings.Join(plugins, ", ")))
		}
	}
	return problems
}
