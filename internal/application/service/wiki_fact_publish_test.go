package service

import (
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestBuildWikiFactPublishRequestMapsBlocksAndTrustedSources(t *testing.T) {
	page := &types.WikiPage{
		ID:              "page-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Slug:            "entity/acme",
		Title:           "Acme",
		PageType:        types.WikiPageTypeEntity,
		SourceRefs:      types.StringArray{"knowledge-1|Acme source"},
	}
	output := &wikiFactOutput{
		SchemaVersion: 1,
		Summary:       "Acme profile",
		Blocks: []wikiFactBlock{{
			Type:           types.WikiBlockFact,
			Content:        "Acme was founded in 2020.",
			LogicalBlockID: "fact-acme-founded",
			Citations: []wikiFactCitation{{
				ChunkID:     "chunk-1",
				KnowledgeID: "knowledge-1",
				Role:        types.WikiSourceSupporting,
			}},
		}},
	}
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		FileHash:        "source-hash",
		UpdatedAt:       time.Date(2026, 7, 29, 1, 2, 3, 0, time.UTC),
	}
	evidence := map[string]wikiCitationEvidence{
		"chunk-1": {
			ChunkID:     "chunk-1",
			KnowledgeID: "knowledge-1",
			Content:     "Acme was founded in 2020.",
		},
	}

	request, err := buildWikiFactPublishRequest(
		page,
		output,
		[]*types.Knowledge{knowledge},
		evidence,
		map[string]int{"knowledge-1": 3},
	)
	require.NoError(t, err)
	require.Equal(t, page.ID, request.PageID)
	require.Equal(t, types.WikiPageStatusPublished, request.PageProjection.Status)
	require.Equal(t, renderWikiFactOutput(output), request.PageProjection.Content)
	require.Equal(t, types.StringArray{"chunk-1"}, request.PageProjection.ChunkRefs)
	require.Len(t, request.KnowledgeRevisions, 1)
	require.Equal(t, 3, request.KnowledgeRevisions[0].ParseAttempt)
	require.Equal(t, "source-hash", request.KnowledgeRevisions[0].ContentHash)
	require.Len(t, request.Blocks, 1)
	require.Equal(t, "fact-acme-founded", request.Blocks[0].LogicalBlockID)
	require.Len(t, request.Sources, 1)
	require.Equal(t, "chunk-1", *request.Sources[0].ChunkID)
	require.Equal(t, sha256Hex(evidence["chunk-1"].Content), request.Sources[0].EvidenceHash)
	require.Equal(t, types.WikiSourceValidationVerified, request.Sources[0].ValidationStatus)
	require.NotEmpty(t, request.IdempotencyKey)
}

func TestBuildWikiFactPublishRequestRejectsStaleCitation(t *testing.T) {
	page := &types.WikiPage{
		ID:              "page-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Slug:            "entity/acme",
	}
	output := &wikiFactOutput{Blocks: []wikiFactBlock{{
		Type:    types.WikiBlockFact,
		Content: "claim",
		Citations: []wikiFactCitation{{
			ChunkID: "stale", KnowledgeID: "knowledge-1", Role: types.WikiSourceSupporting,
		}},
	}}}
	knowledge := &types.Knowledge{
		ID: "knowledge-1", TenantID: 7, KnowledgeBaseID: "kb-1", FileHash: "hash",
	}

	_, err := buildWikiFactPublishRequest(page, output, []*types.Knowledge{knowledge}, nil, nil)
	require.ErrorContains(t, err, "stale citation")
}

func TestBuildWikiFactPublishRequestEnforcesKnowledgeScope(t *testing.T) {
	page := &types.WikiPage{
		ID:              "page-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Slug:            "entity/acme",
	}
	output := &wikiFactOutput{Blocks: []wikiFactBlock{{
		Type:    types.WikiBlockFact,
		Content: "claim",
		Citations: []wikiFactCitation{{
			ChunkID: "chunk-1", KnowledgeID: "knowledge-1", Role: types.WikiSourceSupporting,
		}},
	}}}
	outOfScope := &types.Knowledge{
		ID: "knowledge-1", TenantID: 8, KnowledgeBaseID: "kb-1", FileHash: "hash",
	}
	evidence := map[string]wikiCitationEvidence{
		"chunk-1": {ChunkID: "chunk-1", KnowledgeID: "knowledge-1", Content: "source"},
	}

	_, err := buildWikiFactPublishRequest(page, output, []*types.Knowledge{outOfScope}, evidence, nil)
	require.True(t, errors.Is(err, types.ErrWikiPublishScopeNotFound))
}
