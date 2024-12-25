# syncx

`syncx` is a utility package designed to provide thread-safe data structures and synchronization primitives.

## Features

### Reference

`Reference` allows you to wrap any type so that the underlying type can be updated while keeping the reference pointer constant. This is particularly useful for scenarios like unmarshaling new values without changing the reference. Key features include:

- **Thread-Safe Access:** Both getting and setting operations are thread-safe.
- **Read/Write Locks:** Fine-grained access control using read and write locks.
- **Immutability Recommendation:** It is recommended to use `Set()` to update the reference pointer instead of mutating the underlying value directly.

### IndexedCache

`IndexedCache` maintains multiple indexes for objects, facilitating efficient retrieval based on different keys. It relies on the `GetFieldMap` method to provide a key-value map where each key is a field name and the value is a unique identifier for the object. Features include:

- **Automatic Index Management:** When a unique key is added to any index, the old object associated with that key is removed from all indexes.
- **Compatibility with Reference:** You can maintain an `IndexedCache` of `Reference` objects, allowing for dynamic updates while preserving reference integrity.

### Map & Pool

- **Map:** A simple generic wrapper around Go's `sync.Map`, providing type safety and ease of use.
- **Pool:** A generic pool implementation that wraps Go's `sync.Pool`, allowing for efficient reuse of objects.

## Installation

```bash
go get github.com/yourusername/syncx
```

## Usage

### Reference

```go
item := TestReferenceItem{ID: "1", Name: "test"}
ref := NewReference[TestReferenceItem](&item)

// Get the current reference
currentItem := ref.Get()

// Update the reference safely
newItem := TestReferenceItem{ID: "2", Name: "updated"}
ref.Set(&newItem)
```

### IndexedCache

```go
cache := NewIndexedCache[TestIndexedCacheItem](IndexedCacheConfig{
    Debug: true,
})

item := &TestIndexedCacheItem{
    ID:    "1",
    Name:  "John Doe",
    Email: "john@example.com",
}

cache.Upsert(item)

// Retrieve by index
retrievedItem, found := cache.GetByIndex("email", "john@example.com")
```

### Map & Pool

```go
// Using Map
var myMap Map[string, int]
myMap.Store("key1", 100)
value, exists := myMap.Load("key1")

// Using Pool
pool := Pool[int]{ New: func() int { return 0 } }
num := pool.Get()
pool.Put(num)
```

## Contributing

Contributions are welcome! Please open an issue or submit a pull request for any enhancements or bug fixes.

## License

[MIT](LICENSE)
