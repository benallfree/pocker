package nodemodules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The cases of the npm CLI's nodeModules.test.ts.

type tree struct {
	t    *testing.T
	root string
}

func newTree(t *testing.T) *tree {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &tree{t, root}
}

func (tr *tree) write(rel, content string) {
	abs := filepath.Join(tr.root, rel)
	os.MkdirAll(filepath.Dir(abs), 0o755)
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		tr.t.Fatal(err)
	}
}

func (tr *tree) pkg(dir string, j map[string]any, files map[string]string) {
	b, _ := json.Marshal(j)
	tr.write(dir+"/package.json", string(b))
	if files == nil {
		files = map[string]string{"index.js": ""}
	}
	for f, c := range files {
		tr.write(dir+"/"+f, c)
	}
}

func (tr *tree) link(target, rel string) {
	abs := filepath.Join(tr.root, rel)
	os.MkdirAll(filepath.Dir(abs), 0o755)
	if err := os.Symlink(target, abs); err != nil {
		tr.t.Fatal(err)
	}
}

func (tr *tree) collect() *Result {
	r, err := Collect(tr.root)
	if err != nil {
		tr.t.Fatal(err)
	}
	return r
}

func paths(r *Result) []string {
	out := []string{}
	for _, f := range r.Files {
		out = append(out, f.Path)
	}
	return out
}

func pkgPaths(r *Result, withVersion bool) []string {
	out := []string{}
	for _, p := range r.Packages {
		if withVersion {
			out = append(out, p.Path+"@"+p.Version)
		} else {
			out = append(out, p.Path)
		}
	}
	return out
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

var none = map[string]string{}

func TestDependenciesNotDev(t *testing.T) {
	tr := newTree(t)
	tr.pkg(".", map[string]any{"dependencies": map[string]string{"a": "1"}, "devDependencies": map[string]string{"dev": "1"}}, none)
	tr.pkg("node_modules/a", map[string]any{"name": "a", "version": "1.0.0", "dependencies": map[string]string{"b": "1"}}, nil)
	tr.pkg("node_modules/b", map[string]any{"name": "b", "version": "1.0.0"}, map[string]string{"lib/b.js": "b"})
	tr.pkg("node_modules/dev", map[string]any{"name": "dev", "version": "1.0.0"}, nil)
	tr.pkg("node_modules/unrelated", map[string]any{"name": "unrelated", "version": "1.0.0"}, nil)
	r := tr.collect()
	eq(t, paths(r), []string{"node_modules/a/index.js", "node_modules/a/package.json", "node_modules/b/lib/b.js", "node_modules/b/package.json"})
	eq(t, len(r.Missing), 0)
}

func TestNestsSecondVersion(t *testing.T) {
	tr := newTree(t)
	tr.pkg(".", map[string]any{"dependencies": map[string]string{"a": "1", "b": "1"}}, none)
	tr.pkg("node_modules/b", map[string]any{"name": "b", "version": "1.0.0"}, map[string]string{"v.js": "1"})
	tr.pkg("node_modules/a", map[string]any{"name": "a", "version": "1.0.0", "dependencies": map[string]string{"b": "2"}}, nil)
	tr.pkg("node_modules/a/node_modules/b", map[string]any{"name": "b", "version": "2.0.0"}, map[string]string{"v.js": "2"})
	eq(t, pkgPaths(tr.collect(), true), []string{"node_modules/a@1.0.0", "node_modules/a/node_modules/b@2.0.0", "node_modules/b@1.0.0"})
}

func TestReusesHoisted(t *testing.T) {
	tr := newTree(t)
	tr.pkg(".", map[string]any{"dependencies": map[string]string{"a": "1", "c": "1"}}, none)
	tr.pkg("node_modules/a", map[string]any{"name": "a", "version": "1.0.0", "dependencies": map[string]string{"shared": "1"}}, nil)
	tr.pkg("node_modules/c", map[string]any{"name": "c", "version": "1.0.0", "dependencies": map[string]string{"shared": "1"}}, nil)
	tr.pkg("node_modules/shared", map[string]any{"name": "shared", "version": "1.0.0"}, nil)
	eq(t, pkgPaths(tr.collect(), false), []string{"node_modules/a", "node_modules/c", "node_modules/shared"})
}

func TestPnpmSymlinks(t *testing.T) {
	tr := newTree(t)
	tr.pkg(".", map[string]any{"dependencies": map[string]string{"a": "1"}}, none)
	tr.pkg("node_modules/.pnpm/a@1.0.0/node_modules/a", map[string]any{"name": "a", "version": "1.0.0", "dependencies": map[string]string{"b": "1"}}, nil)
	tr.pkg("node_modules/.pnpm/b@1.0.0/node_modules/b", map[string]any{"name": "b", "version": "1.0.0"}, nil)
	tr.link("../../b@1.0.0/node_modules/b", "node_modules/.pnpm/a@1.0.0/node_modules/b")
	tr.link(".pnpm/a@1.0.0/node_modules/a", "node_modules/a")
	r := tr.collect()
	eq(t, paths(r), []string{"node_modules/a/index.js", "node_modules/a/package.json", "node_modules/b/index.js", "node_modules/b/package.json"})
	for _, f := range r.Files {
		if !strings.Contains(f.Source, "/node_modules/.pnpm/") {
			t.Fatalf("source %s", f.Source)
		}
		if f.Path == "node_modules/b/index.js" && !strings.Contains(f.Source, ".pnpm/b@1.0.0") {
			t.Fatalf("b source %s", f.Source)
		}
	}
}

func TestScoped(t *testing.T) {
	tr := newTree(t)
	tr.pkg(".", map[string]any{"dependencies": map[string]string{"@s/a": "1"}}, none)
	tr.pkg("node_modules/@s/a", map[string]any{"name": "@s/a", "version": "1.0.0", "dependencies": map[string]string{"@s/b": "1"}}, nil)
	tr.pkg("node_modules/@s/b", map[string]any{"name": "@s/b", "version": "1.0.0"}, nil)
	eq(t, pkgPaths(tr.collect(), false), []string{"node_modules/@s/a", "node_modules/@s/b"})
}

func TestLinkedWorkspacePublishedFiles(t *testing.T) {
	tr := newTree(t)
	tr.pkg(".", map[string]any{"dependencies": map[string]string{"w": "workspace:*"}}, none)
	tr.pkg("packages/w", map[string]any{"name": "w", "version": "0.1.0", "files": []string{"dist"}},
		map[string]string{"dist/index.js": "built", "src/index.ts": "source", "tsconfig.json": "{}", "README.md": "readme"})
	tr.link("../packages/w", "node_modules/w")
	eq(t, paths(tr.collect()), []string{"node_modules/w/README.md", "node_modules/w/dist/index.js", "node_modules/w/package.json"})
}

func TestMissing(t *testing.T) {
	tr := newTree(t)
	tr.pkg(".", map[string]any{"dependencies": map[string]string{"gone": "1"}, "optionalDependencies": map[string]string{"maybe": "1"}}, none)
	r := tr.collect()
	eq(t, r.Missing, []Missing{{Name: "gone", RequiredBy: "package.json"}})
	eq(t, len(r.Files), 0)
}

func TestPeerDependencies(t *testing.T) {
	tr := newTree(t)
	tr.pkg(".", map[string]any{"dependencies": map[string]string{"plugin": "1", "host": "1"}}, none)
	tr.pkg("node_modules/plugin", map[string]any{"name": "plugin", "version": "1.0.0", "peerDependencies": map[string]string{"host": "1", "absent": "1"}}, nil)
	tr.pkg("node_modules/host", map[string]any{"name": "host", "version": "1.0.0"}, nil)
	r := tr.collect()
	names := []string{}
	for _, p := range r.Packages {
		names = append(names, p.Name)
	}
	eq(t, names, []string{"host", "plugin"})
	eq(t, len(r.Missing), 0)
}

func TestSymlinkLoop(t *testing.T) {
	tr := newTree(t)
	tr.pkg(".", map[string]any{"dependencies": map[string]string{"a": "1"}}, none)
	tr.pkg("node_modules/a", map[string]any{"name": "a", "version": "1.0.0"}, nil)
	tr.link("..", "node_modules/a/lib/loop")
	eq(t, paths(tr.collect()), []string{"node_modules/a/index.js", "node_modules/a/package.json"})
}

func TestHasRuntimeDependencies(t *testing.T) {
	tr := newTree(t)
	tr.pkg(".", map[string]any{"devDependencies": map[string]string{"x": "1"}}, none)
	if HasRuntimeDependencies(tr.root) {
		t.Fatal("dev only")
	}
	tr.pkg(".", map[string]any{"dependencies": map[string]string{"x": "1"}}, none)
	if !HasRuntimeDependencies(tr.root) {
		t.Fatal("has deps")
	}
}
