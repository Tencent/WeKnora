package embedding

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestContextWithFallbackTimeoutRespectsShorterParentDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	ctx, fallbackCancel := contextWithFallbackTimeout(parent, time.Minute)
	defer fallbackCancel()

	parentDeadline, ok := parent.Deadline()
	require.True(t, ok)
	gotDeadline, ok := ctx.Deadline()
	require.True(t, ok)
	require.Equal(t, parentDeadline, gotDeadline)
}

func TestContextWithFallbackTimeoutAddsDeadlineWhenMissing(t *testing.T) {
	ctx, cancel := contextWithFallbackTimeout(context.Background(), time.Minute)
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	require.Greater(t, time.Until(deadline), 50*time.Second)
}
