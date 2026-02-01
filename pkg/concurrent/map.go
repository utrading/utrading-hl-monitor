package concurrent

import (
	"iter"
	"sync"
	"sync/atomic"
)

// Map is like a Go map[K]V but is safe for concurrent use
// by multiple goroutines without additional locking or coordination.
type Map[K comparable, V any] struct {
	length atomic.Int64
	data   sync.Map
}

// Len returns the current number of elements in the map.
func (m *Map[K, V]) Len() int64 {
	return m.length.Load()
}

// Load returns the value stored in the map for a key, or the zero value if no
// value is present.
// The ok result indicates whether value was found in the map.
func (m *Map[K, V]) Load(key K) (V, bool) {
	value, ok := m.data.Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	return value.(V), true
}

// Store sets the value for a key.
func (m *Map[K, V]) Store(key K, value V) {
	_, loaded := m.data.LoadOrStore(key, value)
	if !loaded {
		m.length.Add(1)
	} else {
		m.data.Store(key, value)
	}
}

// LoadOrStore returns the existing value for the key if present.
// Otherwise, it stores and returns the given value.
// The loaded result is true if the value was loaded, false if stored.
func (m *Map[K, V]) LoadOrStore(key K, value V) (V, bool) {
	actual, loaded := m.data.LoadOrStore(key, value)
	if !loaded {
		m.length.Add(1)
	}
	return actual.(V), loaded
}

// LoadAndDelete deletes the value for a key, returning the previous value if any.
// The loaded result reports whether the key was present.
func (m *Map[K, V]) LoadAndDelete(key K) (V, bool) {
	value, loaded := m.data.LoadAndDelete(key)
	if !loaded {
		var zero V
		return zero, false
	}
	m.length.Add(-1)
	return value.(V), true
}

// Delete deletes the value for a key.
func (m *Map[K, V]) Delete(key K) {
	_, loaded := m.data.LoadAndDelete(key)
	if loaded {
		m.length.Add(-1)
	}
}

// Swap swaps the value for a key and returns the previous value if any.
// The loaded result reports whether the key was present.
func (m *Map[K, V]) Swap(key K, value V) (V, bool) {
	previous, loaded := m.data.Swap(key, value)
	if !loaded {
		m.length.Add(1)

		var zero V
		return zero, false
	}
	return previous.(V), true
}

// CompareAndDelete deletes the entry for key if its value is equal to old.
// This panics if V is not a comparable type.
//
// If there is no current value for key in the map, CompareAndDelete
// returns false.
func (m *Map[K, V]) CompareAndDelete(key K, old V) bool {
	if m.data.CompareAndDelete(key, old) {
		m.length.Add(-1)
		return true
	}
	return false
}

// CompareAndSwap swaps the old and new values for key
// if the value stored in the map is equal to old.
// This panics if V is not a comparable type.
func (m *Map[K, V]) CompareAndSwap(key K, old, new V) bool {
	return m.data.CompareAndSwap(key, old, new)
}

// Clear deletes all the entries, resulting in an empty Map.
func (m *Map[K, V]) Clear() {
	m.data.Clear()
	m.length.Store(0)
}

// Range calls f sequentially for each key and value present in the map.
// If f returns false, range stops the iteration.
//
// Range does not necessarily correspond to any consistent snapshot of the Map's
// contents: no key will be visited more than once, but if the value for any key
// is stored or deleted concurrently (including by f), Range may reflect any
// mapping for that key from any point during the Range call. Range does not
// block other methods on the receiver; even f itself may call any method on m.
//
// Range may be O(N) with the number of elements in the map even if f returns
// false after a constant number of calls.
func (m *Map[K, V]) Range(f func(K, V) bool) {
	m.data.Range(func(key, value any) bool {
		return f(key.(K), value.(V))
	})
}

// All returns an iterator over the keys and values in the map.
func (m *Map[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		m.data.Range(func(key, value any) bool {
			return yield(key.(K), value.(V))
		})
	}
}
