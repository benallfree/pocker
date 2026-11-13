package deploy

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/pockethost/pocketone/ext/deploy/internal/glob"
)

func TestGlob(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"pb_*", "pb_schema.json", true},
		{"pb_*", "pb_hooks/x.js", false},
		{"pb_*/**/*", "pb_hooks/x.js", true},
		{"pb_*/**/*", "pb_public/a/b/.well-known", true},
		{"pb_data/**/*", "pb_data/data.db", true},
		{"patches/**/*", "patches/x.patch", true},
		{"**/*.map", "pb_public/a/app.js.map", true},
		{"package.json", "sub/package.json", false},
	}
	for _, c := range cases {
		if got := glob.Match(c.pattern, c.name); got != c.want {
			t.Errorf("Match(%q, %q) = %v", c.pattern, c.name, got)
		}
	}
	if glob.CouldMatchUnder("pb_*/**/*", "src") || !glob.CouldMatchUnder("pb_*/**/*", "pb_hooks/lib") || glob.CouldMatchUnder("package.json", "pb_hooks") {
		t.Fatal("CouldMatchUnder")
	}
}

func TestCollect(t *testing.T) {
	dir := t.TempDir()
	put := func(rel, c string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(c), 0o644)
	}
	put("pb_hooks/main.pb.js", "x")
	put("pb_public/.well-known/security.txt", "c")
	put("pb_public/.gitignore", "i")
	put("pb_public/app.js.map", "m")
	put("pb_data/data.db", "secret")
	put("pb_schema.json", "{}")
	put("README.md", "no")
	put("src/index.ts", "no")
	put("node_modules/dep/index.js", "no")
	put("pb_hooks/node_modules/x.js", "no")
	os.Symlink("main.pb.js", filepath.Join(dir, "pb_hooks/link.js"))

	no := false
	set, err := Collect(CollectOptions{Dir: dir, Exclude: []string{"**/*.map"}, NodeModules: &no})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range set.Files {
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)
	want := []string{"pb_hooks/main.pb.js", "pb_public/.well-known/security.txt", "pb_schema.json"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("files %v", paths)
	}
	if !reflect.DeepEqual(set.Skipped, []string{"pb_hooks/link.js", "pb_public/.gitignore"}) {
		t.Fatalf("skipped %v", set.Skipped)
	}
	if set.NodeModules != nil {
		t.Fatal("no package.json, nodeModules off")
	}

	put("package.json", `{"dependencies":{"gone":"1"}}`)
	set, _ = Collect(CollectOptions{Dir: dir})
	if set.NodeModules == nil || set.CheckComplete() == nil {
		t.Fatal("missing dependency not reported")
	}
	out := filepath.Join(t.TempDir(), "out")
	put("package.json", `{}`)
	set, _ = Collect(CollectOptions{Dir: dir})
	if err := set.WriteTo(out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "pb_hooks/main.pb.js")); string(b) != "x" {
		t.Fatal("--out")
	}
	if err := set.WriteTo(out); err == nil {
		t.Fatal("non-empty --out accepted")
	}
}

func TestBundleRoundTrip(t *testing.T) {
	entries := [][]byte{[]byte("hello"), {}, bytes.Repeat([]byte("x"), 1000)}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(EncodeBundle(entries))
	zw.Close()
	got, err := decodeBundle(gz.Bytes(), 1<<20)
	if err != nil || len(got) != 3 || string(got[0].data) != "hello" || len(got[2].data) != 1000 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestBatchBySize(t *testing.T) {
	files := []LocalFile{{Size: 10}, {Size: 10}, {Size: 30}, {Size: 5}}
	b := BatchBySize(files, 20)
	if len(b) != 3 || len(b[0]) != 2 || len(b[1]) != 1 || len(b[2]) != 1 {
		t.Fatalf("%v", b)
	}
}
