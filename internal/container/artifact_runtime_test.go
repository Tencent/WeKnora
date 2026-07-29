package container

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestArtifactEventFieldsExcludeKeysPayloadsAndErrorMessages(t *testing.T) {
	fields := artifactEventFields(artifact.Event{
		Kind: artifact.EventStoreFailure,
		Lookup: types.ProcessingArtifactLookup{
			TenantID:    7,
			Stage:       "embedding",
			KeyVersion:  1,
			ArtifactKey: "full-secret-equivalence-key",
		},
		OutputSchema: "embedding.float32.3.v1",
		Reason:       "write_error",
		Err:          errors.New("driver exposed full-secret-equivalence-key and prompt body"),
	})

	assert.Equal(t, "embedding", fields["artifact_stage"])
	assert.Equal(t, "error_fallback", fields["cache_outcome"])
	assert.Equal(t, "*errors.errorString", fields["cache_error_class"])
	assert.NotContains(t, fields, "artifact_key")
	assert.NotContains(t, fields, "tenant_id")
	for _, value := range fields {
		rendered := fmt.Sprint(value)
		assert.NotContains(t, rendered, "full-secret-equivalence-key")
		assert.NotContains(t, rendered, "prompt body")
	}
}

func TestArtifactCacheOutcomeUsesRequiredVocabulary(t *testing.T) {
	assert.Equal(t, "hit", artifactCacheOutcome(artifact.EventHit))
	assert.Equal(t, "miss", artifactCacheOutcome(artifact.EventMiss))
	assert.Equal(t, "computed", artifactCacheOutcome(artifact.EventComputed))
	assert.Equal(t, "wait", artifactCacheOutcome(artifact.EventWait))
	assert.Equal(t, "bypass", artifactCacheOutcome(artifact.EventBypass))
	assert.Equal(t, "corrupt", artifactCacheOutcome(artifact.EventCorrupt))
	assert.Equal(t, "error_fallback", artifactCacheOutcome(artifact.EventStoreFailure))
}
