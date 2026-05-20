package service

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestFinalizeIndexedKnowledgeState(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name                 string
		hasPendingMultimodal bool
		textChunkCount       int
		wantParseStatus      string
		wantSummaryStatus    string
	}{
		{
			name:              "text document is completed once chunks are indexed",
			textChunkCount:    2,
			wantParseStatus:   types.ParseStatusCompleted,
			wantSummaryStatus: types.SummaryStatusPending,
		},
		{
			name:              "empty indexed document is completed without summary work",
			textChunkCount:    0,
			wantParseStatus:   types.ParseStatusCompleted,
			wantSummaryStatus: types.SummaryStatusNone,
		},
		{
			name:                 "document waits while multimodal image work is pending",
			hasPendingMultimodal: true,
			textChunkCount:       2,
			wantParseStatus:      types.ParseStatusProcessing,
			wantSummaryStatus:    types.SummaryStatusNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			knowledge := &types.Knowledge{
				ParseStatus:   types.ParseStatusProcessing,
				SummaryStatus: types.SummaryStatusCompleted,
			}

			finalizeIndexedKnowledgeState(knowledge, 4096, tt.textChunkCount, tt.hasPendingMultimodal, now)

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
