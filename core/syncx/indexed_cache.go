package syncx

import (
	"log/slog"
	"sync"
)

type IIndexedCacheItem interface {
	GetFieldMap() map[string]string
}

type IndexedCache[T IIndexedCacheItem] struct {
	registries Map[string, *Map[string, T]]
	debug      bool
	mu         sync.RWMutex
}

type IndexedCacheConfig struct {
	Debug bool
}

func NewIndexedCache[T IIndexedCacheItem](config IndexedCacheConfig) *IndexedCache[T] {
	return &IndexedCache[T]{
		debug:      config.Debug,
		registries: Map[string, *Map[string, T]]{},
		mu:         sync.RWMutex{},
	}

}

func (p *IndexedCache[T]) Upsert(item T) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, value := range (item).GetFieldMap() {
		if value == "" {
			continue
		}
		registryName := key
		indexName := value

		registry := p.getRegistry(registryName)
		oldItem, ok := registry.Load(indexName)
		if ok {
			p.deleteItem(oldItem)
		}
		registry.Store(indexName, item)
	}
	slog.Debug("Upserted item", "item", item, "total_indexes", p.registries.Len())
	p.registries.Range(
		func(registryName string, registry *Map[string, T]) bool {
			slog.Debug("Registry", "name", registryName, "total_items", registry.Len())
			keys := registry.Keys()
			slog.Debug("Registry keys", "keys", keys)
			return true
		},
	)
}

func (p *IndexedCache[T]) Delete(item T) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deleteItem(item)
}

func (p *IndexedCache[T]) deleteItem(item T) {
	for registryName, indexKey := range (item).GetFieldMap() {
		if indexKey == "" {
			continue
		}
		registry := p.getRegistry(registryName)
		registry.Delete(indexKey)
	}
}

func (p *IndexedCache[T]) GetByFieldNameAndValue(fieldName string, fieldValue string) (T, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.getByFieldNameAndValue(fieldName, fieldValue)
}

func (p *IndexedCache[T]) getByFieldNameAndValue(fieldName string, fieldValue string) (T, bool) {
	index := p.getRegistry(fieldName)
	return index.Load(fieldValue)
}

func (p *IndexedCache[T]) DeleteByFieldNameAndValue(fieldName string, fieldValue string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	obj, ok := p.getByFieldNameAndValue(fieldName, fieldValue)
	if !ok {
		return
	}

	p.deleteItem(obj)
}

func (p *IndexedCache[T]) getRegistry(registryName string) *Map[string, T] {
	index, _ := p.registries.LoadOrStore(registryName, &Map[string, T]{})
	return index
}

func (p *IndexedCache[T]) Range(fn func(item T) bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	idRegistry := p.getRegistry("id")
	idRegistry.Range(func(key string, value T) bool {
		return fn(value)
	})
}

func (p *IndexedCache[T]) RLock() {
	p.mu.RLock()
}

func (p *IndexedCache[T]) RUnlock() {
	p.mu.RUnlock()
}

func (p *IndexedCache[T]) Lock() {
	p.mu.Lock()
}

func (p *IndexedCache[T]) Unlock() {
	p.mu.Unlock()
}
