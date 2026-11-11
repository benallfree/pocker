package jsvm

import (
	"sync"

	"github.com/dop251/goja"
)

type poolItem struct {
	mux  sync.Mutex
	busy bool
	vm   *goja.Runtime
}

type vmsPool struct {
	mux     sync.RWMutex
	factory func() *goja.Runtime
	items   []*poolItem
	onCall  func(vm *goja.Runtime) (func(), error) // POCKER: Config.OnCall
	max     int                                    // POCKER: newLazyPool grows up to max items
}

// POCKER: newLazyPool creates a pool that starts empty and keeps up to max vms, created on demand.
func newLazyPool(max int, factory func() *goja.Runtime) *vmsPool {
	if max < 1 {
		max = 1
	}
	return &vmsPool{factory: factory, max: max}
}

// newPool creates a new pool with pre-warmed vms generated from the specified factory.
func newPool(size int, factory func() *goja.Runtime) *vmsPool {
	if size < 0 {
		size = 0
	}

	pool := &vmsPool{
		factory: factory,
		items:   make([]*poolItem, size),
	}

	for i := 0; i < size; i++ {
		vm := pool.factory()
		pool.items[i] = &poolItem{vm: vm}
	}

	return pool
}

// run executes "call" with a vm created from the pool
// (either from the buffer or a new one if all buffered vms are busy)
func (p *vmsPool) run(call func(vm *goja.Runtime) error) error {
	p.mux.RLock()

	// try to find a free item
	var freeItem *poolItem
	for _, item := range p.items {
		item.mux.Lock()
		if item.busy {
			item.mux.Unlock()
			continue
		}
		item.busy = true
		item.mux.Unlock()
		freeItem = item
		break
	}

	p.mux.RUnlock()

	// create a new one-off item if of all of the pool items are currently busy
	//
	// note: if turned out not efficient we may change this in the future
	// by adding the created item in the pool with some timer for removal
	// POCKER: a lazy pool keeps the new vm
	if freeItem == nil && p.max > 0 {
		p.mux.Lock()
		if len(p.items) < p.max {
			freeItem = &poolItem{vm: p.factory(), busy: true}
			p.items = append(p.items, freeItem)
		}
		p.mux.Unlock()
	}

	if freeItem == nil {
		return p.call(call, p.factory())
	}

	execErr := p.call(call, freeItem.vm)

	// "free" the vm
	freeItem.mux.Lock()
	freeItem.busy = false
	freeItem.mux.Unlock()

	return execErr
}

// POCKER: call runs fn on vm, wrapped by Config.OnCall when set.
func (p *vmsPool) call(fn func(vm *goja.Runtime) error, vm *goja.Runtime) error {
	if p.onCall != nil {
		done, err := p.onCall(vm)
		if err != nil {
			return err
		}
		defer done()
	}
	return fn(vm)
}
