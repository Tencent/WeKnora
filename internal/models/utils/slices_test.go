package utils

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChunkSlice(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []int
		size int
		want [][]int
	}{
		{"empty input", nil, 0, [][]int{}},
		{"single item", []int{1}, 1, [][]int{{1}}},
		{"even batches", []int{1, 2, 3, 4}, 2, [][]int{{1, 2}, {3, 4}}},
		{"last partial batch", []int{1, 2, 3}, 2, [][]int{{1, 2}, {3}}},
		{"batch larger than input", []int{1, 2, 3}, 10, [][]int{{1, 2, 3}}},
		{"maximum batch size", []int{1, 2, 3}, math.MaxInt, [][]int{{1, 2, 3}}},
		{"near maximum batch size", []int{1, 2, 3, 4}, math.MaxInt - 1, [][]int{{1, 2, 3, 4}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ChunkSlice(tc.in, tc.size))
		})
	}
	for _, size := range []int{0, -1} {
		require.Panics(t, func() { ChunkSlice([]int{1}, size) })
	}
}
