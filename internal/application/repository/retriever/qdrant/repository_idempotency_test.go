package qdrant

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/vectorstoreid"
	qdrantpb "github.com/qdrant/go-client/qdrant"
	"github.com/stretchr/testify/require"
)

func TestStablePointIDAndLegacyCleanupFilter(t *testing.T) {
	t.Parallel()

	stable := vectorstoreid.StablePointID("source-1")
	cleanup := qdrantLegacyCleanup{
		sourceID: "source-1", stableID: qdrantpb.NewID(stable),
	}
	filter := cleanup.filter()
	require.Len(t, filter.Must, 1)
	require.Len(t, filter.MustNot, 1)
	require.Equal(t, fieldSourceID, filter.Must[0].GetField().GetKey())
	require.Equal(t, "source-1", filter.Must[0].GetField().GetMatch().GetKeyword())
	require.Equal(t, stable, filter.MustNot[0].GetHasId().GetHasId()[0].GetUuid())
}
