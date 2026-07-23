package chatpipeline

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestFilterHistoryResultsHonorsFolderUnionAndEmptyBoundary(t *testing.T) {
	references := types.References{
		{ID: "chunk-a", KnowledgeBaseID: "kb-1", KnowledgeID: "doc-a", Content: "pricing guide a"},
		{ID: "chunk-b", KnowledgeBaseID: "kb-1", KnowledgeID: "doc-b", Content: "pricing guide b"},
		{ID: "chunk-sibling", KnowledgeBaseID: "kb-1", KnowledgeID: "doc-sibling", Content: "pricing guide sibling"},
		{ID: "chunk-other-kb", KnowledgeBaseID: "kb-2", KnowledgeID: "doc-other", Content: "pricing guide other"},
	}
	tests := []struct {
		name    string
		scopes  map[string][]string
		wantIDs []string
	}{
		{"multi-folder union", map[string][]string{"kb-1": {"doc-a", "doc-b", "doc-a"}}, []string{"chunk-a", "chunk-b", "chunk-other-kb"}},
		{"restricted empty", map[string][]string{"kb-1": {}}, []string{"chunk-other-kb"}},
		{"legacy unscoped", nil, []string{"chunk-a", "chunk-b", "chunk-sibling"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manage := &types.ChatManage{
				PipelineRequest: types.PipelineRequest{Query: "pricing guide", FolderKnowledgeIDs: tt.scopes},
				PipelineState:   types.PipelineState{RewriteQuery: "pricing guide", History: []*types.History{{KnowledgeReferences: references}}},
			}
			assert.Equal(t, tt.wantIDs, searchResultIDs(filterHistoryResults(context.Background(), manage, nil)))
		})
	}
}
