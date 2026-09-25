package chatpipeline

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A document appearing in past answers means the retriever kept picking it,
// not that the user found it useful. The boost it earns has to stay small
// enough to break ties between comparable passages and never large enough to
// drag an unrelated document to the top.
func TestAffinityBoostIsBoundedAndSaturates(t *testing.T) {
	require.Equal(t, 1.0, affinityFactor(1),
		"a single sighting is not evidence of anything")
	require.Greater(t, affinityFactor(2), 1.0)
	require.Greater(t, affinityFactor(8), affinityFactor(3))
	require.LessOrEqual(t, affinityFactor(1000), affinityMaxBoost,
		"familiarity must not become a feedback loop that locks someone in")

	// The tenth reuse counts for far less than the second.
	require.Less(t, affinityFactor(10)-affinityFactor(8), affinityFactor(3)-affinityFactor(2))
}
