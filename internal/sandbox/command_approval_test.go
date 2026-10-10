package sandbox

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandApprovalRoundTripsThroughContext(t *testing.T) {
	_, ok := CommandApprovalFrom(context.Background())
	require.False(t, ok)

	ctx := WithCommandApproval(context.Background(), CommandApproval{Command: "rm a"})
	got, ok := CommandApprovalFrom(ctx)
	require.True(t, ok)
	require.Equal(t, "rm a", got.Command)
	require.Nil(t, got.Approver)
}
