package chatpipeline

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestFilterHistoryResultsHonorsFolderScope(t *testing.T) {
	inside := &types.SearchResult{
		ID:              "inside",
		Content:         "folder scoped answer",
		KnowledgeID:     "doc-inside",
		KnowledgeBaseID: "kb-1",
		FolderID:        "folder-child",
		Score:           1,
	}
	outside := &types.SearchResult{
		ID:              "outside",
		Content:         "folder scoped answer",
		KnowledgeID:     "doc-outside",
		KnowledgeBaseID: "kb-1",
		FolderID:        "folder-other",
		Score:           1,
	}
	legacy := &types.SearchResult{
		ID:              "legacy",
		Content:         "folder scoped answer",
		KnowledgeID:     "doc-legacy",
		KnowledgeBaseID: "kb-1",
		Score:           1,
	}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: "folder scoped answer",
			SearchTargets: types.SearchTargets{{
				Type:            types.SearchTargetTypeKnowledgeBase,
				KnowledgeBaseID: "kb-1",
				FolderIDs:       []string{"folder-root", "folder-child"},
			}},
		},
		PipelineState: types.PipelineState{
			History: []*types.History{{
				KnowledgeReferences: types.References{inside, outside, legacy},
			}},
		},
	}

	got := filterHistoryResults(context.Background(), chatManage, nil)
	require.Len(t, got, 1)
	require.Equal(t, "inside", got[0].ID)
}

func TestFilterHistoryResultsAllowsUnrestrictedTargetAlongsideFolderScope(t *testing.T) {
	unrestricted := &types.SearchResult{
		ID:              "unrestricted",
		Content:         "same current question",
		KnowledgeID:     "doc-2",
		KnowledgeBaseID: "kb-2",
		Score:           1,
	}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: "same current question",
			SearchTargets: types.SearchTargets{
				{
					Type:            types.SearchTargetTypeKnowledgeBase,
					KnowledgeBaseID: "kb-1",
					FolderIDs:       []string{"folder-1"},
				},
				{
					Type:            types.SearchTargetTypeKnowledgeBase,
					KnowledgeBaseID: "kb-2",
				},
			},
		},
		PipelineState: types.PipelineState{
			History: []*types.History{{
				KnowledgeReferences: types.References{unrestricted},
			}},
		},
	}

	got := filterHistoryResults(context.Background(), chatManage, nil)
	require.Len(t, got, 1)
	require.Equal(t, "unrestricted", got[0].ID)
}

func TestFilterHistoryResultsRejectsEmptyKnowledgeScope(t *testing.T) {
	insideFolder := &types.SearchResult{
		ID:              "inside-folder",
		Content:         "same current question",
		KnowledgeID:     "doc-without-selected-tag",
		KnowledgeBaseID: "kb-1",
		FolderID:        "folder-1",
		Score:           1,
	}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: "same current question",
			SearchTargets: types.SearchTargets{{
				Type:            types.SearchTargetTypeKnowledge,
				KnowledgeBaseID: "kb-1",
				FolderIDs:       []string{"folder-1"},
				ScopeTagIDs:     []string{"tag-with-no-documents"},
			}},
		},
		PipelineState: types.PipelineState{
			History: []*types.History{{
				KnowledgeReferences: types.References{insideFolder},
			}},
		},
	}

	got := filterHistoryResults(context.Background(), chatManage, nil)
	require.Empty(t, got)
}

func TestFilterHistoryResultsKeepsLegacyBehaviorWithoutFolderScope(t *testing.T) {
	legacy := &types.SearchResult{
		ID:      "legacy",
		Content: "same current question",
		Score:   1,
	}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{Query: "same current question"},
		PipelineState: types.PipelineState{
			History: []*types.History{{
				KnowledgeReferences: types.References{legacy},
			}},
		},
	}

	got := filterHistoryResults(context.Background(), chatManage, nil)
	require.Len(t, got, 1)
	require.Equal(t, "legacy", got[0].ID)
}
