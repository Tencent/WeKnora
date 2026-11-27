package embedding

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPoolLargeBatchSize(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", strconv.Itoa(math.MaxInt))
	var calls atomic.Int32
	embedder := newPooledEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(answer(r.URL.Path, body)))
	})
	var got [][]float32
	var err error
	require.NotPanics(t, func() {
		got, err = embedder.BatchEmbedWithPool(context.Background(), embedder, []string{"a", "bb", "ccc"})
	})
	require.NoError(t, err)
	require.Equal(t, [][]float32{{1}, {2}, {3}}, got)
	require.EqualValues(t, 1, calls.Load())
}
