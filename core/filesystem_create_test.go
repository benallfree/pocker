package core_test

import (
	"os"
	"path/filepath"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem"
)

func TestOnFilesystemCreate(t *testing.T) {
	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	other := t.TempDir()
	var kinds []string
	app.OnFilesystemCreate().BindFunc(func(e *core.FilesystemCreateEvent) error {
		kinds = append(kinds, e.Kind)
		if e.Kind == core.FilesystemKindStorage {
			fsys, err := filesystem.NewLocal(other)
			if err != nil {
				return err
			}
			e.Filesystem = fsys
		}
		return e.Next()
	})

	// supplied by the handler
	fsys, err := app.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.Upload([]byte("x"), "a.txt"); err != nil {
		t.Fatal(err)
	}
	fsys.Close()
	if _, err := os.Stat(filepath.Join(other, "a.txt")); err != nil {
		t.Fatal("upload didn't go to the handler's filesystem")
	}

	// left nil: the default (local pb_data/backups)
	bfs, err := app.NewBackupsFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	if err := bfs.Upload([]byte("z"), "z.zip"); err != nil {
		t.Fatal(err)
	}
	bfs.Close()
	if _, err := os.Stat(filepath.Join(app.DataDir(), core.LocalBackupsDirName, "z.zip")); err != nil {
		t.Fatal("backups didn't fall back to the default filesystem")
	}

	if len(kinds) != 2 || kinds[0] != core.FilesystemKindStorage || kinds[1] != core.FilesystemKindBackups {
		t.Fatalf("kinds %v", kinds)
	}
}

func TestNewFilesystemOfKind(t *testing.T) {
	app, _ := tests.NewTestApp()
	defer app.Cleanup()

	// no handler, nil fallback: nothing
	fsys, err := app.NewFilesystemOfKind("litestream", nil)
	if err != nil || fsys != nil {
		t.Fatalf("no handler: %v %v", fsys, err)
	}

	// no handler, a fallback: the fallback
	fallback := t.TempDir()
	fsys, err = app.NewFilesystemOfKind("litestream", func() (*filesystem.System, error) { return filesystem.NewLocal(fallback) })
	if err != nil || fsys == nil {
		t.Fatalf("fallback: %v %v", fsys, err)
	}
	fsys.Close()

	// a handler serving the kind wins; other kinds are left alone
	served := t.TempDir()
	app.OnFilesystemCreate().BindFunc(func(e *core.FilesystemCreateEvent) error {
		if e.Kind == "litestream" {
			fs, err := filesystem.NewLocal(served)
			if err != nil {
				return err
			}
			e.Filesystem = fs
		}
		return e.Next()
	})
	fsys, err = app.NewFilesystemOfKind("litestream", nil)
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
	if fsys, err := app.NewFilesystemOfKind("other", nil); err != nil || fsys != nil {
		t.Fatalf("unserved kind: %v %v", fsys, err)
	}
}
