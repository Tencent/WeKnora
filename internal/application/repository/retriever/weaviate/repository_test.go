package weaviate

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBaseFilterIncludesDisabledOnlyWhenRequested(t *testing.T) {
	repository := &weaviateRepository{}

	defaultFilter := repository.getBaseFilter(types.RetrieveParams{}).Build()
	require.Len(t, defaultFilter.Operands, 1)
	assert.Equal(t, []string{fieldIsEnabled}, defaultFilter.Operands[0].Path)

	adminFilter := repository.getBaseFilter(types.RetrieveParams{
		KnowledgeBaseIDs: []string{"kb-1"},
		IncludeDisabled:  true,
	}).Build()
	require.Len(t, adminFilter.Operands, 1)
	assert.Equal(t, []string{fieldKnowledgeBaseID}, adminFilter.Operands[0].Path)
}
