package artifact

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type staticAttemptReader struct {
	attempt int
	err     error
}

func (r staticAttemptReader) LatestAttempt(context.Context, string) (int, error) {
	return r.attempt, r.err
}

func TestAttemptFenceRequiresExactPersistedAttempt(t *testing.T) {
	fence := NewAttemptFence(staticAttemptReader{attempt: 3})
	require.NoError(t, fence.EnsureCurrent(context.Background(), "knowledge", 3))
	require.ErrorIs(t, fence.EnsureCurrent(context.Background(), "knowledge", 2), ErrAttemptSuperseded)

	readErr := errors.New("database unavailable")
	fence = NewAttemptFence(staticAttemptReader{err: readErr})
	require.ErrorIs(t, fence.EnsureCurrent(context.Background(), "knowledge", 3), readErr)
}
