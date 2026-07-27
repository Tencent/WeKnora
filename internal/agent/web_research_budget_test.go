package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	agenttoken "github.com/Tencent/WeKnora/internal/agent/token"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebResearchBudgetRetriesRetryableFetchFailureBeforeFinalizing(t *testing.T) {
	maxRetries := 1
	budget := newWebResearchBudget(&types.AgentConfig{WebFetchMaxRetries: &maxRetries})
	args := map[string]interface{}{
		"items": []interface{}{map[string]interface{}{
			"url":    "https://example.com/transient",
			"prompt": "extract facts",
		}},
	}
	_, allowed := budget.beforeToolCall(agenttools.ToolWebFetch, args)
	require.True(t, allowed)

	budget.afterToolCall(agenttools.ToolWebFetch, failedFetchResult(true))
	finalize, _, _ := budget.finalizationState()
	assert.False(t, finalize, "retryable failures must use the configured retry")

	_, allowed = budget.beforeToolCall(agenttools.ToolWebFetch, args)
	require.True(t, allowed)
	budget.afterToolCall(agenttools.ToolWebFetch, failedFetchResult(true))

	finalize, reason, _ := budget.finalizationState()
	assert.True(t, finalize)
	assert.Equal(t, "all_web_fetches_failed", reason)
}

func TestWebResearchBudgetFinalizesMixedFailuresAfterRetriesExhausted(t *testing.T) {
	maxRetries := 0
	budget := newWebResearchBudget(&types.AgentConfig{WebFetchMaxRetries: &maxRetries})
	args := map[string]interface{}{
		"items": []interface{}{
			map[string]interface{}{"url": "https://example.com/ok", "prompt": "extract facts"},
			map[string]interface{}{"url": "https://example.com/forbidden", "prompt": "extract facts"},
		},
	}
	_, allowed := budget.beforeToolCall(agenttools.ToolWebFetch, args)
	require.True(t, allowed)

	budget.afterToolCall(agenttools.ToolWebFetch, &types.ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"all_failed": false,
			"results": []map[string]interface{}{
				{"url": "https://example.com/ok", "status": "success"},
				{"url": "https://example.com/forbidden", "status": "failed", "retryable": false},
			},
		},
	})

	finalize, reason, _ := budget.finalizationState()
	assert.True(t, finalize)
	assert.Equal(t, "web_fetch_retries_exhausted", reason)
}

func TestWebResearchBudgetRetriesLegacyPartialFetchFailure(t *testing.T) {
	maxRetries := 1
	budget := newWebResearchBudget(&types.AgentConfig{WebFetchMaxRetries: &maxRetries})
	args := map[string]interface{}{
		"items": []interface{}{
			map[string]interface{}{"url": "https://example.com/ok", "prompt": "extract facts"},
			map[string]interface{}{"url": "https://example.com/transient", "prompt": "extract facts"},
		},
	}
	_, allowed := budget.beforeToolCall(agenttools.ToolWebFetch, args)
	require.True(t, allowed)

	budget.afterToolCall(agenttools.ToolWebFetch, &types.ToolResult{
		Success: false,
		Data: map[string]interface{}{
			"results": []map[string]interface{}{
				{"url": "https://example.com/ok", "raw_content": "verified content"},
				{"url": "https://example.com/transient", "error": "request timed out"},
			},
		},
	})

	finalize, _, _ := budget.finalizationState()
	assert.False(t, finalize)
	_, allowed = budget.beforeToolCall(agenttools.ToolWebFetch, map[string]interface{}{
		"items": []interface{}{map[string]interface{}{
			"url": "https://example.com/transient", "prompt": "extract facts",
		}},
	})
	assert.True(t, allowed)
}

func failedFetchResult(retryable bool) *types.ToolResult {
	return &types.ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"all_failed": true,
			"results": []map[string]interface{}{{
				"url":        "https://example.com/transient",
				"status":     "failed",
				"retryable":  retryable,
				"error_code": "connection_timeout",
			}},
		},
	}
}

func TestWebResearchFinalizationInstructionDoesNotBecomeCurrentUserQuery(t *testing.T) {
	messages := []chat.Message{
		{Role: "system", Content: "agent system prompt"},
		{Role: "user", Content: "original research question"},
		{Role: "assistant", Content: "searching"},
		{Role: "tool", Content: "web evidence"},
	}
	appendWebResearchFinalizationInstruction(&messages, "all_web_fetches_failed")

	assert.Equal(t, "system", messages[0].Role)
	assert.Contains(t, messages[0].Content, "Web research must stop now")
	assert.Equal(t, "original research question", messages[1].Content)

	estimator, err := agenttoken.NewEstimator()
	require.NoError(t, err)
	compressed := agenttoken.CompressContext(messages, estimator, 20, estimator.EstimateMessages(messages))
	assert.Contains(t, messageContents(compressed), "original research question")
	assert.Contains(t, messageContents(compressed), "web evidence")
}

func messageContents(messages []chat.Message) string {
	var contents []string
	for _, message := range messages {
		contents = append(contents, message.Content)
	}
	return strings.Join(contents, "\n")
}

func TestWebResearchBudgetDeduplicatesEquivalentSearchQueries(t *testing.T) {
	budget := newWebResearchBudget(&types.AgentConfig{})
	_, allowed := budget.beforeToolCall(agenttools.ToolWebSearch, map[string]interface{}{
		"query": "official 8BitDo Orion NS specs",
	})
	require.True(t, allowed)

	blocked, allowed := budget.beforeToolCall(agenttools.ToolWebSearch, map[string]interface{}{
		"query": "Orion NS specs official 8bitdo",
	})

	assert.False(t, allowed)
	require.NotNil(t, blocked)
	assert.Equal(t, true, blocked.Data["research_budget_exhausted"])
	assert.Equal(t, "duplicate_search_query", blocked.Data["reason"])
}

func TestWebResearchBudgetLimitsSearchRounds(t *testing.T) {
	maxCalls := 2
	budget := newWebResearchBudget(&types.AgentConfig{WebSearchMaxCalls: &maxCalls})
	for _, query := range []string{"first query", "second different query"} {
		_, allowed := budget.beforeToolCall(agenttools.ToolWebSearch, map[string]interface{}{"query": query})
		require.True(t, allowed)
	}

	blocked, allowed := budget.beforeToolCall(agenttools.ToolWebSearch, map[string]interface{}{"query": "third unique query"})

	assert.False(t, allowed)
	assert.Equal(t, "web_search_budget_exhausted", blocked.Data["reason"])
}

func TestWebResearchBudgetAllowsFetchAfterSearchBudgetIsReached(t *testing.T) {
	maxCalls := 1
	budget := newWebResearchBudget(&types.AgentConfig{WebSearchMaxCalls: &maxCalls})
	_, allowed := budget.beforeToolCall(agenttools.ToolWebSearch, map[string]interface{}{"query": "one search"})
	require.True(t, allowed)

	budget.afterToolCall(agenttools.ToolWebSearch, &types.ToolResult{Success: true})
	finalize, _, _ := budget.finalizationState()
	assert.False(t, finalize)

	_, allowed = budget.beforeToolCall(agenttools.ToolWebFetch, map[string]interface{}{
		"items": []interface{}{map[string]interface{}{
			"url":    "https://example.com/page",
			"prompt": "verify facts",
		}},
	})
	assert.True(t, allowed, "search budget must not prevent fetching its results")

	blocked, allowed := budget.beforeToolCall(agenttools.ToolWebSearch, map[string]interface{}{"query": "second search"})
	assert.False(t, allowed)
	assert.Equal(t, "web_search_budget_exhausted", blocked.Data["reason"])
	finalize, reason, injectDirective := budget.finalizationState()
	assert.True(t, finalize)
	assert.Equal(t, "web_search_budget_exhausted", reason)
	assert.True(t, injectDirective)
}

func TestWebResearchBudgetLimitsFetchRetriesPerURL(t *testing.T) {
	maxRetries := 0
	budget := newWebResearchBudget(&types.AgentConfig{WebFetchMaxRetries: &maxRetries})
	args := map[string]interface{}{
		"items": []interface{}{map[string]interface{}{
			"url":    "https://example.com/page",
			"prompt": "extract facts",
		}},
	}
	_, allowed := budget.beforeToolCall(agenttools.ToolWebFetch, args)
	require.True(t, allowed)

	blocked, allowed := budget.beforeToolCall(agenttools.ToolWebFetch, args)

	assert.False(t, allowed)
	assert.Equal(t, "web_fetch_retry_budget_exhausted", blocked.Data["reason"])
}

func TestExecuteLoopFinalizesAfterAllWebFetchesFail(t *testing.T) {
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{{
			ResponseType: types.ResponseTypeAnswer,
			FinishReason: "tool_calls",
			Done:         true,
			ToolCalls: []types.LLMToolCall{{
				ID:   "fetch-1",
				Type: "function",
				Function: types.FunctionCall{
					Name:      agenttools.ToolWebFetch,
					Arguments: `{"items":[{"url":"https://example.com/page","prompt":"verify facts"}]}`,
				},
			}},
		}}},
		{chunks: []types.StreamResponse{{
			ResponseType: types.ResponseTypeAnswer,
			Content:      "Final answer from the search summary; page content was not verified.",
			FinishReason: "stop",
			Done:         true,
		}}},
	}}
	registry := agenttools.NewToolRegistry()
	failedTool := &allFailedWebFetchTool{}
	registry.RegisterTool(failedTool)
	engine := newTestEngine(t, model)
	engine.toolRegistry = registry
	state := &types.AgentState{}
	toolDefinition := chat.Tool{
		Type: "function",
		Function: chat.FunctionDef{
			Name:       failedTool.Name(),
			Parameters: failedTool.Parameters(),
		},
	}

	_, err := engine.executeLoop(context.Background(), state, "research question", emptyMessages(), []chat.Tool{toolDefinition}, "sess-1", "msg-1")

	require.NoError(t, err)
	assert.True(t, state.IsComplete)
	assert.Equal(t, "Final answer from the search summary; page content was not verified.", state.FinalAnswer)
	assert.Equal(t, 2, model.callCount, "all fetch failures must enter finalization on the next agent round")
	assert.Equal(t, 1, failedTool.calls)
	require.Len(t, model.calls, 2)
	assert.True(t, containsResearchFinalizationDirective(model.calls[1]))
}

func TestExecuteLoopUsesRetryableFetchBudgetBeforeFinalizing(t *testing.T) {
	toolCall := types.LLMToolCall{
		ID:   "fetch-retry",
		Type: "function",
		Function: types.FunctionCall{
			Name:      agenttools.ToolWebFetch,
			Arguments: `{"items":[{"url":"https://example.com/transient","prompt":"verify facts"}]}`,
		},
	}
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{{ResponseType: types.ResponseTypeAnswer, FinishReason: "tool_calls", Done: true, ToolCalls: []types.LLMToolCall{toolCall}}}},
		{chunks: []types.StreamResponse{{ResponseType: types.ResponseTypeAnswer, FinishReason: "tool_calls", Done: true, ToolCalls: []types.LLMToolCall{toolCall}}}},
		{chunks: []types.StreamResponse{{ResponseType: types.ResponseTypeAnswer, Content: "Final answer after the configured retry.", FinishReason: "stop", Done: true}}},
	}}
	registry := agenttools.NewToolRegistry()
	failedTool := &retryableFailedWebFetchTool{}
	registry.RegisterTool(failedTool)
	engine := newTestEngine(t, model)
	engine.toolRegistry = registry
	state := &types.AgentState{}
	toolDefinition := chat.Tool{
		Type: "function",
		Function: chat.FunctionDef{
			Name:       failedTool.Name(),
			Parameters: failedTool.Parameters(),
		},
	}

	_, err := engine.executeLoop(context.Background(), state, "research question", emptyMessages(), []chat.Tool{toolDefinition}, "sess-1", "msg-1")

	require.NoError(t, err)
	assert.Equal(t, 2, failedTool.calls)
	assert.Equal(t, 3, model.callCount)
	assert.Equal(t, "Final answer after the configured retry.", state.FinalAnswer)
	assert.False(t, containsResearchFinalizationDirective(model.calls[1]))
	assert.True(t, containsResearchFinalizationDirective(model.calls[2]))
}

type allFailedWebFetchTool struct {
	calls int
}

type retryableFailedWebFetchTool struct {
	allFailedWebFetchTool
}

func (tool *retryableFailedWebFetchTool) Execute(context.Context, json.RawMessage) (*types.ToolResult, error) {
	tool.calls++
	return failedFetchResult(true), nil
}

func (tool *allFailedWebFetchTool) Name() string {
	return agenttools.ToolWebFetch
}

func (tool *allFailedWebFetchTool) Description() string {
	return "test web fetch"
}

func (tool *allFailedWebFetchTool) Parameters() json.RawMessage {
	return utils.GenerateSchema[agenttools.WebFetchInput]()
}

func (tool *allFailedWebFetchTool) Execute(context.Context, json.RawMessage) (*types.ToolResult, error) {
	tool.calls++
	return &types.ToolResult{
		Success: true,
		Output:  "all fetches failed; use existing search summaries",
		Data: map[string]interface{}{
			"all_failed": true,
			"results": []map[string]interface{}{{
				"url":           "https://example.com/page",
				"status":        "failed",
				"retryable":     false,
				"error_code":    "http_403",
				"error_message": "access denied",
			}},
		},
	}, nil
}

func containsResearchFinalizationDirective(messages []chat.Message) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, "Web research must stop now") {
			return true
		}
	}
	return false
}
