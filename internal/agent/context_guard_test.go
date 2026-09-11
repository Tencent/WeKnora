package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

func newGuardTestEngine(t *testing.T, maxContextTokens int) *AgentEngine {
	t.Helper()
	return newTestEngine(t, nil, func(cfg *types.AgentConfig) {
		cfg.MaxContextTokens = maxContextTokens
	})
}

// TestEnsureTurnFitsContextWindow pins the fail-fast guard: a current turn
// that cannot fit the window (no matter what compaction does) aborts with an
// actionable error instead of resending the same oversized request for the
// rest of maxIterations.
func TestEnsureTurnFitsContextWindow(t *testing.T) {
	var big strings.Builder
	for big.Len() < 600_000 {
		big.WriteString("回滚 SLA 为 45 分钟，发布窗口每周二与周四。")
	}
	bigMessage := []chat.Message{{Role: "user", Content: big.String()}}

	t.Run("oversized turn is rejected", func(t *testing.T) {
		engine := newGuardTestEngine(t, 100_000)
		tokens := engine.tokenEstimator.EstimateMessages(bigMessage)
		err := engine.ensureTurnFitsContextWindow(context.Background(), 1, tokens, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "exceeds the model context window")
		require.Contains(t, err.Error(), "bound knowledge bases")
	})

	t.Run("fitting turn passes", func(t *testing.T) {
		engine := newGuardTestEngine(t, 400_000)
		tokens := engine.tokenEstimator.EstimateMessages(bigMessage)
		err := engine.ensureTurnFitsContextWindow(context.Background(), 1, tokens, nil)
		require.NoError(t, err)
	})

	t.Run("unknown window keeps provider-judged behaviour", func(t *testing.T) {
		engine := newGuardTestEngine(t, 0)
		err := engine.ensureTurnFitsContextWindow(context.Background(), 1, 1_000_000, nil)
		require.NoError(t, err)
	})

	t.Run("tool schemas count when no usage baseline exists", func(t *testing.T) {
		engine := newGuardTestEngine(t, 100_000)
		// Messages alone fit comfortably…
		msgTokens := engine.tokenEstimator.EstimateMessages(nil)
		// …but a tool surface sized like the real one (~232 schemas) pushes
		// the request over the window, and with no usage baseline the guard
		// must add the schema cost itself.
		schema := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"],"additionalProperties":false}`)
		tools := make([]chat.Tool, 232)
		for i := range tools {
			tools[i] = chat.Tool{
				Type: "function",
				Function: chat.FunctionDef{
					Name:        "tool_" + strings.Repeat("n", 100) + strings.Repeat(string(rune('a'+i%26)), 8),
					Description: strings.Repeat("Retrieves passages from the bound knowledge bases. ", 60),
					Parameters:  schema,
				},
			}
		}
		require.Error(t, engine.ensureTurnFitsContextWindow(context.Background(), 1, msgTokens, tools))
	})
}
