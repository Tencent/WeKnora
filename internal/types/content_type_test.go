package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKnowledgeContentTypeValidation(t *testing.T) {
	valid := []KnowledgeContentType{
		KnowledgeContentTypeArticle, KnowledgeContentTypeBook, KnowledgeContentTypeWebpage,
		KnowledgeContentTypeMeetingNotes, KnowledgeContentTypeReport, KnowledgeContentTypePresentation,
		KnowledgeContentTypeSpreadsheet, KnowledgeContentTypeManual, KnowledgeContentTypeOther,
	}
	for _, contentType := range valid {
		require.True(t, contentType.IsValid(), contentType)
	}
	require.False(t, KnowledgeContentType("paper").IsValid())
}

func TestSetContentClassificationPreservesMetadata(t *testing.T) {
	knowledge := &Knowledge{Metadata: JSON(`{"journal_rank":{"found":true}}`)}
	require.NoError(t, knowledge.SetContentClassification(ContentClassificationMetadata{
		SchemaVersion: 1,
		Type:          KnowledgeContentTypeArticle,
		Source:        "manual",
	}))
	var metadata map[string]interface{}
	require.NoError(t, json.Unmarshal(knowledge.Metadata, &metadata))
	require.NotNil(t, metadata["journal_rank"])
	classification, err := knowledge.ContentClassification()
	require.NoError(t, err)
	require.Equal(t, KnowledgeContentTypeArticle, classification.Type)
}
