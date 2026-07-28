package artifact

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func entityState(id, match, content, artifactValue, metadata string) EntityState {
	return EntityState{
		ID:             id,
		MatchKey:       match,
		ContentDigest:  SHA256Hex([]byte(content)),
		ArtifactDigest: SHA256Hex([]byte(artifactValue)),
		MetadataDigest: SHA256Hex([]byte(metadata)),
	}
}

func TestPlanDesiredStateClassifiesExactChanges(t *testing.T) {
	kept := entityState("kept", "m1", "a", "p1", "meta")
	metadataLive := entityState("metadata", "m2", "b", "p2", "old")
	metadataDesired := entityState("metadata", "m2", "b", "p2", "new")
	changedLive := entityState("old-id", "m3", "old", "p3", "meta")
	changedDesired := entityState("new-id", "m3", "new", "p4", "meta")
	stale := entityState("stale", "m4", "d", "p5", "meta")
	added := entityState("added", "m5", "e", "p6", "meta")

	plan, err := PlanDesiredState(
		[]EntityState{kept, metadataDesired, changedDesired, added},
		[]EntityState{kept, metadataLive, changedLive, stale},
	)
	require.NoError(t, err)

	require.Len(t, plan.Kept, 1)
	require.Len(t, plan.MetadataOnly, 1)
	require.Len(t, plan.Changed, 1)
	require.Len(t, plan.Added, 1)
	require.Len(t, plan.Stale, 1)
	assert.Equal(t, "stale", plan.Stale[0].ID)
}

func TestPlanDesiredStateRejectsAmbiguousMatches(t *testing.T) {
	first := entityState("one", "duplicate", "a", "p", "m")
	second := entityState("two", "duplicate", "b", "p", "m")
	_, err := PlanDesiredState(nil, []EntityState{first, second})
	require.ErrorContains(t, err, "duplicate live")
}
