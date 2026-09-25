package session

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildStreamResponsePromotesUsageOnCompleteEvents(t *testing.T) {
	usage := &types.TokenUsage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120}

	response := buildStreamResponse(interfaces.StreamEvent{
		Type:  types.ResponseTypeComplete,
		Done:  true,
		Data:  map[string]interface{}{"total_steps": 3, "usage": usage},
		Usage: usage,
	}, "req-1")

	require.NotNil(t, response.Usage)
	assert.Equal(t, 120, response.Usage.TotalTokens)
	assert.Equal(t, usage, response.Data["usage"])
}

func TestBuildStreamResponseLeavesUsageNilWhenAbsent(t *testing.T) {
	response := buildStreamResponse(interfaces.StreamEvent{
		Type: types.ResponseTypeAnswer,
		Data: map[string]interface{}{"event_id": "e-1"},
	}, "req-1")

	assert.Nil(t, response.Usage)
}

func TestStreamEventUsageSurvivesJSONRoundTrip(t *testing.T) {
	// The stream manager persists events as JSON (Redis) before the SSE loop
	// reads them back — the typed usage must survive that round trip.
	usage := &types.TokenUsage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120}
	usage.SetPromptCacheUsage(80, 0, 20, true)

	raw, err := json.Marshal(interfaces.StreamEvent{Type: types.ResponseTypeComplete, Usage: usage})
	require.NoError(t, err)

	var restored interfaces.StreamEvent
	require.NoError(t, json.Unmarshal(raw, &restored))
	require.NotNil(t, restored.Usage)
	assert.Equal(t, *usage, *restored.Usage)
}
