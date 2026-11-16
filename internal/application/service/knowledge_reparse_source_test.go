package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// A FAQ knowledge base keeps its entries in a container knowledge row that has
// no stored file and no URL. Rebuilding the index reparses every row of the KB;
// for this row the content check used to run after cleanupKnowledgeResources had
// already deleted the chunks and vectors, so the rebuild destroyed the index and
// only then failed with "Knowledge has no parseable content".
func TestReparseKeepsIndexForKnowledgeWithoutSource(t *testing.T) {
	f := newDocumentWriteFixture(t)
	row := &types.Knowledge{
		ID:              "faq-container",
		TenantID:        7,
		KnowledgeBaseID: "kb",
		Title:           "FAQ",
		Type:            types.KnowledgeTypeFAQ,
		Source:          types.KnowledgeTypeFAQ,
		ParseStatus:     types.ParseStatusCompleted,
		EnableStatus:    "enabled",
	}
	require.NoError(t, f.db.Create(row).Error)
	require.NoError(t, f.db.Create(&types.Chunk{
		ID:              "faq-chunk",
		TenantID:        7,
		KnowledgeID:     row.ID,
		KnowledgeBaseID: "kb",
		ChunkType:       types.ChunkTypeText,
		Content:         "question",
		IndexStatus:     "ready",
	}).Error)

	_, err := f.svc.ReparseKnowledge(f.ctx, row.ID, nil)
	require.ErrorContains(t, err, "no parseable content")

	require.Zero(t, f.chunkRepo.writes, "cleanup must not delete the index before the source check")
	require.Zero(t, f.graph.calls)

	var remaining int64
	require.NoError(t, f.db.Model(&types.Chunk{}).Where("knowledge_id = ?", row.ID).Count(&remaining).Error)
	require.EqualValues(t, 1, remaining)

	stored, err := f.repo.GetKnowledgeByID(f.ctx, 7, row.ID)
	require.NoError(t, err)
	require.Equal(t, types.ParseStatusCompleted, stored.ParseStatus)
}
