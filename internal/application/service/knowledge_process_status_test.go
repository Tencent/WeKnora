package service

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRequiresScannedPDFOCR(t *testing.T) {
	tests := []struct {
		name             string
		result           *types.ReadResult
		enableMultimodal bool
		want             bool
	}{
		{
			name: "image-only scanned PDF without OCR is rejected before chunking",
			result: &types.ReadResult{
				MarkdownContent: "![page_1](images/page_1.jpg)",
				Metadata:        map[string]string{"image_source_type": "scanned_pdf"},
			},
			want: true,
		},
		{
			name: "scanned PDF with native fallback text remains usable",
			result: &types.ReadResult{
				MarkdownContent: "这是一段可供检索的 PDF 文本。",
				Metadata:        map[string]string{"image_source_type": "scanned_pdf"},
			},
			want: false,
		},
		{
			name: "scanned PDF with OCR enabled continues to multimodal processing",
			result: &types.ReadResult{
				MarkdownContent: "![page_1](images/page_1.jpg)",
				Metadata:        map[string]string{"image_source_type": "scanned_pdf"},
			},
			enableMultimodal: true,
			want:             false,
		},
		{
			name: "image-only non-scanned document is outside PDF guard",
			result: &types.ReadResult{
				MarkdownContent: "![figure](images/figure.jpg)",
				Metadata:        map[string]string{"image_source_type": "pdf_text_layer"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requiresScannedPDFOCR(tt.result, tt.enableMultimodal); got != tt.want {
				t.Fatalf("requiresScannedPDFOCR() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldPreferPDFTextLayer(t *testing.T) {
	tests := []struct {
		name             string
		fileType         string
		parserEngine     string
		enableMultimodal bool
		overrides        map[string]string
		want             bool
	}{
		{name: "builtin PDF without OCR uses conservative fallback", fileType: "pdf", want: true},
		{name: "multimodal PDF keeps OCR-first route", fileType: "pdf", enableMultimodal: true, want: false},
		{name: "custom parser is not overridden", fileType: "pdf", parserEngine: "mineru", want: false},
		{name: "non PDF is not overridden", fileType: "docx", want: false},
		{name: "explicit preference wins", fileType: "pdf", overrides: map[string]string{"pdf_prefer_text_layer": "false"}, want: false},
		{name: "forced scanned mode wins", fileType: "pdf", overrides: map[string]string{"pdf_force_scanned": "true"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldPreferPDFTextLayer(tt.fileType, tt.parserEngine, tt.enableMultimodal, tt.overrides); got != tt.want {
				t.Fatalf("shouldPreferPDFTextLayer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFinalizeIndexedKnowledgeState(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name                 string
		hasPendingMultimodal bool
		textChunkCount       int
		wantParseStatus      string
		wantSummaryStatus    string
		wantErrorMessage     string
	}{
		{
			name:              "text document stays processing so post-process can fan out enrichment",
			textChunkCount:    2,
			wantParseStatus:   types.ParseStatusProcessing,
			wantSummaryStatus: types.SummaryStatusNone,
			// Still processing: FinalizeSubtask clears the column when this
			// row is eventually promoted to completed.
			wantErrorMessage: "previous attempt failed",
		},
		{
			name:              "empty indexed document is completed without summary work",
			textChunkCount:    0,
			wantParseStatus:   types.ParseStatusCompleted,
			wantSummaryStatus: types.SummaryStatusNone,
			wantErrorMessage:  "",
		},
		{
			name:                 "document waits while multimodal image work is pending",
			hasPendingMultimodal: true,
			textChunkCount:       2,
			wantParseStatus:      types.ParseStatusProcessing,
			wantSummaryStatus:    types.SummaryStatusNone,
			wantErrorMessage:     "previous attempt failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			knowledge := &types.Knowledge{
				ParseStatus:   types.ParseStatusProcessing,
				SummaryStatus: types.SummaryStatusCompleted,
				ErrorMessage:  "previous attempt failed",
			}

			finalizeIndexedKnowledgeState(knowledge, 4096, tt.textChunkCount, tt.hasPendingMultimodal, now)

			if knowledge.ErrorMessage != tt.wantErrorMessage {
				t.Fatalf("ErrorMessage = %q, want %q", knowledge.ErrorMessage, tt.wantErrorMessage)
			}

			if knowledge.ParseStatus != tt.wantParseStatus {
				t.Fatalf("ParseStatus = %q, want %q", knowledge.ParseStatus, tt.wantParseStatus)
			}
			if knowledge.SummaryStatus != tt.wantSummaryStatus {
				t.Fatalf("SummaryStatus = %q, want %q", knowledge.SummaryStatus, tt.wantSummaryStatus)
			}
			if knowledge.EnableStatus != "enabled" {
				t.Fatalf("EnableStatus = %q, want enabled", knowledge.EnableStatus)
			}
			if knowledge.StorageSize != 4096 {
				t.Fatalf("StorageSize = %d, want 4096", knowledge.StorageSize)
			}
			if knowledge.ProcessedAt == nil || !knowledge.ProcessedAt.Equal(now) {
				t.Fatalf("ProcessedAt = %v, want %v", knowledge.ProcessedAt, now)
			}
			if !knowledge.UpdatedAt.Equal(now) {
				t.Fatalf("UpdatedAt = %v, want %v", knowledge.UpdatedAt, now)
			}
		})
	}
}

// TestMarkKnowledgeProcessingClearsPreviousAttemptError covers the transition
// every worker performs before it starts a new attempt. A row that failed
// earlier still carries that attempt's error_message, and leaving it in place
// makes the UI report a failure on a document it is simultaneously showing as
// processing.
func TestMarkKnowledgeProcessingClearsPreviousAttemptError(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	knowledge := &types.Knowledge{
		ParseStatus:  types.ParseStatusFailed,
		ErrorMessage: "Task interrupted due to application restart",
	}

	markKnowledgeProcessing(knowledge, now)

	if knowledge.ParseStatus != types.ParseStatusProcessing {
		t.Fatalf("ParseStatus = %q, want %q", knowledge.ParseStatus, types.ParseStatusProcessing)
	}
	if knowledge.ErrorMessage != "" {
		t.Fatalf("ErrorMessage = %q, want empty", knowledge.ErrorMessage)
	}
	if !knowledge.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v, want %v", knowledge.UpdatedAt, now)
	}
}
