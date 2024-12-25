package syncx

import (
	"testing"
)

// TestItem implements IIndexedCacheItem for testing
type TestIndexedCacheItem struct {
	ID    string
	Name  string
	Email string
}

func (t TestIndexedCacheItem) GetFieldMap() map[string]string {
	return map[string]string{
		"id":    t.ID,
		"name":  t.Name,
		"email": t.Email,
	}
}

func TestIndexedCache_Upsert(t *testing.T) {
	cache := NewIndexedCache[*TestIndexedCacheItem](IndexedCacheConfig{
		Debug: true,
	})

	item := &TestIndexedCacheItem{
		ID:    "1",
		Name:  "John Doe",
		Email: "john@example.com",
	}

	cache.Upsert(item)

	// Test retrieval by different indexes
	tests := []struct {
		name      string
		indexName string
		key       string
		want      *TestIndexedCacheItem
		wantFound bool
	}{
		{
			name:      "get by id",
			indexName: "id",
			key:       "1",
			want:      item,
			wantFound: true,
		},
		{
			name:      "get by name",
			indexName: "name",
			key:       "John Doe",
			want:      item,
			wantFound: true,
		},
		{
			name:      "get by email",
			indexName: "email",
			key:       "john@example.com",
			want:      item,
			wantFound: true,
		},
		{
			name:      "get non-existent",
			indexName: "id",
			key:       "999",
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := cache.GetByFieldNameAndValue(tt.indexName, tt.key)
			if found != tt.wantFound {
				t.Errorf("GetByFieldNameAndValue() found = %v, want %v", found, tt.wantFound)
				return
			}
			if found && got != tt.want {
				t.Errorf("GetByFieldNameAndValue() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIndexedCache_Delete(t *testing.T) {
	cache := NewIndexedCache[*TestIndexedCacheItem](IndexedCacheConfig{
		Debug: true,
	})

	item := TestIndexedCacheItem{
		ID:    "1",
		Name:  "John Doe",
		Email: "john@example.com",
	}

	cache.Upsert(&item)

	// Delete by ID
	cache.DeleteByFieldNameAndValue("id", "1")

	// Verify item is deleted from all indexes
	indexes := []string{"id", "name", "email"}
	keys := []string{"1", "John Doe", "john@example.com"}

	for i, indexName := range indexes {
		if _, found := cache.GetByFieldNameAndValue(indexName, keys[i]); found {
			t.Errorf("Item still exists in index %s after deletion", indexName)
		}
	}
}

func TestIndexedCache_UpsertUpdate(t *testing.T) {
	cache := NewIndexedCache[*TestIndexedCacheItem](IndexedCacheConfig{
		Debug: true,
	})

	// Insert initial item
	item := TestIndexedCacheItem{
		ID:    "1",
		Name:  "John Doe",
		Email: "john@example.com",
	}
	cache.Upsert(&item)

	// Update the item
	updatedItem := TestIndexedCacheItem{
		ID:    "1",
		Name:  "John Smith",
		Email: "john.smith@example.com",
	}
	cache.Upsert(&updatedItem)

	// Check old values are not accessible
	if _, found := cache.GetByFieldNameAndValue("name", "John Doe"); found {
		t.Error("Old name index still exists after update")
	}
	if _, found := cache.GetByFieldNameAndValue("email", "john@example.com"); found {
		t.Error("Old email index still exists after update")
	}

	// Check new values are accessible
	got, found := cache.GetByFieldNameAndValue("id", "1")
	if !found {
		t.Error("Updated item not found by ID")
	}
	if got != &updatedItem {
		t.Errorf("Got %v, want %v", got, updatedItem)
	}

	got, found = cache.GetByFieldNameAndValue("name", "John Smith")
	if !found {
		t.Error("Updated item not found by new name")
	}
	if got != &updatedItem {
		t.Errorf("Got %v, want %v", got, updatedItem)
	}
}

func TestIndexedCache_EmptyFields(t *testing.T) {
	cache := NewIndexedCache[*TestIndexedCacheItem](IndexedCacheConfig{
		Debug: true,
	})

	// Item with empty fields
	item := &TestIndexedCacheItem{
		ID:    "1",
		Name:  "", // empty name
		Email: "john@example.com",
	}

	cache.Upsert(item)

	// Test retrieval by different indexes
	tests := []struct {
		name      string
		indexName string
		key       string
		want      *TestIndexedCacheItem
		wantFound bool
	}{
		{
			name:      "get by id",
			indexName: "id",
			key:       "1",
			want:      item,
			wantFound: true,
		},
		{
			name:      "get by empty name should not find",
			indexName: "name",
			key:       "",
			want:      nil,
			wantFound: false,
		},
		{
			name:      "get by email",
			indexName: "email",
			key:       "john@example.com",
			want:      item,
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := cache.GetByFieldNameAndValue(tt.indexName, tt.key)
			if found != tt.wantFound {
				t.Errorf("GetByFieldNameAndValue() found = %v, want %v", found, tt.wantFound)
				return
			}
			if found && got != tt.want {
				t.Errorf("GetByFieldNameAndValue() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIndexedCache_UpsertWithEmptyUpdate(t *testing.T) {
	cache := NewIndexedCache[*TestIndexedCacheItem](IndexedCacheConfig{
		Debug: true,
	})

	// Insert initial item with all fields populated
	item := &TestIndexedCacheItem{
		ID:    "1",
		Name:  "John Doe",
		Email: "john@example.com",
	}
	cache.Upsert(item)

	// Update with empty fields
	updatedItem := &TestIndexedCacheItem{
		ID:    "1",
		Name:  "", // empty name
		Email: "new@example.com",
	}
	cache.Upsert(updatedItem)

	// Verify old name index is removed
	if _, found := cache.GetByFieldNameAndValue("name", "John Doe"); found {
		t.Error("Old name index still exists after update to empty value")
	}

	// Verify empty name is not indexed
	if _, found := cache.GetByFieldNameAndValue("name", ""); found {
		t.Error("Empty name should not be indexed")
	}

	// Verify we can still find by ID and new email
	if _, found := cache.GetByFieldNameAndValue("id", "1"); !found {
		t.Error("Item should still be findable by ID")
	}
	if _, found := cache.GetByFieldNameAndValue("email", "new@example.com"); !found {
		t.Error("Item should be findable by new email")
	}
}
