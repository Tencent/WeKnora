package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type embeddingPanicKBService struct {
	stubKnowledgeBaseService
	withHit bool
}

func (s *embeddingPanicKBService) GetKnowledgeBasesByIDsOnly(
	_ context.Context, ids []string,
) ([]*types.KnowledgeBase, error) {
	kbs := make([]*types.KnowledgeBase, 0, len(ids))
	for _, id := range ids {
		kbs = append(kbs, kbWithIndexes(id, true, false, ""))
	}
	return kbs, nil
}

func (s *embeddingPanicKBService) ResolveEmbeddingModelKeys(
	context.Context, []*types.KnowledgeBase,
) map[string]string {
	return map[string]string{"broken": "broken-model", "healthy": "healthy-model"}
}

func (s *embeddingPanicKBService) GetQueryEmbedding(
	_ context.Context, kbID, _ string,
) ([]float32, error) {
	if kbID == "broken" {
		panic("test: embedding adapter panicked")
	}
	return []float32{1, 0}, nil
}

func (s *embeddingPanicKBService) HybridSearch(
	context.Context, string, types.SearchParams,
) ([]*types.SearchResult, error) {
	if s.withHit {
		return []*types.SearchResult{{
			ID: "chunk", KnowledgeID: "document", KnowledgeBaseID: "healthy",
			Content: "matching evidence", Score: 1,
		}}, nil
	}
	return nil, nil
}

func TestSearchKnowledgeCountsEmbeddingGroupPanics(t *testing.T) {
	for _, tt := range []struct {
		name        string
		kbIDs       []string
		withHit     bool
		wantSuccess bool
	}{
		{name: "only group panics", kbIDs: []string{"broken"}},
		{name: "panic and successful empty search", kbIDs: []string{"broken", "healthy"}, wantSuccess: true},
		{name: "panic and successful hit", kbIDs: []string{"broken", "healthy"}, withHit: true, wantSuccess: true},
		{name: "successful empty search", kbIDs: []string{"healthy"}, wantSuccess: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			targets := make(types.SearchTargets, 0, len(tt.kbIDs))
			for _, id := range tt.kbIDs {
				targets = append(targets, &types.SearchTarget{
					Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: id, TenantID: 1,
				})
			}
			tool := NewSearchKnowledgeTool(&embeddingPanicKBService{withHit: tt.withHit}, nil, nil, targets, nil, nil)

			result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"evidence","mode":"semantic"}`))

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tt.wantSuccess, result.Success)
			if !tt.wantSuccess {
				require.Contains(t, result.Error, "Knowledge search failed")
				require.NotContains(t, result.Output, "No matching chunks")
				return
			}
			require.Empty(t, result.Error)
			if len(tt.kbIDs) > 1 {
				require.Len(t, result.Data["partial_failures"], 1)
			}
			if tt.withHit {
				require.Equal(t, 1, result.Data["count"])
				require.Contains(t, result.Output, "matching evidence")
			} else {
				require.Equal(t, 0, result.Data["count"])
				require.Contains(t, result.Output, "No matching chunks")
			}
		})
	}
}
