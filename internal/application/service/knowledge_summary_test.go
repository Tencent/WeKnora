package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

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

func TestValidateSummaryOutput(t *testing.T) {
	tests := []struct {
		name      string
		response  *types.ChatResponse
		want      string
		wantError bool
	}{
		{name: "nil response rejected", response: nil, wantError: true},
		{name: "empty response rejected", response: &types.ChatResponse{}, wantError: true},
		{
			name:      "whitespace response rejected",
			response:  &types.ChatResponse{Content: " \n\t "},
			wantError: true,
		},
		{
			name:     "valid response is trimmed",
			response: &types.ChatResponse{Content: "  useful summary \n"},
			want:     "useful summary",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateSummaryOutput(tt.response)
			if tt.wantError {
				if !errors.Is(err, errEmptySummaryOutput) {
					t.Fatalf("expected errEmptySummaryOutput, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("summary = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFirstTextChunkSummaryFallback(t *testing.T) {
	t.Run("uses only the first chunk", func(t *testing.T) {
		got := firstTextChunkSummaryFallback([]*types.Chunk{
			{Content: "  first chunk  "},
			{Content: "second chunk"},
		})
		if got != "first chunk" {
			t.Fatalf("fallback = %q, want first chunk", got)
		}
	})

	t.Run("does not skip an empty first chunk", func(t *testing.T) {
		got := firstTextChunkSummaryFallback([]*types.Chunk{
			{Content: " \n\t "},
			{Content: "second chunk"},
		})
		if got != "" {
			t.Fatalf("fallback = %q, want empty", got)
		}
	})

	t.Run("caps unicode content by runes", func(t *testing.T) {
		got := firstTextChunkSummaryFallback([]*types.Chunk{{
			Content: strings.Repeat("摘", summaryFallbackMaxRunes+25),
		}})
		if len([]rune(got)) != summaryFallbackMaxRunes {
			t.Fatalf("fallback rune count = %d, want %d", len([]rune(got)), summaryFallbackMaxRunes)
		}
	})

	t.Run("empty input stays empty", func(t *testing.T) {
		if got := firstTextChunkSummaryFallback(nil); got != "" {
			t.Fatalf("fallback = %q, want empty", got)
		}
	})
}

func TestInitialSummaryChunkCarriesAccessMetadataOnlyWhenAllSourceChunksAgree(t *testing.T) {
	sources := []*types.Chunk{
		{
			ID:       "source-chunk-1",
			Metadata: types.JSON(`{"access_metadata":{"department":"research"},"generated_questions":["unrelated"]}`),
		},
		{
			ID:       "source-chunk-2",
			Metadata: types.JSON(`{"generated_questions":["different"],"access_metadata":{"department":"research"}}`),
		},
	}
	knowledge := &types.Knowledge{ID: "knowledge", TenantID: 1, KnowledgeBaseID: "kb"}

	summary, err := newSummaryChunk(sources, knowledge, "summary-id", "# Summary\ninitial", 8)
	if err != nil {
		t.Fatalf("newSummaryChunk() error = %v", err)
	}
	assertSummaryChunkAccessMetadata(t, summary)

	sources[1].Metadata = types.JSON(`{"access_metadata":{"department":"finance"}}`)
	summary, err = newSummaryChunk(sources, knowledge, "heterogeneous-summary", "# Summary\ninitial", 8)
	if err != nil {
		t.Fatalf("newSummaryChunk() with heterogeneous metadata error = %v", err)
	}
	accessMetadata, err := summary.AccessMetadata()
	if err != nil {
		t.Fatalf("heterogeneous summary AccessMetadata() error = %v", err)
	}
	if len(accessMetadata) != 0 || len(summary.Metadata) != 0 {
		t.Fatalf("heterogeneous summary metadata = %s, want empty", summary.Metadata)
	}
}

func TestRefreshSummaryChunkSynchronizesAndClearsAccessMetadata(t *testing.T) {
	summary := &types.Chunk{
		ID:       "summary-id",
		Metadata: types.JSON(`{"access_metadata":{"department":"stale"}}`),
	}
	sources := []*types.Chunk{
		{ID: "source-1", Metadata: types.JSON(`{"access_metadata":{"department":"research"}}`)},
		{ID: "source-2", Metadata: types.JSON(`{"access_metadata":{"department":"research"}}`)},
	}
	if err := applySummaryChunkAccessMetadata(summary, sources); err != nil {
		t.Fatalf("apply homogeneous refresh metadata: %v", err)
	}
	assertSummaryChunkAccessMetadata(t, summary)

	sources[1].Metadata = types.JSON(`{"access_metadata":{"department":"finance"}}`)
	if err := applySummaryChunkAccessMetadata(summary, sources); err != nil {
		t.Fatalf("apply heterogeneous refresh metadata: %v", err)
	}
	accessMetadata, err := summary.AccessMetadata()
	if err != nil {
		t.Fatalf("cleared summary AccessMetadata() error = %v", err)
	}
	if len(accessMetadata) != 0 || len(summary.Metadata) != 0 {
		t.Fatalf("refreshed heterogeneous summary metadata = %s, want empty", summary.Metadata)
	}
}

func assertSummaryChunkAccessMetadata(t *testing.T, summary *types.Chunk) {
	t.Helper()
	accessMetadata, err := summary.AccessMetadata()
	if err != nil {
		t.Fatalf("summary AccessMetadata() error = %v", err)
	}
	if accessMetadata["department"] != "research" {
		t.Fatalf("summary access metadata = %#v, want department=research", accessMetadata)
	}
	persisted, err := summary.Metadata.Map()
	if err != nil {
		t.Fatalf("summary metadata map: %v", err)
	}
	if len(persisted) != 1 {
		t.Fatalf("summary metadata = %#v, want only access_metadata", persisted)
	}
	if _, ok := persisted["generated_questions"]; ok {
		t.Fatalf("summary metadata leaked unrelated source metadata: %#v", persisted)
	}
}

func TestApplyRetryableSummaryFailureState(t *testing.T) {
	chunks := []*types.Chunk{{Content: "first body chunk"}}

	t.Run("retry keeps existing description", func(t *testing.T) {
		knowledge := &types.Knowledge{
			Description:   "previous summary",
			SummaryStatus: types.SummaryStatusProcessing,
		}
		fallback := applyRetryableSummaryFailureState(knowledge, chunks, true)
		if fallback != "" {
			t.Fatalf("retry fallback = %q, want empty", fallback)
		}
		if knowledge.Description != "previous summary" {
			t.Fatalf("retry changed description to %q", knowledge.Description)
		}
		if knowledge.SummaryStatus != types.SummaryStatusPending {
			t.Fatalf("retry status = %q, want pending", knowledge.SummaryStatus)
		}
	})

	t.Run("terminal failure publishes fallback and fails summary", func(t *testing.T) {
		knowledge := &types.Knowledge{
			Description:   "previous summary",
			SummaryStatus: types.SummaryStatusProcessing,
		}
		fallback := applyRetryableSummaryFailureState(knowledge, chunks, false)
		if fallback != "first body chunk" {
			t.Fatalf("terminal fallback = %q", fallback)
		}
		if knowledge.Description != fallback {
			t.Fatalf("description = %q, want %q", knowledge.Description, fallback)
		}
		if knowledge.SummaryStatus != types.SummaryStatusFailed {
			t.Fatalf("terminal status = %q, want failed", knowledge.SummaryStatus)
		}
	})
}

func TestSummaryRetryStateSupportsLiteExecutorContext(t *testing.T) {
	retryCtx := types.WithTaskRetryMetadata(context.Background(), 1, 3)
	if !summaryTaskWillRetry(retryCtx) {
		t.Fatal("attempt 1 of maxRetry 3 should have another retry")
	}
	if isFinalAsynqAttempt(retryCtx) {
		t.Fatal("attempt 1 of maxRetry 3 should not be final")
	}

	finalCtx := types.WithTaskRetryMetadata(context.Background(), 3, 3)
	if summaryTaskWillRetry(finalCtx) {
		t.Fatal("attempt 3 of maxRetry 3 should not retry")
	}
	if !isFinalAsynqAttempt(finalCtx) {
		t.Fatal("attempt 3 of maxRetry 3 should be final")
	}
}
