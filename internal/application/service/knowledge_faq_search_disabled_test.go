package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type faqSearchKnowledgeBaseServiceStub struct {
	interfaces.KnowledgeBaseService
	searchParams types.SearchParams
}

func (s *faqSearchKnowledgeBaseServiceStub) GetKnowledgeBaseByID(
	_ context.Context, id string,
) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: id, Type: types.KnowledgeBaseTypeFAQ}, nil
}

func (s *faqSearchKnowledgeBaseServiceStub) HybridSearch(
	_ context.Context, _ string, params types.SearchParams,
) ([]*types.SearchResult, error) {
	s.searchParams = params
	return []*types.SearchResult{{ID: "faq-disabled", Score: 0.9}}, nil
}

type faqSearchChunkRepositoryStub struct {
	interfaces.ChunkRepository
}

func (s *faqSearchChunkRepositoryStub) ListChunksByID(
	_ context.Context, tenantID uint64, ids []string,
) ([]*types.Chunk, error) {
	return []*types.Chunk{{
		ID:              ids[0],
		TenantID:        tenantID,
		KnowledgeID:     "faq-knowledge",
		KnowledgeBaseID: "faq-kb",
		ChunkType:       types.ChunkTypeFAQ,
		IsEnabled:       false,
	}}, nil
}

func TestSearchFAQEntriesIncludesDisabledOnlyWhenRequested(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		includeDisabled bool
		wantEntries     int
	}{
		{name: "default excludes disabled"},
		{name: "administrative opt-in includes disabled", includeDisabled: true, wantEntries: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			kbService := &faqSearchKnowledgeBaseServiceStub{}
			service := &knowledgeService{
				kbService: kbService,
				chunkRepo: &faqSearchChunkRepositoryStub{},
			}
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

			entries, err := service.SearchFAQEntries(ctx, "faq-kb", &types.FAQSearchRequest{
				QueryText:       "refund",
				IncludeDisabled: testCase.includeDisabled,
			})
			require.NoError(t, err)
			require.Len(t, entries, testCase.wantEntries)
			require.Equal(t, testCase.includeDisabled, kbService.searchParams.IncludeDisabled)
		})
	}
}
