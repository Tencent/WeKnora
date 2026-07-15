package service

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildStableDocumentChunksAssignsStableLinksAndHashes(t *testing.T) {
	knowledge := &types.Knowledge{ID: "knowledge-1", TenantID: 1, KnowledgeBaseID: "kb-1"}
	parsed := []types.ParsedChunk{
		{Content: "child one", ContextHeader: "Section A", Seq: 2, Start: 10, End: 19, ParentIndex: 0},
		{Content: "child two", Seq: 3, Start: 20, End: 29, ParentIndex: 1},
	}
	parents := []types.ParsedParentChunk{
		{Content: "parent one", Seq: 0, Start: 0, End: 9},
		{Content: "parent two", Seq: 1, Start: 10, End: 29},
	}

	desired, text := buildStableDocumentChunks(knowledge, parsed, parents)

	require.Len(t, desired, 4)
	require.Len(t, text, 2)
	assert.Equal(t, types.ChunkTypeParentText, desired[0].ChunkType)
	assert.Equal(t, types.ChunkTypeParentText, desired[1].ChunkType)
	assert.Equal(t, desired[1].ID, desired[0].NextChunkID)
	assert.Equal(t, desired[0].ID, desired[1].PreChunkID)
	assert.Equal(t, desired[0].ID, text[0].ParentChunkID)
	assert.Equal(t, desired[1].ID, text[1].ParentChunkID)
	assert.Equal(t, text[0].ID, parsed[0].ChunkID)
	assert.Equal(t, text[1].ID, parsed[1].ChunkID)
	assert.Equal(t, "Section A", text[0].ContextHeader)

	for _, chunk := range desired {
		assert.Equal(t, types.ChunkContentHash(chunk.Content), chunk.ContentHash)
	}

	againParsed := []types.ParsedChunk{
		{Content: "child one", ContextHeader: "Section A", Seq: 2, Start: 10, End: 19, ParentIndex: 0},
		{Content: "child two", Seq: 3, Start: 20, End: 29, ParentIndex: 1},
	}
	again, _ := buildStableDocumentChunks(knowledge, againParsed, parents)
	assert.Equal(t, chunkIDs(desired), chunkIDs(again))

	flatParsed := []types.ParsedChunk{{Content: "alpha", Seq: 0}, {Content: "beta", Seq: 1}}
	_, flatText := buildStableDocumentChunks(knowledge, flatParsed, nil)
	assert.Equal(t, flatText[1].ID, flatText[0].NextChunkID)
	assert.Equal(t, flatText[0].ID, flatText[1].PreChunkID)
}

func TestPlanChunkReuseSeparatesReuseIndexAndCleanup(t *testing.T) {
	knowledge := &types.Knowledge{ID: "knowledge-1", TenantID: 1, KnowledgeBaseID: "kb-1", Title: "Doc"}
	desired, text := buildStableDocumentChunks(knowledge, []types.ParsedChunk{
		{Content: "alpha", Seq: 0},
		{Content: "beta", Seq: 1},
	}, nil)
	require.NoError(t, setDesiredEmbeddingFingerprints(text, "model-1", 1536, knowledge.Title))

	alphaOld := cloneChunk(desired[0])
	alphaOld.SeqID = 9
	alphaOld.CreatedAt = time.Unix(123, 0).UTC()
	alphaOld.Flags = types.ChunkFlagRecommended
	alphaOld.RelationChunks = types.JSON(`["relationship-1"]`)
	alphaOld.IndirectRelationChunks = types.JSON(`["relationship-2"]`)
	alphaOld.ImageInfo = `[{"url":"image-1"}]`
	alphaOld.Status = int(types.ChunkStatusIndexed)
	alphaOld.Metadata = types.JSON(`{"questions":["q1"],"_weknora_embedding_fingerprint":"` +
		types.ChunkEmbeddingFingerprint(desired[0].Metadata) + `"}`)
	betaOld := cloneChunk(desired[1])
	betaOld.Status = int(types.ChunkStatusStored)
	stale := &types.Chunk{ID: "stale", ChunkType: types.ChunkTypeText}
	summary := &types.Chunk{ID: "summary", ChunkType: types.ChunkTypeSummary}

	plan, err := planChunkReuse(desired, []*types.Chunk{alphaOld, betaOld, stale, summary}, true)
	require.NoError(t, err)
	assert.Equal(t, []string{alphaOld.ID}, chunkIDs(plan.Reuse))
	assert.Equal(t, []string{betaOld.ID}, chunkIDs(plan.Index))
	assert.Equal(t, []string{"stale"}, chunkIDs(plan.Delete))
	assert.Equal(t, []string{"summary"}, chunkIDs(plan.DeleteGenerated))

	assert.Equal(t, alphaOld.SeqID, desired[0].SeqID)
	assert.Equal(t, alphaOld.CreatedAt, desired[0].CreatedAt)
	assert.Equal(t, alphaOld.Flags, desired[0].Flags)
	assert.Equal(t, alphaOld.RelationChunks, desired[0].RelationChunks)
	assert.Equal(t, alphaOld.IndirectRelationChunks, desired[0].IndirectRelationChunks)
	assert.Equal(t, alphaOld.ImageInfo, desired[0].ImageInfo)
	assert.Equal(t, alphaOld.Status, desired[0].Status)
	assert.Equal(t, betaOld.Status, desired[1].Status)
	values, err := desired[0].Metadata.Map()
	require.NoError(t, err)
	assert.Equal(t, []any{"q1"}, values["questions"])
	assert.Equal(t, types.ChunkEmbeddingFingerprint(alphaOld.Metadata), types.ChunkEmbeddingFingerprint(desired[0].Metadata))
}

func TestPlanChunkReuseIndexesOnlyChangedFingerprint(t *testing.T) {
	knowledge := &types.Knowledge{ID: "knowledge-1", TenantID: 1, KnowledgeBaseID: "kb-1", Title: "Doc"}
	desired, text := buildStableDocumentChunks(knowledge, []types.ParsedChunk{
		{Content: "alpha", Seq: 0},
		{Content: "beta", Seq: 1},
	}, nil)
	require.NoError(t, setDesiredEmbeddingFingerprints(text, "model-1", 1536, knowledge.Title))

	existing := []*types.Chunk{cloneChunk(desired[0]), cloneChunk(desired[1])}
	for _, chunk := range existing {
		chunk.Status = int(types.ChunkStatusIndexed)
	}
	require.NoError(t, setDesiredEmbeddingFingerprints([]*types.Chunk{desired[1]}, "model-2", 1536, knowledge.Title))

	plan, err := planChunkReuse(desired, existing, true)
	require.NoError(t, err)
	assert.Equal(t, []string{desired[0].ID}, chunkIDs(plan.Reuse))
	assert.Equal(t, []string{desired[1].ID}, chunkIDs(plan.Index))
}

func TestPlanChunkReuseWithoutIndexingPreservesExistingMetadata(t *testing.T) {
	desired := []*types.Chunk{{ID: "stable", ChunkType: types.ChunkTypeText}}
	existing := cloneChunk(desired[0])
	existing.Metadata = types.JSON("  {\n" +
		`    "questions": ["q1"],` + "\n" +
		`    "_weknora_embedding_fingerprint": "keep",` + "\n" +
		`    "nested": {"answer": 42}` + "\n" +
		"  }")
	original := append(types.JSON(nil), existing.Metadata...)
	originalValues, err := original.Map()
	require.NoError(t, err)

	plan, err := planChunkReuse(desired, []*types.Chunk{existing}, false)

	require.NoError(t, err)
	require.Len(t, plan.Update, 1)
	assert.Equal(t, original, desired[0].Metadata)
	updatedValues, err := desired[0].Metadata.Map()
	require.NoError(t, err)
	assert.Equal(t, originalValues, updatedValues)
}

func TestPlanChunkReuseWithoutIndexingPreservesMalformedExistingMetadata(t *testing.T) {
	desired := []*types.Chunk{{ID: "stable", ChunkType: types.ChunkTypeText}}
	existing := cloneChunk(desired[0])
	existing.Metadata = types.JSON(`{`)

	plan, err := planChunkReuse(desired, []*types.Chunk{existing}, false)

	require.NoError(t, err)
	require.Len(t, plan.Update, 1)
	assert.Equal(t, types.JSON(`{`), desired[0].Metadata)
}

func TestPlanChunkReuseClassifiesGeneratedAndLeavesUnrelatedChunks(t *testing.T) {
	generated := []*types.Chunk{
		{ID: "summary", ChunkType: types.ChunkTypeSummary},
		{ID: "ocr", ChunkType: types.ChunkTypeImageOCR},
		{ID: "caption", ChunkType: types.ChunkTypeImageCaption},
		{ID: "entity", ChunkType: types.ChunkTypeEntity},
		{ID: "relationship", ChunkType: types.ChunkTypeRelationship},
		{ID: "faq", ChunkType: types.ChunkTypeFAQ},
	}

	plan, err := planChunkReuse(nil, generated, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"summary", "ocr", "caption", "entity", "relationship"}, chunkIDs(plan.DeleteGenerated))
	assert.Empty(t, plan.Delete)
	assert.Empty(t, plan.Create)
	assert.Empty(t, plan.Update)
}

func TestSetDesiredEmbeddingFingerprintsRejectsInvalidMetadata(t *testing.T) {
	err := setDesiredEmbeddingFingerprints([]*types.Chunk{{Content: "alpha", Metadata: types.JSON(`{`)}}, "model-1", 1536, "Doc")
	require.Error(t, err)
}

func cloneChunk(chunk *types.Chunk) *types.Chunk {
	cloned := *chunk
	cloned.Metadata = append(types.JSON(nil), chunk.Metadata...)
	return &cloned
}
