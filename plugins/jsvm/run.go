package jsvm

import (
	"errors"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/dop251/goja"
)

// CE: this file lets Go plugins run JS on an app's hook runtimes.

const executorsStoreKey = "__pbJsvmExecutors"

// ErrNotRegistered is returned by [Run] when the jsvm plugin isn't registered on the app.
var ErrNotRegistered = errors.New("jsvm: not registered on this app")

// Run calls fn with one of the app's pooled JS runtimes (the same ones hooks, routes and cron jobs use, with the
// same globals), wrapped by Config.OnCall like every other call into JS. fn must not keep the runtime after it
// returns.
func Run(app core.App, fn func(vm *goja.Runtime) error) error {
	pool, _ := app.Store().Get(executorsStoreKey).(*vmsPool)
	if pool == nil {
		return ErrNotRegistered
	}
	return pool.run(fn)
}
