package chatpipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// The direct-answer threshold is compared against the score before boosts,
// and the FAQ that cleared it is the one marked exact — not whichever FAQ
// happens to rank first after boosting.
func TestIntoChatMessage_ExactFAQUsesPreBoostScore(t *testing.T) {
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query: "refund policy",
			SummaryConfig: types.SummaryConfig{
				ContextTemplate: "{{contexts}}",
			},
		},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{
					ID: "boosted", Content: "boosted but middling", ChunkType: string(types.ChunkTypeFAQ), Score: 1.02,
					Metadata: map[string]string{"composite_score": "0.8500"},
				},
				{
					ID: "strong", Content: "strong match", ChunkType: string(types.ChunkTypeFAQ), Score: 0.98,
					Metadata: map[string]string{"composite_score": "0.9300"},
				},
			},
		},
	}
	cm.FAQPriorityEnabled = true
	cm.FAQDirectAnswerThreshold = 0.9

	plugin := &PluginIntoChatMessage{}
	next := func() *PluginError { return nil }
	if err := plugin.OnEvent(context.Background(), types.INTO_CHAT_MESSAGE, cm, next); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(cm.UserContent, `id="FAQ-1" match="exact"`) {
		t.Fatalf("boosted FAQ below the threshold was marked exact: %s", cm.UserContent)
	}
	if !strings.Contains(cm.UserContent, `id="FAQ-2" match="exact"`) {
		t.Fatalf("the FAQ that cleared the threshold was not marked exact: %s", cm.UserContent)
	}
}
