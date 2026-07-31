package chatpipeline

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRerankFallbackMinScoreForExplicitScope(t *testing.T) {
	if got := rerankFallbackMinScore(nil); got != 0.15 {
		t.Fatalf("default fallback minimum = %v, want 0.15", got)
	}

	targets := types.SearchTargets{{DisableRecallThresholds: true}}
	if got := rerankFallbackMinScore(targets); got != 0 {
		t.Fatalf("explicit-scope fallback minimum = %v, want 0", got)
	}
}

func TestPreparedRerankTraceOutputContainsOnlyCountsAndHash(t *testing.T) {
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			ExecutionScopeHash: "1234567890abcdef",
		},
		PipelineState: types.PipelineState{
			SearchResult: []*types.SearchResult{
				{
					ID:              "chunk-private",
					KnowledgeID:     "knowledge-private",
					KnowledgeBaseID: "kb-private",
					Content:         "content-private",
				},
			},
			RerankResult: []*types.SearchResult{
				{ID: "reranked-private"},
			},
		},
	}
	out := preparedRerankTraceOutput(chatManage, 1)
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal prepared rerank summary: %v", err)
	}
	payload := string(encoded)
	for _, secret := range []string{
		"chunk-private",
		"knowledge-private",
		"kb-private",
		"content-private",
		"reranked-private",
	} {
		if strings.Contains(payload, secret) {
			t.Fatalf("prepared rerank trace leaked %q: %s", secret, payload)
		}
	}
	if !strings.Contains(payload, "1234567890ab") {
		t.Fatalf("prepared rerank trace omitted scope hash prefix: %s", payload)
	}
}
