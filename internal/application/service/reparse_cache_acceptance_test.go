package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldWipeGraphForRematerialize(t *testing.T) {
	assert.False(t, shouldWipeGraphForRematerialize(3, 0), "unchanged reparse must keep graph")
	assert.True(t, shouldWipeGraphForRematerialize(0, 0), "full rebuild wipes graph")
	assert.True(t, shouldWipeGraphForRematerialize(2, 1), "stale deletes require wipe to avoid orphans")
	assert.True(t, shouldWipeGraphForRematerialize(0, 5), "full rebuild with deletes wipes")
}

func TestSummaryFingerprint_MatchesBuildSummaryUserContent(t *testing.T) {
	svc := &knowledgeService{}
	knowledge := &types.Knowledge{ID: "k1", TenantID: 1}
	chunks := []*types.Chunk{
		{ID: "c1", Content: "Hello world section one.", StartAt: 0, ChunkType: types.ChunkTypeText},
		{ID: "c2", Content: "Section two continues.", StartAt: 24, ChunkType: types.ChunkTypeText},
	}
	user1, _, err := svc.buildSummaryUserContent(context.Background(), knowledge, chunks)
	require.NoError(t, err)
	user2, _, err := svc.buildSummaryUserContent(context.Background(), knowledge, chunks)
	require.NoError(t, err)
	assert.Equal(t, user1, user2)

	fp1 := calculateSummaryCacheFingerprint(user1, "model-a", "prompt-a", 2048, "zh")
	fp2 := calculateSummaryCacheFingerprint(user2, "model-a", "prompt-a", 2048, "zh")
	assert.Equal(t, fp1, fp2)
	assert.NotEqual(t, fp1, calculateSummaryCacheFingerprint(user1+"x", "model-a", "prompt-a", 2048, "zh"))
	assert.NotEqual(t, fp1, calculateSummaryCacheFingerprint(user1, "model-b", "prompt-a", 2048, "zh"))
}

func TestSummaryCacheRoundTrip_Metadata(t *testing.T) {
	fp := calculateSummaryCacheFingerprint("body", "m", "p", 100, "en")
	meta := knowledgeMetadataWithSummaryCache(nil, fp, "cached summary text")
	got, ok := summaryCacheFromKnowledgeMetadata(meta, fp)
	require.True(t, ok)
	assert.Equal(t, "cached summary text", got)
	_, ok = summaryCacheFromKnowledgeMetadata(meta, "other")
	assert.False(t, ok)
}

func TestParseCacheRoundTrip_Metadata(t *testing.T) {
	fp := calculateParseCacheFingerprint("filehash", "engine", "pdf", "", map[string]string{"a": "1"})
	result := &types.ReadResult{MarkdownContent: "# title\nbody", ImageRefs: []types.ImageRef{{Filename: "a.png", StorageKey: "k"}}}
	meta := knowledgeMetadataWithParseCache(nil, fp, result)
	got, ok := parseCacheFromKnowledgeMetadata(meta, fp)
	require.True(t, ok)
	assert.Equal(t, "# title\nbody", got.MarkdownContent)
	require.Len(t, got.ImageRefs, 1)
	assert.Equal(t, "k", got.ImageRefs[0].StorageKey)
	assert.Nil(t, got.ImageRefs[0].ImageData, "inline image bytes must not be stored in metadata cache")
}

func TestQuestionCacheMetadata_HitSkipsWhenFingerprintMatches(t *testing.T) {
	chunk := &types.Chunk{ID: "c1", Content: "body", ChunkType: types.ChunkTypeText}
	fp := calculateQuestionCacheFingerprint("body", "", "", "title", 3, "model", "prompt", "instr", "zh")
	require.NoError(t, chunk.SetDocumentMetadata(&types.DocumentChunkMetadata{
		GeneratedQuestions:       []types.GeneratedQuestion{{ID: "q1", Question: "What?"}},
		QuestionCacheFingerprint: fp,
	}))
	meta, err := chunk.DocumentMetadata()
	require.NoError(t, err)
	assert.Equal(t, fp, meta.QuestionCacheFingerprint)
	assert.Len(t, meta.GeneratedQuestions, 1)

	// Same inputs → same fp → hit
	assert.Equal(t, fp, calculateQuestionCacheFingerprint("body", "", "", "title", 3, "model", "prompt", "instr", "zh"))
	// Instruction change → miss
	assert.NotEqual(t, fp, calculateQuestionCacheFingerprint("body", "", "", "title", 3, "model", "prompt", "other", "zh"))
}

func TestGraphExtractCacheMetadata_PayloadRoundTrip(t *testing.T) {
	chunk := &types.Chunk{ID: "c1", Content: "entity text", ChunkType: types.ChunkTypeText}
	fp := calculateGraphExtractFingerprint("entity text", "model", "desc", "instr", []string{"tag"})
	payload := &types.GraphData{
		Node:     []*types.GraphNode{{Name: "Alice", Chunks: []string{"c1"}}},
		Relation: []*types.GraphRelation{{Node1: "Alice", Node2: "Bob", Type: "knows"}},
	}
	require.NoError(t, chunk.SetDocumentMetadata(&types.DocumentChunkMetadata{
		GraphExtractFingerprint: fp,
		GraphPayload:            payload,
	}))
	meta, err := chunk.DocumentMetadata()
	require.NoError(t, err)
	require.NotNil(t, meta.GraphPayload)
	assert.Equal(t, fp, meta.GraphExtractFingerprint)
	assert.Equal(t, "Alice", meta.GraphPayload.Node[0].Name)
	// hit condition used by extract.go
	assert.True(t, meta.GraphExtractFingerprint == fp && meta.GraphPayload != nil)
}

func TestStableIDs_AllowContentAddressedReuseAcrossRuns(t *testing.T) {
	hash := types.CalculateDocumentChunkContentHash("same", "", types.ChunkTypeText, "emb", "chunking")
	id1 := types.StableDocumentChunkID("doc-1", hash, types.ChunkIDRoleText)
	id2 := types.StableDocumentChunkID("doc-1", hash, types.ChunkIDRoleText)
	assert.Equal(t, id1, id2)
}

// TestReparseAcceptance_LayerChecklist documents the #1679 acceptance gates
// covered by unit tests in this package. End-to-end processChunks+asynq wiring
// is integration-heavy; each expensive layer is proven via fingerprint stability
// + metadata hit conditions that the production handlers consult before Chat/VLM.
func TestReparseAcceptance_LayerChecklist(t *testing.T) {
	// M1/M2 text chunk + stable ID
	TestStableIDs_AllowContentAddressedReuseAcrossRuns(t)
	// M5 summary
	TestSummaryFingerprint_MatchesBuildSummaryUserContent(t)
	TestSummaryCacheRoundTrip_Metadata(t)
	// M6 question
	TestQuestionCacheMetadata_HitSkipsWhenFingerprintMatches(t)
	// M7 graph
	TestShouldWipeGraphForRematerialize(t)
	TestGraphExtractCacheMetadata_PayloadRoundTrip(t)
	// M8 parse
	TestParseCacheRoundTrip_Metadata(t)
	// M3 multimodal + M4 wiki map + layered invalidation already in cache_fingerprint_test.go
}
