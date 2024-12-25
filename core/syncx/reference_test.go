package syncx

import (
	"sync"
	"testing"
)

// Mock struct implementing IIndexedCacheItem for testing
type TestReferenceItem struct {
	ID   string
	Name string
}

func (t TestReferenceItem) GetFieldMap() map[string]string {
	return map[string]string{
		"id":   t.ID,
		"name": t.Name,
	}
}

func TestReference_BasicOperations(t *testing.T) {
	// Test initialization
	item := &TestReferenceItem{ID: "1", Name: "test"}
	ref := NewReference(item)

	// Test Get
	if got := ref.Get(); got != item {
		t.Errorf("Get() = %v, want %v", got, item)
	}

	// Test Set
	newItem := &TestReferenceItem{ID: "2", Name: "updated"}
	ref.Set(newItem)
	newItem.Name = "updated2"
	if got := ref.Get(); got != newItem {
		t.Errorf("After Set(), Get() = %v, want %v", got, newItem)
	}
}

func TestReference_ConcurrentAccess(t *testing.T) {
	item := TestReferenceItem{ID: "1", Name: "test"}
	ref := NewReference[*TestReferenceItem](&item)

	const numGoroutines = 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // For both readers and writers

	// Test concurrent reads
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			_ = ref.Get()
		}()
	}

	// Test concurrent writes
	for i := 0; i < numGoroutines; i++ {
		go func(i int) {
			defer wg.Done()
			ref.Set(&TestReferenceItem{ID: "test", Name: "concurrent"})
		}(i)
	}

	wg.Wait()
}

func TestReference_WithRLock(t *testing.T) {
	item := TestReferenceItem{ID: "1", Name: "test"}
	ref := NewReference[*TestReferenceItem](&item)

	// Test normal execution
	called := false
	ref.WithRLock(func(r *TestReferenceItem) {
		called = true
		if got := r; got != &item {
			t.Errorf("WithRLock callback Get() = %v, want %v", got, item)
		}
	})

	if !called {
		t.Error("WithRLock callback was not called")
	}

	// Test panic recovery
	recovered := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				recovered = true
			}
		}()

		ref.WithRLock(func(r *TestReferenceItem) {
			panic("test panic")
		})
	}()

	if !recovered {
		t.Error("WithRLock did not properly recover from panic")
	}
}

func TestReference_GetFieldMap(t *testing.T) {
	item := TestReferenceItem{ID: "1", Name: "test"}
	ref := NewReference[*TestReferenceItem](&item)

	expected := map[string]string{
		"id":   "1",
		"name": "test",
	}

	got := ref.GetFieldMap()

	if len(got) != len(expected) {
		t.Errorf("GetFieldMap() returned map of size %d, want %d", len(got), len(expected))
	}

	for k, v := range expected {
		if got[k] != v {
			t.Errorf("GetFieldMap()[%s] = %v, want %v", k, got[k], v)
		}
	}
}

func TestReference_LockingMethods(t *testing.T) {
	item := TestReferenceItem{ID: "1", Name: "test"}
	ref := NewReference[*TestReferenceItem](&item)

	// Test Lock/Unlock
	ref.Lock()
	ref.Unlock()

	// Test RLock/RUnlock
	ref.RLock()
	ref.RUnlock()

	// Test multiple readers can acquire lock simultaneously
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		ref.RLock()
		defer ref.RUnlock()
		// Simulate some work
		_ = ref.Get()
	}()

	go func() {
		defer wg.Done()
		ref.RLock()
		defer ref.RUnlock()
		// Simulate some work
		_ = ref.Get()
	}()

	wg.Wait()
}
