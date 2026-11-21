package types

import "testing"

func TestFeedbackPolicyBands(t *testing.T) {
	policy := DefaultFeedbackPolicy()

	none := policy.Evaluate(0, 0)
	if none.PositiveRate != nil || none.RecallWeight != 1 || none.NeedsOptimization {
		t.Fatalf("empty tally = %+v", none)
	}

	early := policy.Evaluate(1, 0)
	if early.PositiveRate == nil || *early.PositiveRate != 1 || early.RecallWeight != 1 || early.NeedsOptimization {
		t.Fatalf("below minimum sample = %+v", early)
	}

	boost := policy.Evaluate(8, 2)
	if boost.RecallWeight != 1.2 || boost.NeedsOptimization || boost.PositiveRate == nil || *boost.PositiveRate != 0.8 {
		t.Fatalf("80%% should boost, got %+v", boost)
	}

	hold := policy.Evaluate(6, 4)
	if hold.RecallWeight != 1 || hold.NeedsOptimization {
		t.Fatalf("60%% should stay neutral, got %+v", hold)
	}

	degrade := policy.Evaluate(4, 6)
	if degrade.RecallWeight != 0.8 || degrade.NeedsOptimization {
		t.Fatalf("40%% should degrade without the optimization flag, got %+v", degrade)
	}

	poor := policy.Evaluate(1, 4)
	if poor.RecallWeight != 0.8 || !poor.NeedsOptimization {
		t.Fatalf("20%% should degrade and need optimization, got %+v", poor)
	}
}

func TestNormalizeFeedbackRequest(t *testing.T) {
	req := &MessageFeedbackRequest{FeedbackType: " dislike ", ReasonCode: "other", ReasonText: "  stale  "}
	if err := NormalizeFeedbackRequest(req); err != nil {
		t.Fatal(err)
	}
	if req.FeedbackType != FeedbackTypeDislike || req.ReasonCode != FeedbackReasonOther || req.ReasonText != "stale" {
		t.Fatalf("normalized = %+v", req)
	}
	if err := NormalizeFeedbackRequest(&MessageFeedbackRequest{FeedbackType: "love"}); !IsInvalidFeedback(err) {
		t.Fatalf("unknown type err = %v", err)
	}
	badReason := &MessageFeedbackRequest{FeedbackType: "dislike", ReasonCode: "nope"}
	if err := NormalizeFeedbackRequest(badReason); !IsInvalidFeedback(err) {
		t.Fatalf("unknown reason err = %v", err)
	}
	like := &MessageFeedbackRequest{FeedbackType: "like", ReasonCode: "other", ReasonText: "x"}
	if err := NormalizeFeedbackRequest(like); err != nil || like.ReasonCode != "" || like.ReasonText != "" {
		t.Fatalf("like should clear reasons: %+v err=%v", like, err)
	}
}

func TestAttributableKnowledgeRefSkipsWebAndHistory(t *testing.T) {
	if AttributableKnowledgeRef(nil) {
		t.Fatal("nil ref")
	}
	if AttributableKnowledgeRef(&SearchResult{ID: "c", KnowledgeID: "k", MatchType: MatchTypeWebSearch}) {
		t.Fatal("web search")
	}
	if AttributableKnowledgeRef(&SearchResult{ID: "c", KnowledgeID: "k", MatchType: MatchTypeHistory}) {
		t.Fatal("history")
	}
	if !AttributableKnowledgeRef(&SearchResult{ID: "c", KnowledgeID: "k", MatchType: MatchTypeEmbedding}) {
		t.Fatal("embedding hit should be attributable")
	}
}
