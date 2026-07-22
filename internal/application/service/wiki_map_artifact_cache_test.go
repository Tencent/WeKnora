package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWikiMapPromptDigestCoversInjectedGuidance(t *testing.T) {
	prompts, err := json.Marshal([]string{
		agent.WikiCandidateSlugPrompt,
		agent.WikiKnowledgeExtractPrompt,
		agent.WikiDeduplicationPrompt,
		agent.WikiSummaryPrompt,
		agent.WikiChunkCitationPrompt,
		agent.WikiGranularityGuidanceFocused,
		agent.WikiGranularityGuidanceStandard,
		agent.WikiGranularityGuidanceExhaustive,
	})
	require.NoError(t, err)
	want := sha256.Sum256(prompts)

	assert.Equal(t, want[:], wikiMapPromptDigest())
}

func TestWikiMapArtifactKeyInvalidatesCanonicalInputs(t *testing.T) {
	request := testWikiMapArtifactRequest()
	base, err := newWikiMapArtifactKey(request)
	require.NoError(t, err)
	assert.Equal(t, wikiMapArtifactStage, base.Stage)

	stable, err := newWikiMapArtifactKey(request)
	require.NoError(t, err)
	assert.Equal(t, base, stable)

	tests := []struct {
		name   string
		mutate func(*wikiMapArtifactRequest)
	}{
		{name: "tenant", mutate: func(r *wikiMapArtifactRequest) { r.tenantID++ }},
		{name: "knowledge base", mutate: func(r *wikiMapArtifactRequest) { r.knowledgeBaseID += " changed" }},
		{name: "knowledge", mutate: func(r *wikiMapArtifactRequest) { r.knowledgeID += " changed" }},
		{name: "model ID", mutate: func(r *wikiMapArtifactRequest) { r.modelID += " changed" }},
		{name: "model name", mutate: func(r *wikiMapArtifactRequest) { r.modelName += " changed" }},
		{name: "content", mutate: func(r *wikiMapArtifactRequest) { r.content += " changed" }},
		{name: "chunk content", mutate: func(r *wikiMapArtifactRequest) { r.chunks[0].Content += " changed" }},
		{name: "chunk index", mutate: func(r *wikiMapArtifactRequest) { r.chunks[0].ChunkIndex++ }},
		{name: "chunk start", mutate: func(r *wikiMapArtifactRequest) { r.chunks[0].StartAt++ }},
		{name: "language", mutate: func(r *wikiMapArtifactRequest) { r.language = "English" }},
		{name: "granularity", mutate: func(r *wikiMapArtifactRequest) { r.granularity = types.WikiExtractionExhaustive }},
		{name: "content instructions", mutate: func(r *wikiMapArtifactRequest) { r.contentInstructions += " changed" }},
		{name: "extraction instructions", mutate: func(r *wikiMapArtifactRequest) { r.extractionInstructions += " changed" }},
		{name: "model revision", mutate: func(r *wikiMapArtifactRequest) { r.modelRevision += " changed" }},
		{name: "prompt suite", mutate: func(r *wikiMapArtifactRequest) { r.promptSuiteVersion += " changed" }},
		{name: "canonicalizer", mutate: func(r *wikiMapArtifactRequest) { r.canonicalizerVersion += " changed" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := testWikiMapArtifactRequest()
			tt.mutate(&changed)
			key, keyErr := newWikiMapArtifactKey(changed)
			require.NoError(t, keyErr)
			assert.NotEqual(t, base, key)
		})
	}
}

func TestWikiMapArtifactRejectsBlankOrMissingSummary(t *testing.T) {
	request := testWikiMapArtifactRequest()

	for _, summary := range []string{" \t\n", "SUMMARY:", "SUMMARY：\n"} {
		t.Run("invalid summary on encode", func(t *testing.T) {
			_, err := encodeWikiMapArtifact(
				wikiMapArtifactValue{SummaryContent: summary}, request.chunks,
			)
			require.Error(t, err)
		})
	}

	t.Run("missing summary on decode", func(t *testing.T) {
		encoded, err := encodeWikiMapArtifact(
			wikiMapArtifactValue{SummaryContent: "SUMMARY: valid"}, request.chunks,
		)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(encoded, &payload))
		delete(payload, "summary_content")
		encoded, err = json.Marshal(payload)
		require.NoError(t, err)

		_, err = decodeWikiMapArtifact(encoded, request.chunks)
		require.Error(t, err)
	})

	t.Run("semantically empty summary on decode", func(t *testing.T) {
		encoded, err := encodeWikiMapArtifact(
			wikiMapArtifactValue{SummaryContent: "SUMMARY: valid"}, request.chunks,
		)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(encoded, &payload))
		payload["summary_content"] = "SUMMARY:"
		encoded, err = json.Marshal(payload)
		require.NoError(t, err)

		_, err = decodeWikiMapArtifact(encoded, request.chunks)
		require.Error(t, err)
	})
}

func TestWikiMapArtifactCodecRemovesAndRebindsChunkOwnership(t *testing.T) {
	oldChunks := testWikiMapChunks("old-chunk-1", "old-chunk-2")
	value := wikiMapArtifactValue{
		Entities: []extractedItem{{
			Name:         "Acme",
			Slug:         "entity/acme",
			Description:  "Company",
			Details:      "Company details",
			SourceChunks: []string{"old-chunk-2"},
		}},
		Concepts: []extractedItem{{
			Name:         "RAG",
			Slug:         "concept/rag",
			Description:  "Method",
			Details:      "Method details",
			SourceChunks: []string{"old-chunk-1", "old-chunk-2"},
		}},
		SummaryContent:  "SUMMARY: document summary",
		CitationBatches: 1,
	}

	payload, err := encodeWikiMapArtifact(value, oldChunks)
	require.NoError(t, err)
	assert.False(t, bytes.Contains(payload, []byte("old-chunk-1")))
	assert.False(t, bytes.Contains(payload, []byte("old-chunk-2")))

	currentChunks := testWikiMapChunks("current-chunk-1", "current-chunk-2")
	got, err := decodeWikiMapArtifact(payload, currentChunks)
	require.NoError(t, err)
	assert.Equal(t, []string{"current-chunk-2"}, got.Entities[0].SourceChunks)
	assert.Equal(t, []string{"current-chunk-1", "current-chunk-2"}, got.Concepts[0].SourceChunks)
	assert.Equal(t, value.SummaryContent, got.SummaryContent)
}

func TestWikiMapArtifactCodecRejectsInvalidOwnership(t *testing.T) {
	chunks := testWikiMapChunks("chunk-1", "chunk-2")
	_, err := encodeWikiMapArtifact(wikiMapArtifactValue{
		Entities: []extractedItem{{
			Name: "Acme", Slug: "entity/acme", Description: "desc", Details: "details",
			SourceChunks: []string{"foreign-chunk"},
		}},
		SummaryContent: "SUMMARY: valid",
	}, chunks)
	require.Error(t, err)

	_, err = encodeWikiMapArtifact(wikiMapArtifactValue{
		Entities: []extractedItem{{
			Name: "Acme", Slug: "entity/acme", Description: "desc", Details: "details",
			SourceChunks: []string{"chunk-1", "chunk-1"},
		}},
		SummaryContent: "SUMMARY: valid",
	}, chunks)
	require.Error(t, err)

	payload, err := encodeWikiMapArtifact(wikiMapArtifactValue{
		Entities: []extractedItem{{
			Name: "Acme", Slug: "entity/acme", Description: "desc", Details: "details",
			SourceChunks: []string{"chunk-2"},
		}},
		SummaryContent: "SUMMARY: valid",
	}, chunks)
	require.NoError(t, err)
	_, err = decodeWikiMapArtifact(payload, chunks[:1])
	require.Error(t, err)

	changedLayout := testWikiMapChunks("current-1", "current-2")
	changedLayout[1].Content = "different content"
	_, err = decodeWikiMapArtifact(payload, changedLayout)
	require.Error(t, err)

	var duplicateOrdinal wikiMapArtifactPayload
	require.NoError(t, json.Unmarshal(payload, &duplicateOrdinal))
	duplicateOrdinal.Entities[0].SourceOrdinals = []int{1, 1}
	encodedDuplicate, err := json.Marshal(duplicateOrdinal)
	require.NoError(t, err)
	_, err = decodeWikiMapArtifact(encodedDuplicate, chunks)
	require.Error(t, err)
}

func TestWikiMapArtifactRejectsAmbiguousChunkLayouts(t *testing.T) {
	duplicateID := testWikiMapArtifactRequest()
	duplicateID.chunks[1].ID = duplicateID.chunks[0].ID
	_, err := newWikiMapArtifactKey(duplicateID)
	require.Error(t, err)

	duplicateDescriptor := testWikiMapArtifactRequest()
	duplicateDescriptor.chunks[1].ChunkIndex = duplicateDescriptor.chunks[0].ChunkIndex
	duplicateDescriptor.chunks[1].StartAt = duplicateDescriptor.chunks[0].StartAt
	duplicateDescriptor.chunks[1].Content = duplicateDescriptor.chunks[0].Content
	duplicateDescriptor.chunks[1].ChunkType = duplicateDescriptor.chunks[0].ChunkType
	_, err = newWikiMapArtifactKey(duplicateDescriptor)
	require.Error(t, err)
}

func TestCanonicalWikiMapChunkOrderStabilizesPositionTies(t *testing.T) {
	alpha := &types.Chunk{
		ID: "chunk-alpha", Content: "alpha", ChunkIndex: 0, StartAt: 0, ChunkType: types.ChunkTypeText,
	}
	beta := &types.Chunk{
		ID: "chunk-beta", Content: "beta", ChunkIndex: 0, StartAt: 0, ChunkType: types.ChunkTypeText,
	}

	first := canonicalWikiMapChunkOrder([]*types.Chunk{beta, alpha})
	second := canonicalWikiMapChunkOrder([]*types.Chunk{alpha, beta})
	require.Len(t, first, 2)
	require.Len(t, second, 2)
	assert.Equal(t, []string{"chunk-alpha", "chunk-beta"}, []string{first[0].ID, first[1].ID})
	assert.Equal(t, []string{"chunk-alpha", "chunk-beta"}, []string{second[0].ID, second[1].ID})
	assert.Equal(t, reconstructContent(first), reconstructContent(second))
}

func TestWikiMapArtifactCodecRejectsSemanticallyInvalidItems(t *testing.T) {
	chunks := testWikiMapChunks("chunk-1", "chunk-2")
	_, err := encodeWikiMapArtifact(wikiMapArtifactValue{
		Entities:       []extractedItem{{Name: " ", Slug: "entity/acme", Description: "desc", Details: "details"}},
		SummaryContent: "SUMMARY: valid",
	}, chunks)
	require.Error(t, err)

	_, err = encodeWikiMapArtifact(wikiMapArtifactValue{
		Entities: []extractedItem{
			{Name: "Acme", Slug: "entity/acme", Description: "desc", Details: "details"},
			{Name: "Other", Slug: "entity/acme", Description: "desc", Details: "details"},
		},
		SummaryContent: "SUMMARY: valid",
	}, chunks)
	require.Error(t, err)

	for _, value := range []wikiMapArtifactValue{
		{
			Entities:       []extractedItem{{Name: "Acme", Slug: "concept/acme", Description: "desc", Details: "details"}},
			SummaryContent: "SUMMARY: valid",
		},
		{
			Concepts:       []extractedItem{{Name: "RAG", Slug: "entity/rag", Description: "desc", Details: "details"}},
			SummaryContent: "SUMMARY: valid",
		},
		{
			Entities:       []extractedItem{{Name: "Acme", Slug: "entity/acme", Details: "details"}},
			SummaryContent: "SUMMARY: valid",
		},
		{
			Entities:       []extractedItem{{Name: "Acme", Slug: "entity/acme", Description: "desc"}},
			SummaryContent: "SUMMARY: valid",
		},
	} {
		_, err = encodeWikiMapArtifact(value, chunks)
		require.Error(t, err)
	}

	valid, err := encodeWikiMapArtifact(wikiMapArtifactValue{
		Entities:       []extractedItem{{Name: "Acme", Slug: "entity/acme", Description: "desc", Details: "details"}},
		SummaryContent: "SUMMARY: valid",
	}, chunks)
	require.NoError(t, err)
	var payload wikiMapArtifactPayload
	require.NoError(t, json.Unmarshal(valid, &payload))
	payload.Entities[0].Name = " "
	invalidItem, err := json.Marshal(payload)
	require.NoError(t, err)
	_, err = decodeWikiMapArtifact(invalidItem, chunks)
	require.Error(t, err)

	require.NoError(t, json.Unmarshal(valid, &payload))
	payload.Entities[0].Slug = "concept/acme"
	invalidItem, err = json.Marshal(payload)
	require.NoError(t, err)
	_, err = decodeWikiMapArtifact(invalidItem, chunks)
	require.Error(t, err)

	require.NoError(t, json.Unmarshal(valid, &payload))
	payload.Entities[0].Description = ""
	invalidItem, err = json.Marshal(payload)
	require.NoError(t, err)
	_, err = decodeWikiMapArtifact(invalidItem, chunks)
	require.Error(t, err)

	invalidUTF8 := append([]byte(nil), valid...)
	invalidUTF8[len(invalidUTF8)-2] = 0xff
	_, err = decodeWikiMapArtifact(invalidUTF8, chunks)
	require.Error(t, err)
}

func TestWikiMapArtifactCodecIsCanonical(t *testing.T) {
	chunks := testWikiMapChunks("chunk-1", "chunk-2")
	first, err := encodeWikiMapArtifact(wikiMapArtifactValue{
		Entities: []extractedItem{
			{Name: "Beta", Slug: "entity/beta", Aliases: []string{"B", "Beta"}, Description: "desc", Details: "details", SourceChunks: []string{"chunk-2", "chunk-1"}},
			{Name: "Acme", Slug: "entity/acme", Description: "desc", Details: "details", SourceChunks: []string{"chunk-1"}},
		},
		SummaryContent: "SUMMARY: valid",
	}, chunks)
	require.NoError(t, err)
	second, err := encodeWikiMapArtifact(wikiMapArtifactValue{
		Entities: []extractedItem{
			{Name: "Acme", Slug: "entity/acme", Description: "desc", Details: "details", SourceChunks: []string{"chunk-1"}},
			{Name: "Beta", Slug: "entity/beta", Aliases: []string{"Beta", "B"}, Description: "desc", Details: "details", SourceChunks: []string{"chunk-1", "chunk-2"}},
		},
		SummaryContent: "SUMMARY: valid",
	}, chunks)
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

func TestWikiMapArtifactCodecPreservesDedupCandidateSlugs(t *testing.T) {
	chunks := testWikiMapChunks("chunk-1")
	value := wikiMapArtifactValue{
		SummaryContent: "SUMMARY: valid",
		DedupContext: wikiMapDedupContext{
			Entities: []extractedItem{{
				Name: "Acme", Slug: "entity/acme", Description: "desc", Details: "details",
			}},
			CandidateSlugs:              []string{"entity/zeta", "entity/alpha"},
			CandidateFingerprint:        "fingerprint",
			ReducedCandidateFingerprint: "reduced-fingerprint",
			OutputPageFingerprints: map[string]string{
				"entity/acme": "page-fingerprint",
			},
		},
	}

	encoded, err := encodeWikiMapArtifact(value, chunks)
	require.NoError(t, err)
	decoded, err := decodeWikiMapArtifact(encoded, chunks)
	require.NoError(t, err)
	assert.Equal(t, []string{"entity/alpha", "entity/zeta"}, decoded.DedupContext.CandidateSlugs)
	assert.Equal(t, "reduced-fingerprint", decoded.DedupContext.ReducedCandidateFingerprint)
	assert.Equal(t, map[string]string{"entity/acme": "page-fingerprint"}, decoded.DedupContext.OutputPageFingerprints)
}

func testWikiMapArtifactRequest() wikiMapArtifactRequest {
	return wikiMapArtifactRequest{
		tenantID:               11,
		knowledgeBaseID:        "kb-1",
		knowledgeID:            "knowledge-1",
		modelID:                "model-1",
		modelName:              "chat-model",
		modelRevision:          "revision-1",
		content:                "canonical document",
		chunks:                 testWikiMapChunks("chunk-1", "chunk-2"),
		language:               "Simplified Chinese",
		granularity:            types.WikiExtractionStandard,
		contentInstructions:    "content instruction",
		extractionInstructions: "extraction instruction",
		promptSuiteVersion:     wikiMapPromptSuiteVersion,
		canonicalizerVersion:   wikiMapCanonicalizerVersion,
	}
}

func testWikiMapChunks(ids ...string) []*types.Chunk {
	chunks := make([]*types.Chunk, 0, len(ids))
	for i, id := range ids {
		chunks = append(chunks, &types.Chunk{
			ID:         id,
			Content:    "content " + id[len(id)-1:],
			ChunkIndex: i,
			StartAt:    i * 10,
			ChunkType:  types.ChunkTypeText,
		})
	}
	return chunks
}
