package jsvm

// PocketOne: Extensions. A plugin extends jsvm by implementing any of the optional methods below on one value and listing
// it in Config.Extensions; jsvm only calls the methods a value has. With no extensions, jsvm behaves like upstream.

import (
	"net/http"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/require"
)

// Extension is a value that implements any of these optional methods (each is its own small interface below, so a
// plugin implements only what it needs):
//
//	OnInit(vm *goja.Runtime)                                            // after jsvm set up a runtime (before Config.OnInit)
//	OnCall(vm *goja.Runtime) (done func(), err error)                   // around every call into JS; an error refuses it
//	HTTPClient(vm *goja.Runtime) *http.Client                           // the client $http.send uses on vm
//	HookFiles(pattern string) (map[string][]byte, error)                // the hook files, instead of HooksDir
//	MigrationFiles(pattern string) (map[string][]byte, error)           // the migration files, instead of MigrationsDir
//	Require() (hooksRoot string, registry *require.Registry)            // __hooks and the require() registry
//	OnLoad(loader *goja.Runtime, hooksRoot, file string, content []byte) (done func(), mapErr func(error) error)
//	OnCompile(loader *goja.Runtime, src string, prefix int) (mapErr func(error) error)
//
// Extensions run in list order (done funcs in reverse); for the file and require sources the first extension that
// has the method wins; error mappers apply in list order.
type Extension any

type (
	initExtension interface{ OnInit(vm *goja.Runtime) }
	callExtension interface {
		OnCall(vm *goja.Runtime) (func(), error)
	}
	httpExtension interface {
		HTTPClient(vm *goja.Runtime) *http.Client
	}
	hookFilesExtension interface {
		HookFiles(pattern string) (map[string][]byte, error)
	}
	migrationFilesExtension interface {
		MigrationFiles(pattern string) (map[string][]byte, error)
	}
	requireExtension interface {
		Require() (string, *require.Registry)
	}
	loadExtension interface {
		OnLoad(loader *goja.Runtime, hooksRoot, file string, content []byte) (func(), func(error) error)
	}
	compileExtension interface {
		OnCompile(loader *goja.Runtime, src string, prefix int) func(error) error
	}
)

type extensions []Extension

func (x extensions) onInit(vm *goja.Runtime) {
	for _, e := range x {
		if i, ok := e.(initExtension); ok {
			i.OnInit(vm)
		}
	}
	for _, e := range x {
		if h, ok := e.(httpExtension); ok {
			if c := h.HTTPClient(vm); c != nil {
				// pinned read-only, so JS can't swap it out, and boxed, so JS can't reach the client's fields (it
				// could otherwise zero its Timeout or drop its Transport); $http.send reads it at call time
				_ = vm.GlobalObject().DefineDataProperty(httpClientKey, vm.ToValue(httpClientBox{c}),
					goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
				break
			}
		}
	}
}

// call runs fn on vm inside every extension's OnCall.
func (x extensions) call(vm *goja.Runtime, fn func() error) error {
	var dones []func()
	defer func() {
		for i := len(dones) - 1; i >= 0; i-- {
			dones[i]()
		}
	}()
	for _, e := range x {
		if c, ok := e.(callExtension); ok {
			done, err := c.OnCall(vm)
			if err != nil {
				return err
			}
			if done != nil {
				dones = append(dones, done)
			}
		}
	}
	return fn()
}

func (x extensions) hookFiles() hookFilesExtension {
	for _, e := range x {
		if h, ok := e.(hookFilesExtension); ok {
			return h
		}
	}
	return nil
}

func (x extensions) migrationFiles() migrationFilesExtension {
	for _, e := range x {
		if m, ok := e.(migrationFilesExtension); ok {
			return m
		}
	}
	return nil
}

func (x extensions) require() requireExtension {
	for _, e := range x {
		if r, ok := e.(requireExtension); ok {
			return r
		}
	}
	return nil
}

// load runs every extension's OnLoad for one hooks file and returns the combined done and error mapping.
func (x extensions) load(loader *goja.Runtime, hooksRoot, file string, content []byte) (func(), func(error) error) {
	var dones []func()
	var maps []func(error) error
	for _, e := range x {
		if l, ok := e.(loadExtension); ok {
			done, m := l.OnLoad(loader, hooksRoot, file, content)
			if done != nil {
				dones = append(dones, done)
			}
			if m != nil {
				maps = append(maps, m)
			}
		}
	}
	return func() {
		for i := len(dones) - 1; i >= 0; i-- {
			dones[i]()
		}
	}, chain(maps)
}

// compile runs every extension's OnCompile for one handler and returns the combined error mapping.
func (x extensions) compile(loader *goja.Runtime, src string, prefix int) func(error) error {
	var maps []func(error) error
	for _, e := range x {
		if c, ok := e.(compileExtension); ok {
			if m := c.OnCompile(loader, src, prefix); m != nil {
				maps = append(maps, m)
			}
		}
	}
	return chain(maps)
}

func chain(maps []func(error) error) func(error) error {
	return func(err error) error {
		if err == nil {
			return nil
		}
		for _, m := range maps {
			err = m(err)
		}
		return err
	}
}
