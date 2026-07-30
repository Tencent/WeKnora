package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArtifactObservationPreservesRequestAndComputeSemantics(t *testing.T) {
	claimed := ArtifactObservation(IngestionOperationPostprocessSummary, "summary", "abcdef12", ArtifactCacheClaimed)
	require.Zero(t, claimed.RequestCount)
	require.Zero(t, claimed.ComputedItems)
	require.Equal(t, IngestionCacheStatusMiss, claimed.CacheStatus)
	computed := ArtifactObservation(IngestionOperationPostprocessSummary, "summary", "abcdef12", ArtifactCacheComputed)
	require.Equal(t, 1, computed.ComputedItems)
	require.Zero(t, computed.RequestCount)
	hit := ArtifactObservation(IngestionOperationPostprocessSummary, "summary", "abcdef12", ArtifactCacheHit)
	require.Equal(t, 1, hit.ReusedItems)
	require.Equal(t, IngestionCacheStatusHit, hit.CacheStatus)
	for _, event := range []ArtifactCacheEvent{ArtifactCacheHit, ArtifactCacheMiss, ArtifactCacheClaimed, ArtifactCacheBusy, ArtifactCacheComputed, ArtifactCacheFailed, ArtifactCacheLeaseTakeover} {
		observation := ArtifactObservation(IngestionOperationPostprocessSummary, "summary", "abcdef12", event)
		encoded := observation.ToJSONMap()
		require.Equal(t, string(event), encoded["artifact_cache_event"])
		require.Zero(t, observation.RequestCount)
		for _, sensitive := range []string{"payload", "prompt", "image", "owner_token"} {
			require.NotContains(t, encoded, sensitive)
		}
		if event != ArtifactCacheHit {
			require.Zero(t, observation.ReusedItems)
		}
		if event != ArtifactCacheComputed {
			require.Zero(t, observation.ComputedItems)
		}
	}
	failed := ArtifactObservation(IngestionOperationPostprocessSummary, "summary", "abcdef12", ArtifactCacheFailed)
	require.Equal(t, IngestionCacheStatusError, failed.CacheStatus)
	require.False(t, failed.Success)
}
