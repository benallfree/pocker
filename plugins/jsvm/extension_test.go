package jsvm

// PocketOne: tests for Config.Extensions, the lazy pool and Run, so an upstream merge that breaks one fails here and not
// only in ext/.

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/require"
)

type callRecorder struct {
	name string
	log  *[]string
	err  error
}

func (r callRecorder) OnCall(*goja.Runtime) (func(), error) {
	*r.log = append(*r.log, "in:"+r.name)
	if r.err != nil {
		return nil, r.err
	}
	return func() { *r.log = append(*r.log, "out:"+r.name) }, nil
}

func TestExtensionsCall(t *testing.T) {
	var log []string
	x := extensions{callRecorder{name: "a", log: &log}, "not an extension", callRecorder{name: "b", log: &log}}
	if err := x.call(goja.New(), func() error { log = append(log, "fn"); return nil }); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(log, " "); got != "in:a in:b fn out:b out:a" {
		t.Fatalf("order %q", got)
	}

	// an OnCall error refuses the call; the extensions already entered still get their done
	log = nil
	refused := errors.New("refused")
	x = extensions{callRecorder{name: "a", log: &log}, callRecorder{name: "b", log: &log, err: refused}}
	err := x.call(goja.New(), func() error { log = append(log, "fn"); return nil })
	if !errors.Is(err, refused) {
		t.Fatalf("err %v", err)
	}
	if got := strings.Join(log, " "); got != "in:a in:b out:a" {
		t.Fatalf("order %q", got)
	}
}

type mapRecorder struct{ tag string }

func (m mapRecorder) OnLoad(*goja.Runtime, string, string, []byte) (func(), func(error) error) {
	return nil, func(err error) error { return errors.New(err.Error() + m.tag) }
}

func (m mapRecorder) OnCompile(*goja.Runtime, string, int) func(error) error {
	return func(err error) error { return errors.New(err.Error() + m.tag) }
}

func TestExtensionsErrorMappersChain(t *testing.T) {
	x := extensions{mapRecorder{"1"}, mapRecorder{"2"}}
	done, mapErr := x.load(nil, "", "", nil)
	done()
	if got := mapErr(errors.New("e")).Error(); got != "e12" {
		t.Fatalf("load mappers %q", got)
	}
	if mapErr(nil) != nil {
		t.Fatal("a nil error must stay nil")
	}
	if got := x.compile(nil, "", 0)(errors.New("e")).Error(); got != "e12" {
		t.Fatalf("compile mappers %q", got)
	}
	if got := extensions(nil).compile(nil, "", 0)(errors.New("e")).Error(); got != "e" {
		t.Fatalf("no extensions should leave the error as is, got %q", got)
	}
}

func TestLazyPool(t *testing.T) {
	var made atomic.Int32
	pool := newLazyPool(2, func() *goja.Runtime { made.Add(1); return goja.New() })
	if made.Load() != 0 {
		t.Fatal("a lazy pool must not create vms up front")
	}

	// sequential runs reuse the one vm
	for i := 0; i < 3; i++ {
		if err := pool.run(func(*goja.Runtime) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if made.Load() != 1 {
		t.Fatalf("made %d vms for sequential runs, want 1", made.Load())
	}

	// 3 concurrent runs: the pool grows to its max (2) and the third gets a one-off vm
	release := make(chan struct{})
	var started, wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		started.Add(1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			pool.run(func(*goja.Runtime) error { started.Done(); <-release; return nil })
		}()
	}
	started.Wait()
	close(release)
	wg.Wait()
	if n := len(pool.items); n != 2 {
		t.Fatalf("pool kept %d vms, want 2", n)
	}
	if made.Load() != 3 {
		t.Fatalf("made %d vms, want 3", made.Load())
	}
}

// fileSource supplies hook and migration files and a require registry, and records loads and compiles.
type fileSource struct {
	loads, compiles *[]string
}

func (fileSource) HookFiles(string) (map[string][]byte, error) {
	return map[string][]byte{"main.pb.js": []byte(`routerAdd("GET", "/ce-ext-test", (e) => e.string(200, "ok"))`)}, nil
}

func (fileSource) MigrationFiles(string) (map[string][]byte, error) {
	return map[string][]byte{"1700000000_ce_ext_test.js": []byte(`migrate((app) => {}, (app) => {})`)}, nil
}

func (fileSource) Require() (string, *require.Registry) { return "/ce/pb_hooks", new(require.Registry) }

func (s fileSource) OnLoad(_ *goja.Runtime, root, file string, _ []byte) (func(), func(error) error) {
	*s.loads = append(*s.loads, root+"|"+file)
	return nil, nil
}

func (s fileSource) OnCompile(_ *goja.Runtime, src string, _ int) func(error) error {
	*s.compiles = append(*s.compiles, src)
	return nil
}

func TestRegisterWithExtensions(t *testing.T) {
	own := &core.MigrationsList{}
	app, err := tests.NewTestAppWithConfig(core.BaseAppConfig{AppMigrations: own})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()

	if err := Run(app, func(*goja.Runtime) error { return nil }); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("Run before Register: %v", err)
	}

	var loads, compiles []string
	if err := Register(app, Config{Extensions: []Extension{fileSource{&loads, &compiles}}}); err != nil {
		t.Fatal(err)
	}

	if len(own.Items()) != 1 {
		t.Fatalf("the MigrationFiles migration should be on the app's own list, got %d items", len(own.Items()))
	}
	if len(loads) != 1 || loads[0] != "/ce/pb_hooks|main.pb.js" {
		t.Fatalf("OnLoad calls %q", loads)
	}
	if len(compiles) != 1 || !strings.Contains(compiles[0], `e.string(200, "ok")`) {
		t.Fatalf("OnCompile calls %q", compiles)
	}

	var hooks string
	if err := Run(app, func(vm *goja.Runtime) error {
		hooks = vm.Get("__hooks").String()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if hooks != "/ce/pb_hooks" {
		t.Fatalf("__hooks %q, want the Require root", hooks)
	}
}
