package storage

import (
	"strings"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/tests"
)

// Named backends + mounts: each kind goes to its own backend and folder; a plugin's own kind (litestream) is served
// through core's NewFilesystemOfKind; instances can't see each other; a single app has no instances/ segment.
func TestMountsPerKind(t *testing.T) {
	b := startMinio(t)
	cold := *b
	cold.Bucket, cold.Prefix = "own", "cold/"
	p := New(Config{
		Backends: map[string]*Bucket{"hot": b, "cold": &cold},
		Mounts:   map[string]string{"storage": "hot", "backups": "cold", "litestream": "hot"},
		Logger:   quiet,
	})
	shared, own := raw(t, b, "shared"), raw(t, b, "own")

	app1, _ := tests.NewTestApp()
	defer app1.Cleanup()
	app2, _ := tests.NewTestApp()
	defer app2.Cleanup()
	single, _ := tests.NewTestApp()
	defer single.Cleanup()
	for app, id := range map[*tests.TestApp]string{app1: "inst1", app2: "inst2", single: ""} {
		if err := p.Bind(app, id); err != nil {
			t.Fatal(err)
		}
	}

	up := func(name string, open func() (interface {
		Upload([]byte, string) error
		Close() error
	}, error), key string) {
		t.Helper()
		fsys, err := open()
		if err != nil || fsys == nil {
			t.Fatalf("%s: %v %v", name, fsys, err)
		}
		if err := fsys.Upload([]byte(name), key); err != nil {
			t.Fatal(err)
		}
		fsys.Close()
	}
	up("upload", func() (interface {
		Upload([]byte, string) error
		Close() error
	}, error) {
		return app1.NewFilesystem()
	}, "x.txt")
	up("backup", func() (interface {
		Upload([]byte, string) error
		Close() error
	}, error) {
		return app1.NewBackupsFilesystem()
	}, "b.zip")
	up("pitr", func() (interface {
		Upload([]byte, string) error
		Close() error
	}, error) {
		return app1.NewFilesystemOfKind("litestream", nil)
	}, "gen/1.ltx")
	up("single", func() (interface {
		Upload([]byte, string) error
		Close() error
	}, error) {
		return single.NewFilesystemOfKind("litestream", nil)
	}, "gen/2.ltx")

	for bucket, key := range map[string]string{
		"shared:eu/instances/inst1/storage/x.txt":        "",
		"own:cold/instances/inst1/backups/b.zip":         "",
		"shared:eu/instances/inst1/litestream/gen/1.ltx": "",
		"shared:eu/litestream/gen/2.ltx":                 "",
	} {
		_ = key
		name, k, _ := strings.Cut(bucket, ":")
		fsys := shared
		if name == "own" {
			fsys = own
		}
		if !exists(t, fsys, k) {
			t.Errorf("missing %s", bucket)
		}
	}

	// inst2 sees nothing of inst1's history
	fs2, err := app2.NewFilesystemOfKind("litestream", nil)
	if err != nil || fs2 == nil {
		t.Fatal(fs2, err)
	}
	defer fs2.Close()
	if objs, err := fs2.List(""); err != nil || len(objs) != 0 {
		t.Fatalf("inst2 listed %d objects (%v)", len(objs), err)
	}

	// an unmounted kind isn't served
	if fsys, err := app1.NewFilesystemOfKind("other", nil); err != nil || fsys != nil {
		t.Fatalf("unmounted kind: %v %v", fsys, err)
	}

	// FS: the same roots for callers outside a hook
	fsys, ok, err := p.FS("inst1", "litestream")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if !exists(t, fsys, "gen/1.ltx") {
		t.Error("FS(inst1, litestream) doesn't see the history")
	}
	fsys.Close()
	if _, ok, err := p.FS("inst1", "other"); ok || err != nil {
		t.Fatal("FS on an unmounted kind", ok, err)
	}
}

func TestMountConfigErrors(t *testing.T) {
	b := &Bucket{Bucket: "x"}
	app, _ := tests.NewTestApp()
	defer app.Cleanup()
	if err := New(Config{Backends: map[string]*Bucket{"a": b}, Mounts: map[string]string{"litestream": "nope"}, Logger: quiet}).Bind(app, "i"); err == nil {
		t.Error("a mount naming an unknown backend should fail")
	}
	if err := New(Config{Bucket: b, Backends: map[string]*Bucket{"default": b}, Logger: quiet}).Bind(app, "i"); err == nil {
		t.Error("deprecated bucket + a backend named default should fail")
	}
	if err := New(Config{Logger: quiet}).Bind(app, "i"); err != nil {
		t.Error("no mounts: nothing to do", err)
	}
}
