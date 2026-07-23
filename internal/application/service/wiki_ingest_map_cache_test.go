package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wikiMapCacheChatModel struct {
	modelID   string
	modelName string
}

func (m wikiMapCacheChatModel) Chat(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (*types.ChatResponse, error) {
	return &types.ChatResponse{Content: ""}, nil
}

func (m wikiMapCacheChatModel) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (m wikiMapCacheChatModel) GetModelName() string { return m.modelName }
func (m wikiMapCacheChatModel) GetModelID() string   { return m.modelID }

func TestWikiMapCacheKeyInvalidatesOnInputs(t *testing.T) {
	baseCtx := &WikiBatchContext{
		ExtractionGranularity:  types.WikiExtractionStandard,
		ContentInstructions:    "content instructions",
		ExtractionInstructions: "extract instructions",
	}
	model := wikiMapCacheChatModel{modelID: "model-a", modelName: "name-a"}
	base := wikiMapCacheKey("knowledge-a", "document content", model, "English", baseCtx)

	assert.Less(t, len(base), 255)
	assert.Equal(t, base, wikiMapCacheKey("knowledge-a", " document content ", model, "English", baseCtx))
	assert.NotEqual(t, base, wikiMapCacheKey("knowledge-a", "changed content", model, "English", baseCtx))
	assert.NotEqual(t, base, wikiMapCacheKey("knowledge-a", "document content", model, "Chinese", baseCtx))
	assert.NotEqual(t, base, wikiMapCacheKey("knowledge-a", "document content", model, "English", &WikiBatchContext{
		ExtractionGranularity:  types.WikiExtractionFocused,
		ContentInstructions:    "content instructions",
		ExtractionInstructions: "extract instructions",
	}))
	assert.NotEqual(t, base, wikiMapCacheKey("knowledge-a", "document content", model, "English", &WikiBatchContext{
		ExtractionGranularity:  types.WikiExtractionStandard,
		ContentInstructions:    "changed content instructions",
		ExtractionInstructions: "extract instructions",
	}))
	assert.NotEqual(t, base, wikiMapCacheKey("knowledge-a", "document content", model, "English", &WikiBatchContext{
		ExtractionGranularity:  types.WikiExtractionStandard,
		ContentInstructions:    "content instructions",
		ExtractionInstructions: "changed extract instructions",
	}))
	assert.NotEqual(t, base, wikiMapCacheKey("knowledge-a", "document content",
		wikiMapCacheChatModel{modelID: "model-b", modelName: "name-a"}, "English", baseCtx))
	assert.NotEqual(t, base, wikiMapCacheKey("knowledge-a", "document content",
		wikiMapCacheChatModel{modelName: "name-a"}, "English", baseCtx))
}

func TestWikiMapCachePayloadRoundTripAndRebuildUsesCurrentState(t *testing.T) {
	payload := &wikiDocumentMapCachePayload{
		KnowledgeID:    "knowledge-a",
		SummaryContent: "SUMMARY: Acme overview\n\nAcme builds search tools.",
		Entities: []extractedItem{{
			Name:         "Acme",
			Slug:         "entity/acme",
			Description:  "A company.",
			SourceChunks: []string{"chunk-1"},
		}},
		Concepts: []extractedItem{{
			Name: "Search",
			Slug: "concept/search",
		}},
		Uncited:            1,
		NewSlugCount:       1,
		ClassifyBatchCount: 2,
	}
	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var restored wikiDocumentMapCachePayload
	require.NoError(t, json.Unmarshal(data, &restored))

	batchCtx := &WikiBatchContext{
		SummaryContentByKnowledgeID: func(context.Context, string) string {
			return "previous summary contribution"
		},
	}
	result, updates := restored.buildResultAndUpdates(
		context.Background(),
		batchCtx,
		"knowledge-a",
		"Current Title",
		"knowledge-a",
		"current document content",
		map[string]bool{
			"entity/acme":      true,
			"summary/old":      true,
			"concept/obsolete": true,
		},
		nil,
		true,
		3,
		"English",
	)

	require.NotNil(t, result)
	assert.Equal(t, "Current Title", result.DocTitle)
	assert.Equal(t, "Acme overview", result.Summary)
	assert.True(t, result.MapStats["cache_hit"].(bool))
	assert.Equal(t, 3, result.MapStats["chunks"])
	assert.Equal(t, 1, result.MapStats["reparse_slugs"])
	assert.Equal(t, 2, result.MapStats["stale_slugs"])

	var foundRetract, foundStale bool
	for _, update := range updates {
		if update.Type == "retract" && update.Slug == "entity/acme" {
			foundRetract = true
			assert.Equal(t, "previous summary contribution", update.RetractDocContent)
		}
		if update.Type == "retractStale" && update.Slug == "concept/obsolete" {
			foundStale = true
			assert.Equal(t, "current document content", update.RetractDocContent)
		}
	}
	assert.True(t, foundRetract)
	assert.True(t, foundStale)
}

func TestWikiMapCacheGetSetAndFallback(t *testing.T) {
	ctx := context.Background()
	repo := newFakeContentCacheRepo()
	svc := &wikiIngestService{contentCacheRepo: repo}
	cacheKey := "wiki_map:key"
	payload := &wikiDocumentMapCachePayload{
		KnowledgeID:    "knowledge-a",
		SummaryContent: "SUMMARY: Cached\n\nBody",
	}

	svc.setCachedWikiDocumentMap(ctx, 1, cacheKey, payload)
	got, hit := svc.getCachedWikiDocumentMap(ctx, 1, cacheKey)
	require.True(t, hit)
	require.NotNil(t, got)
	assert.Equal(t, payload.SummaryContent, got.SummaryContent)

	repo.getErr = errors.New("read failed")
	got, hit = svc.getCachedWikiDocumentMap(ctx, 1, cacheKey)
	assert.False(t, hit)
	assert.Nil(t, got)

	repo.getErr = nil
	repo.upsertErr = errors.New("write failed")
	svc.setCachedWikiDocumentMap(ctx, 1, "wiki_map:other", payload)
}
