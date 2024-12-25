package syncx

import (
	"sync"
	"sync/atomic"
)

type Map[K comparable, V any] struct {
	m     sync.Map
	count atomic.Int64
}

func (m *Map[K, V]) Delete(key K) {
	m.m.Delete(key)
	m.count.Add(-1)
}
func (m *Map[K, V]) Load(key K) (value V, ok bool) {
	v, ok := m.m.Load(key)
	if !ok {
		return value, ok
	}
	return v.(V), ok
}
func (m *Map[K, V]) LoadAndDelete(key K) (value V, loaded bool) {
	v, loaded := m.m.LoadAndDelete(key)
	if !loaded {
		return value, loaded
	}
	return v.(V), loaded
}
func (m *Map[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	a, loaded := m.m.LoadOrStore(key, value)
	if !loaded {
		m.count.Add(1)
	}
	return a.(V), loaded
}
func (m *Map[K, V]) Range(f func(key K, value V) bool) {
	m.m.Range(func(key, value any) bool { return f(key.(K), value.(V)) })
}
func (m *Map[K, V]) Store(key K, value V) {
	m.m.Store(key, value)
	m.count.Add(1)
}

func (m *Map[K, V]) Len() int {
	return int(m.count.Load())
}

func (m *Map[K, V]) Keys() []K {
	keys := []K{}
	m.Range(func(key K, _ V) bool {
		keys = append(keys, key)
		return true
	})
	return keys
}

func (m *Map[K, V]) Values() []V {
	values := []V{}
	m.Range(func(_ K, value V) bool {
		values = append(values, value)
		return true
	})
	return values
}
