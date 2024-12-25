package syncx

import "sync"

type Pool[T any] struct {
	pool sync.Pool
	New  func() T
}

func (p *Pool[T]) Get() T {
	return p.pool.Get().(T)
}

func (p *Pool[T]) Put(t T) {
	p.pool.Put(t)
}
