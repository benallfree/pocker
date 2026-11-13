package pitr

import (
	"os"
	"path/filepath"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem"
)

// filesystemOfKind: nothing without a handler; a handler serving the kind wins; other kinds are left alone.
func TestFilesystemOfKind(t *testing.T) {
	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	if fsys, err := filesystemOfKind(app, Kind); err != nil || fsys != nil {
		t.Fatalf("no handler: %v %v", fsys, err)
	}

	served := t.TempDir()
	app.OnFilesystemCreate().BindFunc(func(e *core.FilesystemCreateEvent) error {
		if e.Kind == Kind {
			fs, err := filesystem.NewLocal(served)
			if err != nil {
				return err
			}
			e.Filesystem = fs
		}
		return e.Next()
	})
	fsys, err := filesystemOfKind(app, Kind)
	if err != nil || fsys == nil {
		t.Fatalf("handler: %v %v", fsys, err)
	}
	if err := fsys.Upload([]byte("x"), "gen/1.ltx"); err != nil {
		t.Fatal(err)
	}
	fsys.Close()
	if _, err := os.Stat(filepath.Join(served, "gen", "1.ltx")); err != nil {
		t.Fatal("the handler's filesystem wasn't used")
	}
	if fsys, err := filesystemOfKind(app, "other"); err != nil || fsys != nil {
		t.Fatalf("unserved kind: %v %v", fsys, err)
	}
}
