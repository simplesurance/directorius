package orderedmap

import (
	"iter"

	"github.com/simplesurance/directorius/internal/linkedlist"
)

// Map is a map data structure that allows accessing it's element in a
// fixed order.
type Map[K comparable, V any] struct {
	order   *linkedlist.List[V]
	m       map[K]*linkedlist.Element[V]
	cmp     func(a, b V) int
	zeroval V
}

// New creates a new Map that orders inserted elements in ascending order
// after the first element accordingly to the cmp function.
// The position of the first element is fixed!
func New[K comparable, V any](cmp func(a, b V) int) *Map[K, V] {
	return &Map[K, V]{
		order: linkedlist.New[V](),
		m:     map[K]*linkedlist.Element[V]{},
		cmp:   cmp,
	}
}

// InsertIfNotExist adds val to the map if K does not exist.
// val is inserted at the position determined by the cmp function, but always
// after the first element.
func (m *Map[K, V]) InsertIfNotExist(key K, val V) (isFirst, added bool) {
	if _, exist := m.m[key]; exist {
		return false, false
	}

	elem := m.insertList(val)
	m.m[key] = elem

	return m.order.Len() == 1, true
}

func (m *Map[K, V]) insertList(val V) *linkedlist.Element[V] {
	if m.order.Len() < 2 {
		// The first element in the list is fixed!
		// append if we only have 1 element or an empty list.
		return m.order.PushBack(val)
	}

	for e := m.order.Front().Next(); e != nil; e = e.Next() {
		if m.cmp(val, e.Value) < 0 {
			return m.order.InsertBefore(val, e)
		}
	}
	return m.order.PushBack(val)
}

// Get returns the value for the given key.
// If the key does not exist, the zero value is returned
func (m *Map[K, V]) Get(key K) V {
	v, exist := m.m[key]
	if !exist {
		return m.zeroval
	}

	return v.Value
}

// Dequeue removes the value with the key from the map and returns it.
// If the key does not exist in the map, the zero value is returned.
func (m *Map[K, V]) Dequeue(key K) (removedElem V) {
	v, exist := m.m[key]
	if !exist {
		return m.zeroval
	}
	delete(m.m, key)

	return m.order.Remove(v)
}

// First returns the first element in the map.
// If the map is empty, the zero value is returned.
func (m *Map[K, V]) First() V {
	if e := m.order.Front(); e != nil {
		return e.Value
	}

	return m.zeroval
}

// Len returns the number of elements in the maps.
func (m *Map[K, V]) Len() int {
	return m.order.Len()
}

// Foreach returns an ordered iterator over the values.
func (m *Map[K, V]) Foreach() iter.Seq[V] {
	return m.order.Foreach()
}

// AsSlice returns a new slice containing the elements of the orderedMap in
// order.
func (m *Map[K, V]) AsSlice() []V {
	result := make([]V, 0, m.order.Len())

	for e := m.order.Front(); e != nil; e = e.Next() {
		result = append(result, e.Value)
	}

	return result
}
