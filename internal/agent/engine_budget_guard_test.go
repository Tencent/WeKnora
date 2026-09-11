package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/stretchr/testify/require"
)

// Big but legitimate tool schema — the guard must count tool schemas, which
// ride on every request yet are deliberately excluded from the compaction
// trigger's estimate.
func bulkyTool(descRunes int) []chat.Tool {
	return []chat.Tool{{
		Function: chat.FunctionDef{
			Name:        "knowledge_search",
			Description: strings.Repeat("search ", descRunes),
			Parameters:  json.RawMessage(`{"type":"object"}`),
		},
	}}
}

func TestGuardRequestBudget_NoWindowConfigured(t *testing.T) {
	// Without a configured window there is nothing to guard against — same
	// policy as compaction.
	engine := newTestEngine(t, &mockChat{})
	require.NoError(t, engine.guardRequestBudget(
		context.Background(), 1, 10_000_000, bulkyTool(1000)))
}

func TestGuardRequestBudget_Fits(t *testing.T) {
	engine := newTestEngine(t, &mockChat{}, withMaxContextTokens(32000))
	require.NoError(t, engine.guardRequestBudget(context.Background(), 1, 1000, bulkyTool(10)))
}

func TestGuardRequestBudget_OversizedHistoryTrips(t *testing.T) {
	engine := newTestEngine(t, &mockChat{}, withMaxContextTokens(32000))

	// ~308k runes of history against a 32k window: the exact #3158 shape —
	// a turn that is born over budget, with no history for compaction to
	// reclaim.
	huge := strings.Repeat("描", 308_000)
	err := engine.guardRequestBudget(
		context.Background(), 1, engine.tokenEstimator.EstimateString(huge), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "context window")
	require.Contains(t, err.Error(), "knowledge-base scope")
	require.Contains(t, err.Error(), fmt.Sprint(engine.config.MaxContextTokens))
}

func TestGuardRequestBudget_OversizedToolSchemasTrip(t *testing.T) {
	engine := newTestEngine(t, &mockChat{}, withMaxContextTokens(32000))

	// Small history, one giant tool schema: the guard must still trip,
	// because the schema is billed on every request.
	err := engine.guardRequestBudget(context.Background(), 1, 1000, bulkyTool(30_000))
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool schemas")
}

func TestGuardRequestBudget_ZeroWindowEngine(t *testing.T) {
	// An engine with a nil estimator must not panic; newTestEngine always
	// builds one, so exercise the config-only short circuit instead.
	engine := newTestEngine(t, &mockChat{})
	engine.config.MaxContextTokens = 0
	require.NoError(t, engine.guardRequestBudget(context.Background(), 1, 999_999, nil))
}
