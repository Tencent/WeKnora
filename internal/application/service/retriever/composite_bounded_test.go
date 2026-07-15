package retriever

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestConcurrentRetrieveUsesBoundedWorkerPool(t *testing.T) {
	params := make([]types.RetrieveParams, 25)
	var active atomic.Int32
	var maximum atomic.Int32
	var completed atomic.Int32

	_, err := concurrentRetrieve(context.Background(), params,
		func(_ context.Context, _ types.RetrieveParams, _ *[]*types.RetrieveResult, _ *sync.Mutex) error {
			current := active.Add(1)
			for {
				observed := maximum.Load()
				if current <= observed || maximum.CompareAndSwap(observed, current) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			active.Add(-1)
			completed.Add(1)
			return nil
		},
	)

	require.NoError(t, err)
	require.EqualValues(t, len(params), completed.Load())
	require.LessOrEqual(t, maximum.Load(), int32(maxConcurrentRetrieveParams))
	require.Greater(t, maximum.Load(), int32(1))
}
