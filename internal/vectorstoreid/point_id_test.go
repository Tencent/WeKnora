package vectorstoreid

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestStablePointIDContract(t *testing.T) {
	t.Parallel()
	first := StablePointID("source-A")
	require.Equal(t, first, StablePointID("source-A"))
	require.NotEqual(t, first, StablePointID("source-B"))
	_, err := uuid.Parse(first)
	require.NoError(t, err)
}

func TestCompatibleUUIDPointID(t *testing.T) {
	t.Parallel()
	const existing = "5ea772e4-86e7-44e7-a263-3b1f3aac364a"
	require.Equal(t, existing, CompatibleUUIDPointID(existing))
	require.Equal(t, StablePointID("chunk-question"), CompatibleUUIDPointID("chunk-question"))
}
