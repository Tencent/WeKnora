package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type summaryArtifactChunkRepo struct{ interfaces.ChunkRepository }

func (summaryArtifactChunkRepo) ListChunksByParentIDs(context.Context, uint64, []string) ([]*types.Chunk, error) {
	return nil, nil
}

// TestCheckSufficientSummaryContent verifies the gate that prevents getSummary
// from calling the LLM (and ProcessSummaryGeneration from creating a summary
// chunk) when the document has no usable text. This is the entry point for
// the errInsufficientSummaryContent → SummaryStatusFailed flow that the
// caller in ProcessSummaryGeneration relies on.
func TestCheckSufficientSummaryContent(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		content   string
		wantError bool
	}{
		{
			name:      "empty content rejected",
			content:   "",
			wantError: true,
		},
		{
			name:      "only whitespace rejected",
			content:   "   \n\n\t  ",
			wantError: true,
		},
		{
			name:      "below threshold rejected",
			content:   "hi",
			wantError: true,
		},
		{
			name: "scanned PDF with no OCR (image-only) rejected",
			content: "![MX5280_page_1.png](images/MX5280_page_1.png)\n" +
				"![MX5280_page_2.png](images/MX5280_page_2.png)",
			wantError: true,
		},
		{
			name:      "scanned PDF with empty <image> wrapper rejected",
			content:   `<image url="x"><image_original>![a](x)</image_original></image>`,
			wantError: true,
		},
		{
			name:      "short legitimate note above threshold accepted",
			content:   "Meeting at 3pm tomorrow.",
			wantError: false,
		},
		{
			name: "scanned PDF with successful VLM OCR accepted",
			content: `<image url="images/p1.png">
<image_original>![p1](images/p1.png)</image_original>
<image_caption>scanned letter</image_caption>
<image_ocr>Sehr geehrter Herr Mustermann, in der Sache 4711/2024 ...</image_ocr>
</image>`,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkSufficientSummaryContent(ctx, "test-knowledge-id", tt.content)
			if tt.wantError {
				if err == nil {
					t.Errorf("expected errInsufficientSummaryContent, got nil")
					return
				}
				if !errors.Is(err, errInsufficientSummaryContent) {
					t.Errorf("expected errInsufficientSummaryContent sentinel, got %v", err)
				}
			} else {
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}
			}
		})
	}
}

// TestCheckSufficientSummaryContent_ThresholdOverride verifies that
// minTextContentRunes is a `var` (not const) so tests and future runtime
// configuration can adjust the threshold without a rebuild.
func TestCheckSufficientSummaryContent_ThresholdOverride(t *testing.T) {
	ctx := context.Background()
	content := "Meeting at 3pm." // 15 runes

	originalThreshold := minTextContentRunes
	t.Cleanup(func() { minTextContentRunes = originalThreshold })

	// With default threshold (10), this content passes.
	if err := checkSufficientSummaryContent(ctx, "kid", content); err != nil {
		t.Fatalf("default threshold: expected pass, got %v", err)
	}

	// With a tighter threshold (50), the same content is rejected.
	minTextContentRunes = 50
	err := checkSufficientSummaryContent(ctx, "kid", content)
	if !errors.Is(err, errInsufficientSummaryContent) {
		t.Fatalf("tightened threshold: expected errInsufficientSummaryContent, got %v", err)
	}
}

func TestGetSummaryReusesSuccessfulChatArtifact(t *testing.T) {
	store := newChatArtifactFakeStore()
	model := &chatArtifactFakeModel{
		modelID:   "summary-model",
		modelName: "summary-model",
		response:  &types.ChatResponse{Content: "cached summary"},
	}
	svc := &knowledgeService{
		config: &config.Config{Conversation: &config.ConversationConfig{
			GenerateSummaryPrompt: "Summarize in {{language}}.",
		}},
		chunkRepo:     summaryArtifactChunkRepo{},
		artifactStore: store,
	}
	ctx := context.WithValue(context.Background(), types.LanguageContextKey, "en")
	knowledge := &types.Knowledge{ID: "knowledge-1", TenantID: 7}
	chunks := []*types.Chunk{{ID: "chunk-1", Content: "This document contains enough useful text.", StartAt: 0}}

	first, firstHit, err := svc.getSummary(ctx, model, "revision-1", knowledge, chunks)
	require.NoError(t, err)
	second, secondHit, err := svc.getSummary(ctx, model, "revision-1", knowledge, chunks)
	require.NoError(t, err)

	assert.Equal(t, "cached summary", first)
	assert.Equal(t, first, second)
	assert.False(t, firstHit)
	assert.True(t, secondHit)
	assert.Equal(t, 1, model.calls)
	assert.Equal(t, 1, store.putCalls)
}

func TestGetSummaryBypassesArtifactWithoutSafeModelRevision(t *testing.T) {
	store := newChatArtifactFakeStore()
	model := &chatArtifactFakeModel{
		modelID:   "summary-model",
		modelName: "summary-model",
		response:  &types.ChatResponse{Content: "fresh summary"},
	}
	svc := &knowledgeService{
		config: &config.Config{Conversation: &config.ConversationConfig{
			GenerateSummaryPrompt: "Summarize.",
		}},
		chunkRepo:     summaryArtifactChunkRepo{},
		artifactStore: store,
	}
	knowledge := &types.Knowledge{ID: "knowledge-1", TenantID: 7}
	chunks := []*types.Chunk{{ID: "chunk-1", Content: "This document contains enough useful text.", StartAt: 0}}

	_, hit, err := svc.getSummary(context.Background(), model, "", knowledge, chunks)
	require.NoError(t, err)
	assert.False(t, hit)
	assert.Equal(t, 1, model.calls)
	assert.Zero(t, store.getCalls)
	assert.Zero(t, store.putCalls)
}

func TestStableSummaryChunkIDIgnoresCompletionVariance(t *testing.T) {
	first := stableSummaryChunkID("knowledge-1")
	second := stableSummaryChunkID("knowledge-1")
	assert.Equal(t, first, second)
	assert.NotEmpty(t, first)
	assert.NotEqual(t, first, stableSummaryChunkID("knowledge-2"))
}
