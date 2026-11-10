package jsvm

// POCKER: this whole file is Pocker's sandbox for running many apps' JS hooks in one process.
// It is only active when Config.Sandbox is set; upstream behaviour is unchanged otherwise.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/template"
	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/require"
)

// Sandbox confines an app's JS hooks and migrations so several apps can share one process:
//
//   - file access ($os, $filepath, $filesystem, $template, $apis.static, require) is limited to Dir, enforced with
//     an [os.Root] (the kernel resolves every path beneath Dir; ".." and escaping symlinks are refused);
//   - host primitives are disabled: $os.cmd/exec, $os.exit, $os.args, $filesystem.local;
//   - $os.getenv and process.env see only Env;
//   - Go methods that reach the process (restart, bootstrap, raw database handles, hook binding at runtime, the HTTP
//     server, ...) are hidden from JS;
//   - every JS call is interrupted after MaxExecution.
type Sandbox struct {
	// Dir is the app's folder (pb_data, pb_hooks, pb_migrations, pb_public). Required.
	Dir string

	// Env is the only environment JS can read.
	Env map[string]string

	// MaxExecution interrupts any single JS call (hook, route, cron job, migration, hook file load) that runs longer.
	// Zero means 30 seconds; negative disables the limit.
	MaxExecution time.Duration

	// MaxSleep caps the sleep() binding (default 10 seconds).
	MaxSleep time.Duration

	once    sync.Once
	openErr error
	dir     string
	root    *os.Root
}

// ErrSandboxed is returned by bindings that are disabled in the sandbox.
var ErrSandboxed = errors.New("not available on this host (sandboxed)")

func (s *Sandbox) open() error {
	s.once.Do(func() {
		if s.Dir == "" {
			s.openErr = errors.New("jsvm sandbox: Dir is required")
			return
		}
		s.dir, s.openErr = filepath.Abs(s.Dir)
		if s.openErr != nil {
			return
		}
		s.root, s.openErr = os.OpenRoot(s.dir)
	})
	return s.openErr
}

func (s *Sandbox) maxExecution() time.Duration {
	switch {
	case s == nil || s.MaxExecution < 0:
		return 0
	case s.MaxExecution == 0:
		return 30 * time.Second
	default:
		return s.MaxExecution
	}
}

func (s *Sandbox) maxSleep() time.Duration {
	if s.MaxSleep > 0 {
		return s.MaxSleep
	}
	return 10 * time.Second
}

// rel turns a JS path (relative to Dir, or absolute under Dir) into a path relative to Dir.
// Anything outside Dir is rejected here; os.Root rejects escapes through ".." and symlinks.
func (s *Sandbox) rel(p string) (string, error) {
	if p == "" {
		return ".", nil
	}
	if filepath.IsAbs(p) {
		r, err := filepath.Rel(s.dir, filepath.Clean(p))
		if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("%s: path is outside the app folder", p)
		}
		return r, nil
	}
	return filepath.Clean(p), nil
}

// fsPath is rel in io/fs form (slash separated, "." for the root).
func (s *Sandbox) fsPath(p string) (string, error) {
	r, err := s.rel(p)
	if err != nil {
		return "", err
	}
	r = filepath.ToSlash(r)
	if !fs.ValidPath(r) {
		return "", fmt.Errorf("%s: path is outside the app folder", p)
	}
	return r, nil
}

func (s *Sandbox) abs(rel string) string {
	return filepath.Join(s.dir, filepath.FromSlash(rel))
}

// requireLoader reads modules for require() through the root.
func (s *Sandbox) requireLoader(p string) ([]byte, error) {
	if err := s.open(); err != nil {
		return nil, err
	}
	r, err := s.rel(p)
	if err != nil {
		return nil, require.ModuleFileDoesNotExistError
	}
	b, err := s.root.ReadFile(r)
	if err != nil {
		return nil, require.ModuleFileDoesNotExistError
	}
	return b, nil
}

// newRequireRegistry returns the registry for require(): confined to the sandbox when there is one.
func newRequireRegistry(s *Sandbox) *require.Registry {
	if s == nil {
		return new(require.Registry)
	}
	return require.NewRegistry(require.WithLoader(s.requireLoader))
}

// apply replaces the host-reaching bindings on vm. It runs after the standard binds and before Config.OnInit.
func (s *Sandbox) apply(vm *goja.Runtime, templates *template.Registry) {
	if err := s.open(); err != nil {
		panic(err)
	}

	vm.SetFieldNameMapper(sandboxMapper{})

	disabled := func(name string) func(goja.FunctionCall) goja.Value {
		return func(goja.FunctionCall) goja.Value {
			panic(vm.NewGoError(fmt.Errorf("%s: %w", name, ErrSandboxed)))
		}
	}

	// $os
	osObj := vm.NewObject()
	vm.Set("$os", osObj)
	osObj.Set("args", []string{})
	osObj.Set("exec", disabled("$os.exec"))
	osObj.Set("cmd", disabled("$os.cmd"))
	osObj.Set("exit", disabled("$os.exit"))
	osObj.Set("getenv", func(key string) string { return s.Env[key] })
	osObj.Set("dirFS", func(p string) (fs.FS, error) {
		r, err := s.fsPath(p)
		if err != nil {
			return nil, err
		}
		return fs.Sub(s.root.FS(), r)
	})
	osObj.Set("stat", func(p string) (os.FileInfo, error) {
		r, err := s.rel(p)
		if err != nil {
			return nil, err
		}
		return s.root.Stat(r)
	})
	osObj.Set("readFile", func(p string) ([]byte, error) {
		r, err := s.rel(p)
		if err != nil {
			return nil, err
		}
		return s.root.ReadFile(r)
	})
	osObj.Set("writeFile", func(p string, data []byte, perm os.FileMode) error {
		r, err := s.rel(p)
		if err != nil {
			return err
		}
		return s.root.WriteFile(r, data, perm)
	})
	osObj.Set("readDir", func(p string) ([]os.DirEntry, error) {
		r, err := s.fsPath(p)
		if err != nil {
			return nil, err
		}
		return fs.ReadDir(s.root.FS(), r)
	})
	osObj.Set("tempDir", func() string {
		_ = s.root.MkdirAll(filepath.Join("pb_data", ".pocker_tmp"), 0o755)
		return s.abs("pb_data/.pocker_tmp")
	})
	osObj.Set("truncate", func(p string, size int64) error {
		r, err := s.rel(p)
		if err != nil {
			return err
		}
		f, err := s.root.OpenFile(r, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		return f.Truncate(size)
	})
	osObj.Set("getwd", func() (string, error) { return s.dir, nil })
	osObj.Set("mkdir", func(p string, perm os.FileMode) error {
		r, err := s.rel(p)
		if err != nil {
			return err
		}
		return s.root.Mkdir(r, perm)
	})
	osObj.Set("mkdirAll", func(p string, perm os.FileMode) error {
		r, err := s.rel(p)
		if err != nil {
			return err
		}
		return s.root.MkdirAll(r, perm)
	})
	osObj.Set("rename", func(from, to string) error {
		rf, err := s.rel(from)
		if err != nil {
			return err
		}
		rt, err := s.rel(to)
		if err != nil {
			return err
		}
		return s.root.Rename(rf, rt)
	})
	osObj.Set("remove", func(p string) error {
		r, err := s.rel(p)
		if err != nil {
			return err
		}
		return s.root.Remove(r)
	})
	osObj.Set("removeAll", func(p string) error {
		r, err := s.rel(p)
		if err != nil {
			return err
		}
		return s.root.RemoveAll(r)
	})
	osObj.Set("openRoot", func(p string) (*os.Root, error) {
		r, err := s.rel(p)
		if err != nil {
			return nil, err
		}
		return s.root.OpenRoot(r)
	})
	osObj.Set("openInRoot", func(dir, name string) (*os.File, error) {
		r, err := s.rel(dir)
		if err != nil {
			return nil, err
		}
		sub, err := s.root.OpenRoot(r)
		if err != nil {
			return nil, err
		}
		defer sub.Close()
		return sub.Open(name)
	})

	// process.env
	if proc, ok := vm.Get("process").(*goja.Object); ok {
		env := vm.NewObject()
		for k, v := range s.Env {
			env.Set(k, v)
		}
		proc.Set("env", env)
	}

	// $filepath: pure helpers stay; the ones that read the disk go through the root
	if fp, ok := vm.Get("$filepath").(*goja.Object); ok {
		fp.Set("glob", func(pattern string) ([]string, error) {
			r, err := s.fsPath(pattern)
			if err != nil {
				return nil, err
			}
			matches, err := fs.Glob(s.root.FS(), r)
			if err != nil {
				return nil, err
			}
			for i, m := range matches {
				matches[i] = s.abs(m)
			}
			return matches, nil
		})
		fp.Set("walkDir", func(start string, fn fs.WalkDirFunc) error {
			r, err := s.fsPath(start)
			if err != nil {
				return err
			}
			return fs.WalkDir(s.root.FS(), r, func(p string, d fs.DirEntry, err error) error {
				return fn(s.abs(p), d, err)
			})
		})
		fp.Set("walk", func(start string, fn filepath.WalkFunc) error {
			r, err := s.fsPath(start)
			if err != nil {
				return err
			}
			return fs.WalkDir(s.root.FS(), r, func(p string, d fs.DirEntry, err error) error {
				var info fs.FileInfo
				if d != nil {
					info, _ = d.Info()
				}
				return fn(s.abs(p), info, err)
			})
		})
	}

	// $filesystem
	if fsObj, ok := vm.Get("$filesystem").(*goja.Object); ok {
		fsObj.Set("local", disabled("$filesystem.local"))
		fsObj.Set("fileFromPath", func(p string) (*filesystem.File, error) {
			r, err := s.rel(p)
			if err != nil {
				return nil, err
			}
			b, err := s.root.ReadFile(r)
			if err != nil {
				return nil, err
			}
			return filesystem.NewFileFromBytes(b, filepath.Base(r))
		})
	}

	// $apis.static: a string path is resolved inside the app folder
	if apisObj, ok := vm.Get("$apis").(*goja.Object); ok {
		apisObj.Set("static", func(dirOrFS any, indexFallback bool) (func(*core.RequestEvent) error, error) {
			switch v := dirOrFS.(type) {
			case fs.FS:
				return apis.Static(v, indexFallback), nil
			case string:
				r, err := s.fsPath(v)
				if err != nil {
					return nil, err
				}
				sub, err := fs.Sub(s.root.FS(), r)
				if err != nil {
					return nil, err
				}
				return apis.Static(sub, indexFallback), nil
			default:
				return nil, errors.New("$apis.static expects the first argument to be either a plain string path or fs.FS value")
			}
		})
	}

	// $template: loadFiles reads through the root
	tpl := vm.NewObject()
	tpl.Set("loadFiles", func(filenames ...string) (*template.Renderer, error) {
		rels := make([]string, len(filenames))
		for i, f := range filenames {
			r, err := s.fsPath(f)
			if err != nil {
				return nil, err
			}
			rels[i] = r
		}
		return templates.LoadFS(s.root.FS(), rels...), nil
	})
	tpl.Set("loadFS", templates.LoadFS)
	tpl.Set("loadString", templates.LoadString)
	tpl.Set("addFuncs", func(funcs map[string]any) *goja.Object {
		templates.AddFuncs(funcs)
		return tpl
	})
	vm.Set("$template", tpl)

	// sleep: capped, so a hook can't park a goroutine forever
	maxSleep := s.maxSleep()
	vm.Set("sleep", func(ms int64) {
		d := time.Duration(ms) * time.Millisecond
		if d > maxSleep {
			d = maxSleep
		}
		time.Sleep(d)
	})
}

// interruptAfter arms vm.Interrupt for one JS call and returns the function that disarms it.
func (s *Sandbox) interruptAfter(vm *goja.Runtime) func() {
	limit := s.maxExecution()
	if limit <= 0 {
		return func() {}
	}
	t := time.AfterFunc(limit, func() {
		vm.Interrupt(fmt.Sprintf("execution timeout: JS ran longer than %s", limit))
	})
	return func() {
		t.Stop()
		vm.ClearInterrupt()
	}
}

// sandboxMapper hides Go members that let JS reach the process instead of its own app.
// goja does not expose a member whose mapped name is "".
type sandboxMapper struct{ FieldMapper }

// hiddenPackages: every field and method of types from these packages is hidden.
var hiddenPackages = map[string]bool{
	"database/sql/driver": true,
	"modernc.org/sqlite":  true,
	"os/exec":             true,
	"net":                 true, // raw listeners and connections
	"syscall":             true,
}

// hiddenTypes: types (package path + name) whose members are hidden and that no method may return to JS.
// sql.Result and sql.Rows stay usable; the handles that can open or run arbitrary connections do not.
var hiddenTypes = map[string]bool{
	"database/sql.DB":   true,
	"database/sql.Conn": true,
	"database/sql.Tx":   true,
	"database/sql.Stmt": true,
}

func typeKey(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.PkgPath() + "." + t.Name()
}

func isHiddenType(t reflect.Type) bool {
	return hiddenPackages[pkgOf(t)] || hiddenTypes[typeKey(t)]
}

// hiddenMethods: method names hidden on types from the given package.
var hiddenMethods = map[string]map[string]bool{
	"ganigeorgiev.com/pocketbase/pocketbase/core": {
		// process lifecycle: one app must not restart, re-bootstrap or migrate the shared process
		"Restart": true, "RestoreBackup": true, "Bootstrap": true, "ResetBootstrapState": true,
		"RunAllMigrations": true, "RunSystemMigrations": true, "RunAppMigrations": true,
		// per-app internals (DataDir stays: it is inside the confined app folder)
		"EncryptionKey": true, "AppMigrationsList": true,
		// server-level hooks and the cron scheduler (JS functions run on other goroutines; use cronAdd)
		"OnServe": true, "OnTerminate": true, "OnBootstrap": true, "Cron": true,
		// hookless copy of the app bypasses every hook, including the platform's
		"UnsafeWithoutHooks": true,
	},
	"ganigeorgiev.com/pocketbase/pocketbase/tools/hook": {
		// binding JS functions at runtime runs them on other goroutines (goja isn't goroutine-safe)
		"Bind": true, "BindFunc": true, "Unbind": true, "UnbindAll": true, "Trigger": true,
	},
	"os": {
		// *os.Root / *os.File handed out by the sandbox: links and ownership could plant a way out for code that
		// doesn't use the root (PocketBase's storage follows symlinks); Chdir changes the whole process's cwd.
		"Symlink": true, "Link": true, "Chown": true, "Lchown": true, "Chdir": true, "Fd": true,
	},
	"ganigeorgiev.com/pocketbase/dbx": {
		"DB": true, // *sql.DB: a raw connection pool that can open any file
	},
	"net/http": {
		// *http.Server and friends reached from events
		"Shutdown": true, "Close": true, "ListenAndServe": true, "ListenAndServeTLS": true, "Serve": true,
		"ServeTLS": true, "SetKeepAlivesEnabled": true, "RegisterOnShutdown": true,
	},
}

// hiddenFields: struct fields hidden on types from the given package.
var hiddenFields = map[string]map[string]bool{
	"ganigeorgiev.com/pocketbase/pocketbase/core": {
		"Server": true, "CertManager": true, "Listener": true, "InstallerFunc": true,
	},
}

func pkgOf(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.PkgPath()
}

func (m sandboxMapper) FieldName(t reflect.Type, f reflect.StructField) string {
	pkg := pkgOf(t)
	if isHiddenType(t) || hiddenFields[pkg][f.Name] || isHiddenType(f.Type) {
		return ""
	}
	return m.FieldMapper.FieldName(t, f)
}

func (m sandboxMapper) MethodName(t reflect.Type, meth reflect.Method) string {
	pkg := pkgOf(t)
	if isHiddenType(t) || hiddenMethods[pkg][meth.Name] {
		return ""
	}
	// any method returning a hidden type (e.g. dbx.DB.DB() *sql.DB)
	for i := 0; i < meth.Type.NumOut(); i++ {
		if isHiddenType(meth.Type.Out(i)) {
			return ""
		}
	}
	return m.FieldMapper.MethodName(t, meth)
}
