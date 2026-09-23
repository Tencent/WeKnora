package parity

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/chat"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestVolcengineCompletionBudgetFieldsAreMutuallyExclusive(t *testing.T) {
	vendor, ok := modelruntime.Get("volcengine")
	require.True(t, ok)
	for _, model := range vendor.ModelsByType(types.ModelTypeKnowledgeQA) {
		t.Run(model.ID, func(t *testing.T) {
			for _, stream := range []bool{false, true} {
				for _, thinking := range []bool{false, true} {
					for _, budget := range []int{0, 256} {
						body := buildBody(t, &chat.ChatConfig{
							Provider: "volcengine", ModelName: model.ID, BaseURL: "http://127.0.0.1:9/v3",
							Spec: &types.ModelSpecOverride{Compat: map[string]any{
								"extra_body": map[string]any{"max_tokens": 1024, "max_completion_tokens": 2048},
							}},
						}, &api.Options{MaxTokens: budget, Thinking: ptrBool(thinking)}, stream)
						require.NotContains(t, body, "max_tokens")
						want := 2048
						if budget > 0 {
							want = budget
						}
						require.Equal(t, float64(want), body["max_completion_tokens"])
					}
				}
			}
		})
	}
}
