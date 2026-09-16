package compaction

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestX04AccountingFailureStopsCompactionWithoutFallback(t *testing.T) {
	model := &stubChat{err: fmt.Errorf("ledger timeout: %w", types.ErrModelAccounting)}
	c := New(model, newEstimator(t), testSettings())
	result, err := c.Compact(context.Background(), reactTurn(40), ReasonOverflow)
	require.ErrorIs(t, err, types.ErrModelAccounting)
	require.Nil(t, result)
	require.Equal(t, 1, model.calls)
}
