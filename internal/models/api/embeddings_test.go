package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type placed struct {
	index  int
	vector []float32
}

func place(want int, reply []placed) ([][]float32, error) {
	return PlaceEmbeddings(want, len(reply), func(i int) (int, []float32) {
		return reply[i].index, reply[i].vector
	})
}

func TestPlaceEmbeddingsHonoursTheReportedIndex(t *testing.T) {
	got, err := place(2, []placed{{1, []float32{2}}, {0, []float32{1}}})
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{1}, {2}}, got)
}

// Every way a reply can fail to cover the inputs exactly once is an error:
// each of them would otherwise store a vector under the wrong chunk or an
// empty one.
func TestPlaceEmbeddingsRefusesAReplyThatDoesNotCoverEachInputOnce(t *testing.T) {
	cases := map[string][]placed{
		"short":        {{0, []float32{1}}},
		"out of range": {{0, []float32{1}}, {2, []float32{2}}},
		"duplicate":    {{0, []float32{1}}, {0, []float32{2}}, {1, []float32{3}}},
		"empty vector": {{0, []float32{1}}, {1, nil}},
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := place(2, reply)
			assert.Error(t, err)
		})
	}
}
