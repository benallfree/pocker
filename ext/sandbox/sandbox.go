// Package sandbox confines one PocketBase app's JS hooks and migrations so many apps can share one process.
//
// It plugs into PocketBase's jsvm plugin through two hooks:
//
//	jsvm.Register(app, jsvm.Config{OnInit: sb.OnInit, OnCall: sb.OnCall})
package sandbox

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/plugins/jsvm"
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

	// MaxAllocation caps single large allocations JS can request in one step (String repeat/pad, Array join,
	// Buffer/ArrayBuffer/typed arrays, file reads, readerToString/toBytes). Default 64 MiB. Gradual growth is the
	// host's memory watchdog's job (see InterruptActive).
	MaxAllocation int64

	// MaxCallStackSize caps JS recursion depth (default 2000). Without it deep recursion overflows the Go stack,
	// which is fatal for the whole process.
	MaxCallStackSize int

	// Owner identifies the app in InterruptActive results (e.g. the instance id).
	Owner string

	// MaxConcurrent caps how many hook/route/cron calls the app runs at once (0 = no cap). A call waits up to
	// MaxExecution for a slot, then fails.
	MaxConcurrent int
	slotsOnce     sync.Once
	slots         chan struct{}

	// Console, when set, receives console.log/info/debug ("stdout") and console.warn/error ("stderr") output
	// instead of the process's stdout/stderr, so each app's output stays its own.
	Console func(stream, message string)

	// HTTPSendMaxTimeout (seconds) and HTTPSendMaxBody (bytes) cap $http.send for this app; 0 = no cap
	// (upstream: the caller's timeout, default 120 s, and any body size).
	HTTPSendMaxTimeout int
	HTTPSendMaxBody    int64
	clientOnce         sync.Once
	client             *http.Client

	once      sync.Once
	openErr   error
	dir       string
	realDir   string // dir with symlinks resolved, when different
	root      *os.Root
	templates *template.Registry // shared by the app's runtimes, like jsvm's own
	requires  *require.Registry  // confined require(), shared by the app's runtimes
}

// ErrSandboxed is returned by bindings that are disabled in the sandbox.
var ErrSandboxed = errors.New("not available on this host (sandboxed)")

func (s *Sandbox) open() error {
	s.once.Do(func() {
		if s.Dir == "" {
			s.openErr = errors.New("sandbox: Dir is required")
			return
		}
		s.dir, s.openErr = filepath.Abs(s.Dir)
		if s.openErr != nil {
			return
		}
		// the folder's real path, for paths that arrive with symlinks resolved (require() resolves them; on macOS
		// /var/folders is /private/var/folders)
		if real, err := filepath.EvalSymlinks(s.dir); err == nil && real != s.dir {
			s.realDir = real
		}
		s.root, s.openErr = os.OpenRoot(s.dir)
		s.templates = template.NewRegistry()
		s.requires = require.NewRegistry(require.WithLoader(s.requireLoader))
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

func (s *Sandbox) maxAllocation() int64 {
	if s.MaxAllocation > 0 {
		return s.MaxAllocation
	}
	return 64 << 20
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
		clean := filepath.Clean(p)
		for _, base := range []string{s.dir, s.realDir} {
			if base == "" {
				continue
			}
			if r, err := filepath.Rel(base, clean); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
				return r, nil
			}
		}
		return "", fmt.Errorf("%s: path is outside the app folder", p)
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

// OnInit replaces the host-reaching bindings on vm. Use it as (or call it from) jsvm.Config.OnInit, which runs
// after jsvm's standard binds.
func (s *Sandbox) OnInit(vm *goja.Runtime) {
	if err := s.open(); err != nil {
		panic(err)
	}
	templates := s.templates
	s.requires.Enable(vm) // require() reads through the root

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
		return s.readFileCapped(p)
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
			b, err := s.readFileCapped(p)
			if err != nil {
				return nil, err
			}
			return filesystem.NewFileFromBytes(b, filepath.Base(p))
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

	// memory: recursion depth and one-shot allocations
	callStack := s.MaxCallStackSize
	if callStack <= 0 {
		callStack = 2000
	}
	vm.SetMaxCallStackSize(callStack)
	s.capAllocations(vm)

	// console: the app's own sink instead of the shared process output
	if s.Console != nil {
		sink := s.Console
		isError := func(goja.Value) bool { return false }
		if v, err := vm.RunString("(function (e) { return e instanceof Error })"); err == nil {
			if fn, ok := goja.AssertFunction(v); ok {
				isError = func(o goja.Value) bool {
					res, err := fn(goja.Undefined(), o)
					return err == nil && res.ToBoolean()
				}
			}
		}
		format := func(call goja.FunctionCall) string {
			parts := make([]string, len(call.Arguments))
			for i, a := range call.Arguments {
				if o, ok := a.(*goja.Object); ok && !isError(o) {
					if b, err := o.MarshalJSON(); err == nil {
						parts[i] = string(b)
						continue
					}
				}
				parts[i] = a.String()
			}
			return strings.Join(parts, " ")
		}
		con := vm.NewObject()
		for name, stream := range map[string]string{"log": "stdout", "info": "stdout", "debug": "stdout", "warn": "stderr", "error": "stderr"} {
			stream := stream
			con.Set(name, func(call goja.FunctionCall) goja.Value {
				sink(stream, format(call))
				return goja.Undefined()
			})
		}
		vm.Set("console", con)
	}

	// $http.send uses this app's client (timeout and body caps), pinned read-only on the VM (jsvm.HTTPClientKey)
	_ = vm.GlobalObject().DefineDataProperty(jsvm.HTTPClientKey, vm.ToValue(s.httpClient()),
		goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)

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

// readFileCapped reads a file inside the root, refusing files larger than MaxAllocation.
func (s *Sandbox) readFileCapped(p string) ([]byte, error) {
	r, err := s.rel(p)
	if err != nil {
		return nil, err
	}
	f, err := s.root.Open(r)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	max := s.maxAllocation()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s: file larger than %d bytes", p, max)
	}
	return b, nil
}

// capAllocations wraps the built-ins that allocate a large result in one step. The originals are only reachable
// through the wrappers' closures. Results above MaxAllocation throw a RangeError instead of allocating.
func (s *Sandbox) capAllocations(vm *goja.Runtime) {
	max := s.maxAllocation()

	// Go-side readers take a JS-supplied limit: clamp it.
	for _, name := range []string{"readerToString", "toBytes"} {
		orig, ok := goja.AssertFunction(vm.Get(name))
		if !ok {
			continue
		}
		vm.Set(name, func(call goja.FunctionCall) goja.Value {
			limit := call.Argument(1).ToInteger()
			if limit <= 0 || limit > max {
				limit = max
			}
			res, err := orig(goja.Undefined(), call.Argument(0), vm.ToValue(limit))
			if err != nil {
				panic(err)
			}
			return res
		})
	}

	_, err := vm.RunString(fmt.Sprintf(`(function () {
  "use strict";
  const MAX = %d;
  const tooBig = (what) => new RangeError(what + ": result larger than " + MAX + " bytes is not allowed on this host");
  const lenOf = (v) => { const n = Number(v); return Number.isFinite(n) ? n : 0; };
  const wrap = (obj, key, check) => {
    if (!obj || typeof obj[key] !== "function") return;
    const orig = obj[key];
    Object.defineProperty(obj, key, { configurable: false, writable: false, enumerable: false,
      value: function (...args) { check(this, args); return orig.apply(this, args); } });
  };

  wrap(String.prototype, "repeat", (self, a) => { if (String(self).length * lenOf(a[0]) > MAX) throw tooBig("String.repeat"); });
  wrap(String.prototype, "padStart", (self, a) => { if (lenOf(a[0]) > MAX) throw tooBig("String.padStart"); });
  wrap(String.prototype, "padEnd", (self, a) => { if (lenOf(a[0]) > MAX) throw tooBig("String.padEnd"); });
  wrap(Array.prototype, "join", (self, a) => {
    const sep = a[0] === undefined ? 1 : String(a[0]).length;
    if (lenOf(self.length) * Math.max(sep, 1) > MAX) throw tooBig("Array.join");
  });

  // requested element count: a number, or the length of an array-like (not an existing buffer)
  const requested = (a) => {
    if (typeof a === "number") return a;
    if (a && typeof a === "object" && !(a instanceof ArrayBuffer) && typeof a.length === "number") return a.length;
    return 0;
  };
  const guardCtor = (name, bytesPer) => {
    const C = globalThis[name];
    if (typeof C !== "function") return;
    const check = (n) => { if (requested(n) * bytesPer > MAX) throw tooBig(name); };
    const P = new Proxy(C, {
      construct(t, args, nt) { check(args[0]); return Reflect.construct(t, args, nt === P ? t : nt); },
      apply(t, self, args) { check(args[0]); return Reflect.apply(t, self, args); },
    });
    // instances reach their constructor through prototype.constructor: point it at the guard too
    Object.defineProperty(C.prototype, "constructor", { configurable: false, writable: false, enumerable: false, value: P });
    if (typeof C.from === "function") wrap(C, "from", (self, a) => check(a[0]));
    Object.defineProperty(globalThis, name, { configurable: false, writable: false, enumerable: false, value: P });
  };
  guardCtor("ArrayBuffer", 1);
  guardCtor("SharedArrayBuffer", 1);
  [["Int8Array",1],["Uint8Array",1],["Uint8ClampedArray",1],["Int16Array",2],["Uint16Array",2],["Int32Array",4],
   ["Uint32Array",4],["Float32Array",4],["Float64Array",8],["BigInt64Array",8],["BigUint64Array",8]]
    .forEach(([n, b]) => guardCtor(n, b));

  if (typeof Buffer === "function") {
    ["alloc", "allocUnsafe", "allocUnsafeSlow"].forEach((k) =>
      wrap(Buffer, k, (self, a) => { if (lenOf(a[0]) > MAX) throw tooBig("Buffer." + k); }));
    guardCtor("Buffer", 1); // deprecated new Buffer(size)
  }
  // Array.toString / toLocaleString join without going through the wrapped join
  ["toString", "toLocaleString"].forEach((k) =>
    wrap(Array.prototype, k, (self) => { if (lenOf(self && self.length) > MAX) throw tooBig("Array." + k); }));
})()`, max))
	if err != nil {
		panic(fmt.Errorf("sandbox: allocation caps: %w", err))
	}
}

// activeRun is one JS call in progress, for the host's memory watchdog.
type activeRun struct {
	owner   string
	started time.Time
	vm      *goja.Runtime
}

var activeRuns sync.Map // *activeRun -> struct{}

// InterruptActive interrupts every sandboxed JS call that has been running for at least minAge and returns the
// owners (Sandbox.Owner) it interrupted, once each. The host's memory watchdog calls it when the heap climbs past
// its soft limit.
func InterruptActive(minAge time.Duration, reason string) []string {
	now := time.Now()
	seen := map[string]bool{}
	var owners []string
	activeRuns.Range(func(k, _ any) bool {
		r := k.(*activeRun)
		if now.Sub(r.started) < minAge {
			return true
		}
		r.vm.Interrupt(reason)
		if !seen[r.owner] {
			seen[r.owner] = true
			owners = append(owners, r.owner)
		}
		return true
	})
	return owners
}

// interruptAfter arms vm.Interrupt for one JS call, registers it for InterruptActive, and returns the function that
// ends both.
func (s *Sandbox) interruptAfter(vm *goja.Runtime) func() {
	run := &activeRun{owner: s.Owner, started: time.Now(), vm: vm}
	activeRuns.Store(run, struct{}{})

	var t *time.Timer
	if limit := s.maxExecution(); limit > 0 {
		t = time.AfterFunc(limit, func() {
			vm.Interrupt(fmt.Sprintf("execution timeout: JS ran longer than %s", limit))
		})
	}
	return func() {
		if t != nil {
			t.Stop()
		}
		activeRuns.Delete(run)
		vm.ClearInterrupt()
	}
}

// sandboxMapper hides Go members that let JS reach the process instead of its own app.
// goja does not expose a member whose mapped name is "".
type sandboxMapper struct{ jsvm.FieldMapper }

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

// ErrTooBusy is returned when an app already runs MaxConcurrent JS calls and no slot frees up in time.
var ErrTooBusy = errors.New("too many concurrent JS calls for this app; retry shortly")

// acquire takes a concurrency slot for one JS call; the returned function gives it back.
func (s *Sandbox) acquire() (func(), error) {
	if s.MaxConcurrent <= 0 {
		return func() {}, nil
	}
	s.slotsOnce.Do(func() { s.slots = make(chan struct{}, s.MaxConcurrent) })
	wait := s.maxExecution()
	if wait <= 0 {
		wait = 30 * time.Second
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case s.slots <- struct{}{}:
		return func() { <-s.slots }, nil
	case <-t.C:
		return nil, ErrTooBusy
	}
}

// OnCall limits one JS call: it takes a concurrency slot (MaxConcurrent) and arms the MaxExecution interrupt. Use it
// as jsvm.Config.OnCall.
func (s *Sandbox) OnCall(vm *goja.Runtime) (func(), error) {
	release, err := s.acquire()
	if err != nil {
		return nil, err
	}
	stop := s.interruptAfter(vm)
	return func() {
		stop()
		release()
	}, nil
}
