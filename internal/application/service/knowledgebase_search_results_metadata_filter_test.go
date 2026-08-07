package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type metadataFilterResultChunkRepo struct {
	interfaces.ChunkRepository
	chunks map[string]*types.Chunk
}

func (r *metadataFilterResultChunkRepo) ListChunksByID(
	_ context.Context, _ uint64, ids []string,
) ([]*types.Chunk, error) {
	chunks := make([]*types.Chunk, 0, len(ids))
	for _, id := range ids {
		if chunk := r.chunks[id]; chunk != nil {
			chunks = append(chunks, chunk)
		}
	}
	return chunks, nil
}

func (r *metadataFilterResultChunkRepo) ListChunksByParentIDs(
	_ context.Context, _ uint64, _ []string,
) ([]*types.Chunk, error) {
	return nil, nil
}

type metadataFilterResultKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge map[string]*types.Knowledge
}

func (r *metadataFilterResultKnowledgeRepo) GetKnowledgeBatch(
	_ context.Context, _ uint64, ids []string,
) ([]*types.Knowledge, error) {
	knowledges := make([]*types.Knowledge, 0, len(ids))
	for _, id := range ids {
		if knowledge := r.knowledge[id]; knowledge != nil {
			knowledges = append(knowledges, knowledge)
		}
	}
	return knowledges, nil
}

func newMetadataFilterResultService(chunks map[string]*types.Chunk) *knowledgeBaseService {
	return &knowledgeBaseService{
		chunkRepo: &metadataFilterResultChunkRepo{chunks: chunks},
		kgRepo: &metadataFilterResultKnowledgeRepo{knowledge: map[string]*types.Knowledge{
			"knowledge": {
				ID: "knowledge", KnowledgeBaseID: "kb", Title: "metadata filter test",
				Description: "document-wide summary containing restricted chunk content",
			},
		}},
	}
}

func metadataFilterResultContext() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
}

func metadataForDepartment(department string) types.JSON {
	return types.JSON(`{"access_metadata":{"department":"` + department + `"}}`)
}

func metadataFilterResultChunk(id string, metadata types.JSON) *types.Chunk {
	return &types.Chunk{
		ID:          id,
		KnowledgeID: "knowledge",
		Content:     id,
		ChunkType:   types.ChunkTypeText,
		IsEnabled:   true,
		IndexStatus: "ready",
		Metadata:    metadata,
	}
}

func metadataFilterForResearch() *types.MetadataFilter {
	return &types.MetadataFilter{Field: "department", Op: types.MetadataFilterOpEqual, Value: "research"}
}

func searchResultIDs(results []*types.SearchResult) []string {
	ids := make([]string, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.ID)
	}
	return ids
}

func TestProcessSearchResultsMetadataFilterExcludesDisallowedNearbyAndAccessMetadata(t *testing.T) {
	metadata := types.JSON(`{"access_metadata":{"department":"research"},"label":"visible"}`)
	primary := metadataFilterResultChunk("primary", metadata)
	primary.NextChunkID = "nearby"
	nearby := metadataFilterResultChunk("nearby", metadataForDepartment("finance"))
	service := newMetadataFilterResultService(map[string]*types.Chunk{
		primary.ID: primary,
		nearby.ID:  nearby,
	})

	results, err := service.processSearchResults(metadataFilterResultContext(), []*types.IndexWithScore{{
		ChunkID: primary.ID, KnowledgeID: primary.KnowledgeID, Score: 1,
	}}, false, metadataFilterForResearch())
	if err != nil {
		t.Fatalf("processSearchResults() error = %v", err)
	}
	if got := searchResultIDs(results); len(got) != 1 || got[0] != primary.ID {
		t.Fatalf("result IDs = %v, want [%s]", got, primary.ID)
	}
	var chunkMetadata map[string]json.RawMessage
	if err := json.Unmarshal(results[0].ChunkMetadata, &chunkMetadata); err != nil {
		t.Fatalf("decode result chunk metadata: %v", err)
	}
	if _, exists := chunkMetadata["access_metadata"]; exists {
		t.Fatal("access metadata must not be included in SearchResult")
	}
	if string(chunkMetadata["label"]) != `"visible"` {
		t.Fatalf("public chunk metadata = %s, want visible label", chunkMetadata["label"])
	}
	if results[0].KnowledgeDescription != "" {
		t.Fatalf("filtered result leaked knowledge description %q", results[0].KnowledgeDescription)
	}
}

func TestProcessSearchResultsMetadataFilterExcludesSummaryChunks(t *testing.T) {
	summary := metadataFilterResultChunk("summary", metadataForDepartment("research"))
	summary.ChunkType = types.ChunkTypeSummary
	summary.Content = "summary assembled from chunks with different access metadata"
	service := newMetadataFilterResultService(map[string]*types.Chunk{summary.ID: summary})

	results, err := service.processSearchResults(metadataFilterResultContext(), []*types.IndexWithScore{{
		ChunkID: summary.ID, KnowledgeID: summary.KnowledgeID, Score: 1,
	}}, true, metadataFilterForResearch())
	if err != nil {
		t.Fatalf("processSearchResults() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("filtered result returned summary content: %#v", results)
	}
}

func TestProcessSearchResultsMetadataFilterStillFiltersPrimaryWhenEnrichmentSkipped(t *testing.T) {
	disallowed := metadataFilterResultChunk("disallowed", metadataForDepartment("finance"))
	service := newMetadataFilterResultService(map[string]*types.Chunk{disallowed.ID: disallowed})

	results, err := service.processSearchResults(metadataFilterResultContext(), []*types.IndexWithScore{{
		ChunkID: disallowed.ID, KnowledgeID: disallowed.KnowledgeID, Score: 1,
	}}, true, metadataFilterForResearch())
	if err != nil {
		t.Fatalf("processSearchResults() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("result IDs = %v, want no disallowed primary result", searchResultIDs(results))
	}
}

func TestProcessSearchResultsMetadataFilterExcludesMalformedAccessMetadata(t *testing.T) {
	t.Run("primary", func(t *testing.T) {
		primary := metadataFilterResultChunk("malformed-primary", types.JSON(`{"access_metadata":"not-an-object"}`))
		service := newMetadataFilterResultService(map[string]*types.Chunk{primary.ID: primary})

		results, err := service.processSearchResults(metadataFilterResultContext(), []*types.IndexWithScore{{
			ChunkID: primary.ID, KnowledgeID: primary.KnowledgeID, Score: 1,
		}}, true, metadataFilterForResearch())
		if err != nil {
			t.Fatalf("processSearchResults() error = %v", err)
		}
		if len(results) != 0 {
			t.Fatalf("result IDs = %v, want no malformed primary result", searchResultIDs(results))
		}
	})

	t.Run("enrichment", func(t *testing.T) {
		primary := metadataFilterResultChunk("primary", metadataForDepartment("research"))
		primary.NextChunkID = "malformed-nearby"
		nearby := metadataFilterResultChunk("malformed-nearby", types.JSON(`{"access_metadata":"not-an-object"}`))
		service := newMetadataFilterResultService(map[string]*types.Chunk{
			primary.ID: primary,
			nearby.ID:  nearby,
		})

		results, err := service.processSearchResults(metadataFilterResultContext(), []*types.IndexWithScore{{
			ChunkID: primary.ID, KnowledgeID: primary.KnowledgeID, Score: 1,
		}}, false, metadataFilterForResearch())
		if err != nil {
			t.Fatalf("processSearchResults() error = %v", err)
		}
		if got := searchResultIDs(results); len(got) != 1 || got[0] != primary.ID {
			t.Fatalf("result IDs = %v, want only allowed primary", got)
		}
	})
}

func TestProcessSearchResultsMetadataFilterExcludesDisallowedParentAndRelation(t *testing.T) {
	primary := metadataFilterResultChunk("primary", metadataForDepartment("research"))
	primary.ParentChunkID = "parent"
	primary.RelationChunks = types.JSON(`["relation"]`)
	parent := metadataFilterResultChunk("parent", metadataForDepartment("finance"))
	relation := metadataFilterResultChunk("relation", metadataForDepartment("finance"))
	service := newMetadataFilterResultService(map[string]*types.Chunk{
		primary.ID:  primary,
		parent.ID:   parent,
		relation.ID: relation,
	})

	results, err := service.processSearchResults(metadataFilterResultContext(), []*types.IndexWithScore{{
		ChunkID: primary.ID, KnowledgeID: primary.KnowledgeID, Score: 1,
	}}, false, metadataFilterForResearch())
	if err != nil {
		t.Fatalf("processSearchResults() error = %v", err)
	}
	if got := searchResultIDs(results); len(got) != 1 || got[0] != primary.ID {
		t.Fatalf("result IDs = %v, want only allowed primary", got)
	}
}

func TestProcessSearchResultsMetadataFilterExcludesDisallowedSecondLevelParent(t *testing.T) {
	primary := metadataFilterResultChunk("primary", metadataForDepartment("research"))
	primary.ChunkType = types.ChunkTypeImageOCR
	primary.ParentChunkID = "intermediate"
	intermediate := metadataFilterResultChunk("intermediate", metadataForDepartment("research"))
	intermediate.ParentChunkID = "second-level-parent"
	secondLevelParent := metadataFilterResultChunk("second-level-parent", metadataForDepartment("finance"))
	service := newMetadataFilterResultService(map[string]*types.Chunk{
		primary.ID:           primary,
		intermediate.ID:      intermediate,
		secondLevelParent.ID: secondLevelParent,
	})

	results, err := service.processSearchResults(metadataFilterResultContext(), []*types.IndexWithScore{{
		ChunkID: primary.ID, KnowledgeID: primary.KnowledgeID, Score: 1,
	}}, false, metadataFilterForResearch())
	if err != nil {
		t.Fatalf("processSearchResults() error = %v", err)
	}
	if got := searchResultIDs(results); len(got) != 2 || got[0] != primary.ID || got[1] != intermediate.ID {
		t.Fatalf("result IDs = %v, want [%s %s]", got, primary.ID, intermediate.ID)
	}
}
