package tools

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
)

type rerankChatStub struct {
	response *types.ChatResponse
	calls    int
	options  []*chat.ChatOptions
}

func (s *rerankChatStub) Chat(
	_ context.Context,
	_ []chat.Message,
	opts *chat.ChatOptions,
) (*types.ChatResponse, error) {
	s.calls++
	copied := *opts
	s.options = append(s.options, &copied)
	return s.response, nil
}

func (*rerankChatStub) ChatStream(
	context.Context,
	[]chat.Message,
	*chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	stream := make(chan types.StreamResponse)
	close(stream)
	return stream, nil
}

func (*rerankChatStub) GetModelName() string { return "rerank-test" }

func (*rerankChatStub) GetModelID() string { return "rerank-test" }

func TestRerankChatStubStreamIsClosed(t *testing.T) {
	t.Parallel()

	stream, err := (&rerankChatStub{}).ChatStream(
		context.Background(),
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("ChatStream() returned an open stream")
		}
	default:
		t.Fatal("ChatStream() stream is not closed")
	}
}

func TestFilterRerankRankResults_thresholdAndFallback(t *testing.T) {
	t.Parallel()
	rankResults := []rerank.RankResult{
		{Index: 0, RelevanceScore: 0.05},
		{Index: 1, RelevanceScore: 0.02},
	}
	filtered := filterRerankRankResults(rankResults, 0.3, false)
	if len(filtered) != 0 {
		t.Fatalf("expected empty filter, got %#v", filtered)
	}

	rankResults = []rerank.RankResult{
		{Index: 0, RelevanceScore: 0.05},
		{Index: 1, RelevanceScore: 0.20},
	}
	filtered = filterRerankRankResults(rankResults, 0.3, false)
	if len(filtered) != 1 || filtered[0].Index != 1 {
		t.Fatalf("expected fallback top score, got %#v", filtered)
	}

	rankResults = []rerank.RankResult{
		{Index: 0, RelevanceScore: 0.05},
		{Index: 1, RelevanceScore: 0.02},
	}
	filtered = filterRerankRankResults(rankResults, 0.3, true)
	if len(filtered) != 1 || filtered[0].Index != 0 {
		t.Fatalf("expected explicit scope to preserve top result, got %#v", filtered)
	}

	rankResults = []rerank.RankResult{
		{Index: 0, RelevanceScore: 0.8},
		{Index: 1, RelevanceScore: 0.4},
		{Index: 2, RelevanceScore: 0.1},
	}
	filtered = filterRerankRankResults(rankResults, 0.3, false)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 passing scores, got %#v", filtered)
	}
}

func TestApplyModelRerankScores_faqUsesCompositeScale(t *testing.T) {
	t.Parallel()
	tool := &KnowledgeSearchTool{
		config: &config.Config{
			Conversation: &config.ConversationConfig{RerankThreshold: 0.3},
		},
	}
	originals := []*searchResultWithMeta{
		{
			SearchResult:      &types.SearchResult{ID: "faq-1", Content: "Q: WeKnora", Score: 0.011},
			KnowledgeBaseType: types.KnowledgeBaseTypeFAQ,
		},
		{
			SearchResult: &types.SearchResult{ID: "doc-1", Content: "swimming club", Score: 0.02},
		},
	}
	rankResults := []rerank.RankResult{
		{Index: 0, RelevanceScore: 0.05},
		{Index: 1, RelevanceScore: 0.9},
	}
	out := tool.applyModelRerankScores(originals, rankResults, 0.3, false)
	if len(out) != 1 || out[0].ID != "doc-1" {
		t.Fatalf("weak FAQ should be filtered out, got %#v", out)
	}
	if out[0].Score <= 0.011 {
		t.Fatalf("composite score should exceed raw retrieval score, got %.4f", out[0].Score)
	}
}

func TestRerankThreshold_default(t *testing.T) {
	t.Parallel()
	tool := &KnowledgeSearchTool{}
	if got := tool.rerankThreshold(); got != 0.3 {
		t.Fatalf("default threshold = %v, want 0.3", got)
	}
}

func TestRerankWithLLMStopsAfterIncompleteOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response *types.ChatResponse
	}{
		{
			name: "reasoning budget exhausted",
			response: &types.ChatResponse{
				FinishReason: "length",
			},
		},
		{
			name: "score list is incomplete",
			response: &types.ChatResponse{
				Content:      "Passage 1: 0.90",
				FinishReason: "stop",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stub := &rerankChatStub{response: tt.response}
			tool := &KnowledgeSearchTool{chatModel: stub}
			results := make([]*searchResultWithMeta, 16)
			for i := range results {
				results[i] = &searchResultWithMeta{SearchResult: &types.SearchResult{
					ID:      string(rune('a' + i)),
					Content: "passage",
					Score:   0.9,
				}}
			}

			got, err := tool.rerankWithLLM(context.Background(), "query", results)
			if err != nil {
				t.Fatalf("rerankWithLLM returned error: %v", err)
			}
			if stub.calls != 1 {
				t.Fatalf("chat calls = %d, want 1; invalid first batch should skip remaining batches", stub.calls)
			}
			if len(stub.options) != 1 {
				t.Fatalf("captured options = %d, want 1", len(stub.options))
			}
			if stub.options[0].Thinking == nil || *stub.options[0].Thinking {
				t.Fatalf("Thinking = %v, want false", stub.options[0].Thinking)
			}
			if stub.options[0].MaxTokens != 1424 {
				t.Fatalf("MaxTokens = %d, want 1424", stub.options[0].MaxTokens)
			}
			if len(got) != len(results) {
				t.Fatalf("reranked results = %d, want %d original-score fallbacks", len(got), len(results))
			}
		})
	}
}

func TestParseScoresFromResponseRequiresExactCount(t *testing.T) {
	t.Parallel()
	tool := &KnowledgeSearchTool{}

	scores, err := tool.parseScoresFromResponse("Passage 1: 0.90\nPassage 2: 0.40", 2)
	if err != nil {
		t.Fatalf("complete score list returned error: %v", err)
	}
	if len(scores) != 2 || scores[0] != 0.9 || scores[1] != 0.4 {
		t.Fatalf("scores = %#v, want [0.9 0.4]", scores)
	}

	if _, err := tool.parseScoresFromResponse("Passage 1: 0.90", 2); err == nil {
		t.Fatal("incomplete score list should return an error")
	}
}
