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

// A gateway that merges concurrent requests server-side may number this
// request's inputs within the merged batch (Xinference: 5..9 for a five-input
// request). A contiguous run of the requested length is rebased; the vectors
// still land in relative order.
func TestPlaceEmbeddingsRebasesAMergedBatchRun(t *testing.T) {
	for name, reply := range map[string][]placed{
		"in order":     {{5, []float32{1}}, {6, []float32{2}}, {7, []float32{3}}},
		"out of order": {{7, []float32{3}}, {5, []float32{1}}, {6, []float32{2}}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := place(3, reply)
			require.NoError(t, err)
			assert.Equal(t, [][]float32{{1}, {2}, {3}}, got)
		})
	}
}

// Only a complete contiguous run is rebased. Gaps, duplicates and a reply
// with the wrong count stay errors, so a genuinely broken response cannot be
// mistaken for a merged-batch offset.
func TestPlaceEmbeddingsRefusesAnIncompleteMergedBatchRun(t *testing.T) {
	cases := map[string][]placed{
		"gapped":      {{5, []float32{1}}, {6, []float32{2}}, {8, []float32{3}}},
		"duplicate":   {{5, []float32{1}}, {5, []float32{2}}, {6, []float32{3}}},
		"short count": {{5, []float32{1}}, {6, []float32{2}}},
		"extra count": {{5, []float32{1}}, {6, []float32{2}}, {7, []float32{3}}, {8, []float32{4}}},
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := place(3, reply)
			assert.Error(t, err)
		})
	}
}
