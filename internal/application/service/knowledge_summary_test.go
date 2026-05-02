package service

import (
	"context"
	"errors"
	"testing"
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
			name: "scanned PDF with empty <image> wrapper rejected",
			content: `<image url="x"><image_original>![a](x)</image_original></image>`,
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
