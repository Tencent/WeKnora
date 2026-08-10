package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type knowledgeBaseCountRepo struct {
	interfaces.KnowledgeBaseRepository
	rows []*types.KnowledgeBase
}

func (r *knowledgeBaseCountRepo) ListKnowledgeBasesByTenantID(
	_ context.Context,
	tenantID uint64,
) ([]*types.KnowledgeBase, error) {
	rows := make([]*types.KnowledgeBase, 0, len(r.rows))
	for _, kb := range r.rows {
		if kb.TenantID == tenantID {
			copyKB := *kb
			rows = append(rows, &copyKB)
		}
	}
	return rows, nil
}

type knowledgeCountRepo struct {
	interfaces.KnowledgeRepository
	count int64
	err   error
	calls int
}

func (r *knowledgeCountRepo) CountKnowledgeByKnowledgeBaseID(
	context.Context,
	uint64,
	string,
) (int64, error) {
	r.calls++
	return r.count, r.err
}

func (*knowledgeCountRepo) CountKnowledgeByStatus(
	context.Context,
	uint64,
	string,
	[]string,
) (int64, error) {
	return 0, nil
}

type chunkCountRepo struct {
	interfaces.ChunkRepository
	count int64
	err   error
	calls int
}

func (r *chunkCountRepo) CountChunksByKnowledgeBaseID(
	context.Context,
	uint64,
	string,
) (int64, error) {
	r.calls++
	return r.count, r.err
}

func TestListKnowledgeBasesPopulatesBothCountsForEveryType(t *testing.T) {
	rows := []*types.KnowledgeBase{
		{ID: "document-kb", TenantID: 7, Type: types.KnowledgeBaseTypeDocument},
		{ID: "faq-kb", TenantID: 7, Type: types.KnowledgeBaseTypeFAQ},
	}
	knowledgeRepo := &knowledgeCountRepo{count: 4}
	chunkRepo := &chunkCountRepo{count: 149}
	service := &knowledgeBaseService{
		repo:      &knowledgeBaseCountRepo{rows: rows},
		kgRepo:    knowledgeRepo,
		chunkRepo: chunkRepo,
	}

	knowledgeBases, err := service.ListKnowledgeBases(ctxWithTenant(7))

	require.NoError(t, err)
	require.Len(t, knowledgeBases, 2)
	for _, kb := range knowledgeBases {
		assert.Equal(t, int64(4), kb.KnowledgeCount, "knowledge_count for %s", kb.Type)
		assert.Equal(t, int64(149), kb.ChunkCount, "chunk_count for %s", kb.Type)
	}
	assert.Equal(t, 2, knowledgeRepo.calls)
	assert.Equal(t, 2, chunkRepo.calls)
}

func TestListKnowledgeBasesByTenantIDPopulatesBothCountsForEveryType(t *testing.T) {
	rows := []*types.KnowledgeBase{
		{ID: "document-kb", TenantID: 7, Type: types.KnowledgeBaseTypeDocument},
		{ID: "faq-kb", TenantID: 7, Type: types.KnowledgeBaseTypeFAQ},
	}
	knowledgeRepo := &knowledgeCountRepo{count: 4}
	chunkRepo := &chunkCountRepo{count: 149}
	service := &knowledgeBaseService{
		repo:      &knowledgeBaseCountRepo{rows: rows},
		kgRepo:    knowledgeRepo,
		chunkRepo: chunkRepo,
	}

	knowledgeBases, err := service.ListKnowledgeBasesByTenantID(context.Background(), 7)

	require.NoError(t, err)
	require.Len(t, knowledgeBases, 2)
	for _, kb := range knowledgeBases {
		assert.Equal(t, int64(4), kb.KnowledgeCount, "knowledge_count for %s", kb.Type)
		assert.Equal(t, int64(149), kb.ChunkCount, "chunk_count for %s", kb.Type)
	}
	assert.Equal(t, 2, knowledgeRepo.calls)
	assert.Equal(t, 2, chunkRepo.calls)
}

func TestFillKnowledgeBaseCountsContinuesAfterKnowledgeCountFailure(t *testing.T) {
	knowledgeRepo := &knowledgeCountRepo{err: errors.New("knowledge count failed")}
	chunkRepo := &chunkCountRepo{count: 149}
	service := &knowledgeBaseService{kgRepo: knowledgeRepo, chunkRepo: chunkRepo}
	kb := &types.KnowledgeBase{
		ID:       "document-kb",
		TenantID: 7,
		Type:     types.KnowledgeBaseTypeDocument,
	}

	err := service.FillKnowledgeBaseCounts(context.Background(), kb)

	require.NoError(t, err)
	assert.Zero(t, kb.KnowledgeCount)
	assert.Equal(t, int64(149), kb.ChunkCount)
	assert.Equal(t, 1, knowledgeRepo.calls)
	assert.Equal(t, 1, chunkRepo.calls)
}
