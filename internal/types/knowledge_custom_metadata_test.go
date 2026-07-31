package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKnowledgeCustomMetadataTextIsStableAndSeparate(t *testing.T) {
	knowledge := &Knowledge{
		Metadata:       JSON(`{"external_id":"secret-internal-id"}`),
		CustomMetadata: JSON(`{"region":"Shanghai","department":"R&D"}`),
	}
	require.Equal(t, "department: R&D\nregion: Shanghai", knowledge.CustomMetadataText())
	require.NotContains(t, knowledge.CustomMetadataText(), "external_id")
}
