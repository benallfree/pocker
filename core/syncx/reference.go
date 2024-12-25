package syncx

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
)

type IReferencedItem interface{}
type Reference[T any] struct {
	ref T
	mu  sync.RWMutex
}

func (p *Reference[T]) GetFieldMap() map[string]string {
	funcRef := reflect.ValueOf(p.ref).MethodByName("GetFieldMap")
	if !funcRef.IsValid() {
		panic(fmt.Sprintf("IIndexedCacheItem.GetFieldMap() must be implemented on type %T to use it in an indexed cache", p.ref))
	}
	return funcRef.Call(nil)[0].Interface().(map[string]string)
}

type IReference[T IReferencedItem] interface {
	Set(ref T)
	Get() T
	RLock()
	RUnlock()
	Lock()
	Unlock()
	WithRLock(fn func(item T))
	IIndexedCacheItem
}

func NewReference[T any](item T) *Reference[T] {
	wrapper := Reference[T]{ref: item}
	return &wrapper
}

func (p *Reference[T]) Set(ref T) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ref = ref
}

func (p *Reference[T]) Get() T {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ref
}

func (p *Reference[T]) WithRLock(fn func(item T)) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	fn(p.ref)
}

func (p *Reference[T]) RLock() {
	p.mu.RLock()
}

func (p *Reference[T]) RUnlock() {
	p.mu.RUnlock()
}

func (p *Reference[T]) Lock() {
	p.mu.Lock()
}

func (p *Reference[T]) Unlock() {
	p.mu.Unlock()
}

// MarshalJSON implements json.Marshaler
func (p *Reference[T]) MarshalJSON() ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return json.Marshal(p.ref)
}

// UnmarshalJSON implements json.Unmarshaler
func (p *Reference[T]) UnmarshalJSON(data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return json.Unmarshal(data, &p.ref)
}
