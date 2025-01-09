package orderedmap

import (
	"cmp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInsertOrder(t *testing.T) {
	m := New[string, int](cmp.Compare[int])
	isFirst, added := m.InsertIfNotExist("first", 100)
	require.True(t, added)
	require.True(t, isFirst)

	for _, val := range []int{1, 9, 3, 4, 10} {
		isFirst, added := m.InsertIfNotExist(strconv.Itoa(val), val)
		require.True(t, added)
		require.False(t, isFirst)
	}

	expectedOrder := []int{100, 1, 3, 4, 9, 10}
	var i int
	m.Foreach()(func(v int) bool {
		assert.Equalf(t, expectedOrder[i], v, "element on index %d is %d, expecting %d", i, v, expectedOrder[i])
		i++
		return true
	})
}
