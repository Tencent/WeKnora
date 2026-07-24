package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestExtractChunkRefs(t *testing.T) {
	msg := &types.Message{
		ID:        "m1",
		SessionID: "s1",
		Role:      "assistant",
		KnowledgeReferences: types.References{
			{ID: "c1", KnowledgeBaseID: "kb1"},
			{ID: "c1", KnowledgeBaseID: "kb1"},                                // duplicate chunk
			{ID: "c2", KnowledgeBaseID: "kb2"},                                // second KB
			{ID: "c3", KnowledgeBaseID: "kb1", ChunkType: "web_search"},       // filtered
			{ID: "c4", KnowledgeBaseID: "kb1", KnowledgeSource: "web_search"}, // filtered
			{ID: "", KnowledgeBaseID: "kb1"},                                  // missing chunk id
			{ID: "c5", KnowledgeBaseID: ""},                                   // missing KB id
			nil,
		},
	}
	refs := extractChunkRefs(msg)
	if len(refs) != 2 {
		t.Fatalf("extractChunkRefs returned %d refs, want 2: %+v", len(refs), refs)
	}
	if refs[0].ChunkID != "c1" || refs[0].KnowledgeBaseID != "kb1" ||
		refs[0].MessageID != "m1" || refs[0].SessionID != "s1" {
		t.Errorf("unexpected first ref: %+v", refs[0])
	}
	if refs[1].ChunkID != "c2" || refs[1].KnowledgeBaseID != "kb2" {
		t.Errorf("unexpected second ref: %+v", refs[1])
	}
}

func TestExtractChunkRefsEmptyReferences(t *testing.T) {
	msg := &types.Message{ID: "m1", Role: "assistant"}
	if refs := extractChunkRefs(msg); len(refs) != 0 {
		t.Errorf("expected no refs for message without references, got %d", len(refs))
	}
}

func TestNormalizeFeedbackReasons(t *testing.T) {
	reasons, err := normalizeFeedbackReasons(types.FeedbackRatingDislike,
		[]string{"inaccurate", "inaccurate", "outdated"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reasons) != 2 || reasons[0] != "inaccurate" || reasons[1] != "outdated" {
		t.Errorf("expected deduplicated reasons, got %v", reasons)
	}

	if _, err := normalizeFeedbackReasons(types.FeedbackRatingDislike, []string{"bogus"}); err == nil {
		t.Error("expected error for unknown reason code")
	}

	// Reasons are dropped for non-dislike ratings.
	reasons, err = normalizeFeedbackReasons(types.FeedbackRatingLike, []string{"inaccurate"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reasons) != 0 {
		t.Errorf("expected reasons dropped for like rating, got %v", reasons)
	}
}
