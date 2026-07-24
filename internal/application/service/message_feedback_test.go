package service

import (
	"context"
	"testing"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// TestRequireOwnedKBRejectsCrossTenant pins F2: feedback stats/logs/reset are
// owner-only. A caller whose active tenant does not own the KB is denied even
// though the KBAccess guards would let shared viewers/editors of a
// cross-tenant KB through; a system admin bypasses the check.
func TestRequireOwnedKBRejectsCrossTenant(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb1", TenantID: 1}
	svc := &messageFeedbackService{
		kbRepo: &fakeKBRepo{rows: map[string]*types.KnowledgeBase{"kb1": kb}},
	}

	// Owner tenant: allowed.
	if _, err := svc.requireOwnedKB(ctxWithTenant(1), "kb1"); err != nil {
		t.Fatalf("owner tenant must be allowed, got %v", err)
	}

	// Shared/other tenant: denied with NotFound (no existence disclosure).
	_, err := svc.requireOwnedKB(ctxWithTenant(2), "kb1")
	if err == nil {
		t.Fatal("cross-tenant caller must be denied")
	}
	if appErr, ok := werrors.IsAppError(err); !ok || appErr.Code != werrors.ErrNotFound {
		t.Errorf("expected NotFound, got %v", err)
	}

	// System admin: bypasses ownership.
	adminCtx := context.WithValue(ctxWithTenant(2), types.SystemAdminContextKey, true)
	if _, err := svc.requireOwnedKB(adminCtx, "kb1"); err != nil {
		t.Errorf("system admin must bypass ownership, got %v", err)
	}
}

func TestExtractChunkRefs(t *testing.T) {
	msg := &types.Message{
		ID:        "m1",
		SessionID: "s1",
		Role:      "assistant",
		KnowledgeReferences: types.References{
			{ID: "c1", KnowledgeBaseID: "kb1", SubChunkID: []string{"c1", "c1a", "c1b"}}, // merged passage
			{ID: "c1", KnowledgeBaseID: "kb1"},                                           // duplicate head chunk
			{ID: "c2", KnowledgeBaseID: "kb2"},                                           // second KB
			{ID: "c3", KnowledgeBaseID: "kb1", ChunkType: "web_search"},                  // filtered
			{ID: "c4", KnowledgeBaseID: "kb1", KnowledgeSource: "web_search"},            // filtered
			{ID: "", KnowledgeBaseID: "kb1", SubChunkID: []string{"c5"}},                 // no head id, sub still counts
			{ID: "c6", KnowledgeBaseID: ""},                                              // missing KB id
			nil,
		},
	}
	refs := extractChunkRefs(msg)
	got := make(map[string]string, len(refs))
	for _, ref := range refs {
		if ref.MessageID != "m1" || ref.SessionID != "s1" {
			t.Errorf("unexpected message/session on ref: %+v", ref)
		}
		if _, dup := got[ref.ChunkID]; dup {
			t.Errorf("duplicate chunk id in refs: %s", ref.ChunkID)
		}
		got[ref.ChunkID] = ref.KnowledgeBaseID
	}
	// c1 + merged subchunks c1a/c1b (all kb1), c2 (kb2), c5 (kb1 via subchunk).
	want := map[string]string{
		"c1": "kb1", "c1a": "kb1", "c1b": "kb1", "c2": "kb2", "c5": "kb1",
	}
	if len(got) != len(want) {
		t.Fatalf("extractChunkRefs = %v, want keys %v", got, want)
	}
	for id, kb := range want {
		if got[id] != kb {
			t.Errorf("chunk %s: kb = %q, want %q", id, got[id], kb)
		}
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
