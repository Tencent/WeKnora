package types

import "testing"

func TestDecideChunkWeightBands(t *testing.T) {
	cfg := DefaultChunkFeedbackConfig()
	cases := []struct {
		name     string
		likes    int
		dislikes int
		weight   float64
		needs    bool
	}{
		{name: "boost", likes: 4, dislikes: 1, weight: 1.2, needs: false},
		{name: "exact boost boundary", likes: 8, dislikes: 2, weight: 1.2, needs: false},
		{name: "neutral", likes: 1, dislikes: 1, weight: 1, needs: false},
		{name: "exact neutral boundary", likes: 1, dislikes: 1, weight: 1, needs: false},
		{name: "degrade", likes: 1, dislikes: 2, weight: 0.8, needs: false},
		{name: "needs optimization", likes: 1, dislikes: 4, weight: 0.8, needs: true},
		{name: "no votes", likes: 0, dislikes: 0, weight: 1, needs: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecideChunkWeight(cfg, tc.likes, tc.dislikes, 1)
			if got.Weight != tc.weight {
				t.Fatalf("weight = %v, want %v", got.Weight, tc.weight)
			}
			if got.NeedsOptimization != tc.needs {
				t.Fatalf("needs optimization = %v, want %v", got.NeedsOptimization, tc.needs)
			}
			if tc.likes+tc.dislikes == 0 && got.ApprovalRate != nil {
				t.Fatal("approval rate must be empty without votes")
			}
		})
	}

	held := cfg
	held.MinSamples = 3
	got := DecideChunkWeight(held, 0, 1, 1.2)
	if got.Weight != 1 || got.NeedsOptimization {
		t.Fatalf("below min samples should return to neutral, got %+v", got)
	}

	disabled := cfg
	disabled.Enabled = false
	got = DecideChunkWeight(disabled, 0, 5, 1.2)
	if got.Weight != 1.2 || !got.NeedsOptimization {
		t.Fatalf("disabled policy should keep weight and still flag, got %+v", got)
	}
	if got.Changed {
		t.Fatal("disabled policy should not report a weight change")
	}
}

func TestFeedbackCountDelta(t *testing.T) {
	like, dislike := FeedbackCountDelta("", AnswerFeedbackLike)
	if like != 1 || dislike != 0 {
		t.Fatalf("like delta = %d,%d", like, dislike)
	}
	like, dislike = FeedbackCountDelta(AnswerFeedbackLike, AnswerFeedbackDislike)
	if like != -1 || dislike != 1 {
		t.Fatalf("switch delta = %d,%d", like, dislike)
	}
	like, dislike = FeedbackCountDelta(AnswerFeedbackDislike, AnswerFeedbackNone)
	if like != 0 || dislike != -1 {
		t.Fatalf("cancel delta = %d,%d", like, dislike)
	}
}

func TestAnswerChunkRefsFromMessageSkipsContext(t *testing.T) {
	msg := &Message{
		KnowledgeReferences: References{
			{ID: "cited", KnowledgeID: "doc", KnowledgeBaseID: "kb", MatchType: MatchTypeKeywords},
			{ID: "nearby", KnowledgeID: "doc", KnowledgeBaseID: "kb", MatchType: MatchTypeNearByChunk},
			{ID: "web", ChunkType: ChunkTypeWebSearch, MatchType: MatchTypeWebSearch},
		},
		AgentSteps: AgentSteps{{
			ToolCalls: []ToolCall{{
				Result: &ToolResult{Data: map[string]interface{}{
					"display_type": "search_results",
					"results": []interface{}{
						map[string]interface{}{"chunk_id": "cited", "knowledge_id": "doc", "knowledge_base_id": "kb"},
						map[string]interface{}{"faq_id": "faq-1", "knowledge_id": "faq-doc", "knowledge_base_id": "kb"},
					},
				}},
			}},
		}},
	}
	refs := AnswerChunkRefsFromMessage(msg)
	got := map[string]AnswerChunkRef{}
	for _, ref := range refs {
		got[ref.ChunkID] = ref
	}
	if _, ok := got["nearby"]; ok {
		t.Fatal("nearby context chunk must not be attributed")
	}
	if _, ok := got["web"]; ok {
		t.Fatal("web result must not be attributed")
	}
	if got["cited"].KnowledgeID != "doc" || got["faq-1"].KnowledgeBaseID != "kb" {
		t.Fatalf("refs = %+v", got)
	}
}

func TestChunkFeedbackConfigValidate(t *testing.T) {
	if err := DefaultChunkFeedbackConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := DefaultChunkFeedbackConfig()
	bad.DegradeThreshold = 0.9
	if err := bad.Validate(); err == nil {
		t.Fatal("expected degrade threshold above boost to fail")
	}
}
