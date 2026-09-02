package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/compaction"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countingTool struct {
	agenttools.BaseTool
	calls int
}

func newCountingTool(name string) *countingTool {
	return &countingTool{BaseTool: agenttools.NewBaseTool(name, "test", json.RawMessage(`{"type":"object"}`))}
}

func (t *countingTool) Execute(context.Context, json.RawMessage) (*types.ToolResult, error) {
	t.calls++
	return &types.ToolResult{Success: true, Output: "executed"}, nil
}

// ---------------------------------------------------------------------------
// Mock: chat.Chat
// ---------------------------------------------------------------------------

type mockResponse struct {
	chunks []types.StreamResponse
}

type mockChat struct {
	mu        sync.Mutex
	responses []mockResponse
	calls     [][]chat.Message
	opts      []*chat.ChatOptions
	callCount int
}

func (m *mockChat) ChatStream(
	_ context.Context,
	messages []chat.Message,
	opts *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.callCount >= len(m.responses) {
		return nil, fmt.Errorf("unexpected ChatStream call #%d (only %d responses prepared)", m.callCount, len(m.responses))
	}
	resp := m.responses[m.callCount]
	m.calls = append(m.calls, append([]chat.Message(nil), messages...))
	m.opts = append(m.opts, opts)
	m.callCount++

	ch := make(chan types.StreamResponse, len(resp.chunks))
	for _, chunk := range resp.chunks {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}

func TestStreamLLMResourceAliasesRoundTrip(t *testing.T) {
	const ref = "resource://AbCdEfGhIjKlMnOpQrStUv"
	model := &mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "![image](res://0"},
		{ResponseType: types.ResponseTypeAnswer, Content: "001)", Done: true},
	}}}}
	engine := newTestEngine(t, model)
	result, err := engine.streamLLMToEventBus(
		context.Background(),
		[]chat.Message{{Role: "tool", Content: "source=" + ref}},
		nil,
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, "![image]("+ref+")", result.Content)
	require.Len(t, model.calls, 1)
	require.Equal(t, "source=res://0001", model.calls[0][0].Content)
}

// TestStreamLLMSummarySlugSurvivesDocumentCompaction is the regression guard for
// the mangled `summary/<uuid>` → `summary/d1` bug. A wiki summary-page slug
// embeds a document's UUID. The unified model-context registry owns the
// resource-before-source encoding order so the slug cannot become summary/d1.
func TestStreamLLMSummarySlugSurvivesDocumentCompaction(t *testing.T) {
	const knowledgeID = "07a20bb1-a662-47cf-9929-06fb5d5b5b5e"
	const summarySlug = "summary/" + knowledgeID

	// The model copies the protected token it saw back into a wiki_read call.
	model := &mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{
		{
			ResponseType: types.ResponseTypeAnswer,
			Content:      "reading the summary",
			ToolCalls: []types.LLMToolCall{{
				Type: "function",
				Function: types.FunctionCall{
					Name:      "wiki_read_page",
					Arguments: `{"slugs":["res://0001"]}`,
				},
			}},
			Done:         true,
			FinishReason: "tool_calls",
		},
	}}}}

	engine := newTestEngine(t, model)
	// The document UUID is registered as citation alias d1, exactly as the RAG
	// context (<document id="d1">…) would have registered it upstream.
	require.Equal(t, "d1", engine.modelContext.RegisterDocument(knowledgeID))

	toolMsg := chat.Message{
		Role:    "tool",
		Content: `<link>[[` + summarySlug + `|Weknora 试错记录.md - Summary]]</link>`,
	}
	result, err := engine.streamLLMToEventBus(context.Background(),
		[]chat.Message{toolMsg}, nil, nil)
	require.NoError(t, err)

	// What the model actually saw must NOT contain the mangled slug; the UUID
	// must have been aliased to a res:// token before citation compaction ran.
	require.Len(t, model.calls, 1)
	sent := model.calls[0][0].Content
	require.NotContains(t, sent, "summary/d1",
		"summary slug was clobbered by document-id compaction (encode ordering regressed)")
	require.Contains(t, sent, "res://", "summary slug must be protected as a res:// token")

	// The model's tool call echoing the token must decode back to the real slug.
	require.Len(t, result.ToolCalls, 1)
	require.Contains(t, result.ToolCalls[0].Function.Arguments, summarySlug)
	require.NotContains(t, result.ToolCalls[0].Function.Arguments, "res://")
}

// Reproduces the round that ended a 40-round conversation: the stream broke
// while serializing a large write_sandbox_file call, after a short preamble had
// already streamed. Treating that as a completed turn let the preamble stand in
// as the final answer and dropped the call, so the stream error must surface.
func TestStreamLLMToEventBus_ErrorAfterContent_IsNotASuccessfulTurn(t *testing.T) {
	model := &mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "非常好，我已经获取了骨架模板。"},
		{
			ResponseType: types.ResponseTypeError,
			Content:      "context deadline exceeded",
			Done:         true,
			FinishReason: types.FinishReasonIncomplete,
			ToolCalls: []types.LLMToolCall{{
				ID: "call-1",
				Function: types.FunctionCall{
					Name:      "write_sandbox_file",
					Arguments: "{\"path\":\"/a.html\",\"content\":\"<htm",
				},
			}},
		},
	}}}}

	engine := newTestEngine(t, model)
	result, err := engine.streamLLMToEventBus(context.Background(), nil, nil, nil)

	require.Error(t, err, "a broken stream must not be reported as a completed turn")
	require.Contains(t, err.Error(), "context deadline exceeded")
	// The partial call rides along for diagnostics, and the finish reason must
	// never fall back to "stop" — that fallback is what ended the conversation.
	require.Len(t, result.ToolCalls, 1)
	require.Equal(t, types.FinishReasonIncomplete, result.FinishReason)
	require.True(t, isTransientError(err), "a broken stream must be retryable")
}

func TestStreamLLMChunkReferenceExpandsBeforeEmission(t *testing.T) {
	model := &mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: `answer <ref id="`},
		{ResponseType: types.ResponseTypeAnswer, Content: `c1"/>`, Done: true},
	}}}}
	engine := newTestEngine(t, model)
	engine.modelContext.RegisterChunk(modelcontext.ChunkReference{
		ChunkID:         "chunk-1",
		KnowledgeBaseID: "kb-1",
		DocumentTitle:   "Doc",
	})
	result, err := engine.streamLLMToEventBus(context.Background(), nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, `answer <kb doc="Doc" chunk_id="chunk-1" kb_id="kb-1" />`, result.Content)
}

func TestRunToolCallRejectsUnresolvedHandlesBeforeExecution(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	engine.toolRegistry = agenttools.NewToolRegistry()
	tool := newCountingTool("test_unresolved")
	engine.toolRegistry.RegisterTool(tool)

	result := engine.runToolCall(
		context.Background(),
		types.LLMToolCall{
			ID: "call-1",
			Function: types.FunctionCall{
				Name:      tool.Name(),
				Arguments: `{"knowledge_id":"d99"}`,
			},
			ModelArguments:     `{"knowledge_id":"d99"}`,
			ArgumentResolution: modelcontext.ArgumentResolutionUnresolved,
			UnresolvedHandles:  []string{"d99"},
		},
		0, 0, 1, "session", "message",
	)
	require.Zero(t, tool.calls)
	require.NotNil(t, result.Result)
	require.False(t, result.Result.Success)
	require.Contains(t, result.Result.Error, "unresolved model handles")
}

func TestRunToolCallDecodesHandlesAfterJSONRepair(t *testing.T) {
	newEngine := func() (*AgentEngine, *countingTool) {
		engine := newTestEngine(t, &mockChat{})
		engine.toolRegistry = agenttools.NewToolRegistry()
		tool := newCountingTool(agenttools.ToolListKnowledgeChunks)
		engine.toolRegistry.RegisterTool(tool)
		return engine, tool
	}

	unknownEngine, unknownTool := newEngine()
	unknown := unknownEngine.runToolCall(
		context.Background(),
		types.LLMToolCall{
			ID:             "call-unknown",
			Function:       types.FunctionCall{Name: unknownTool.Name(), Arguments: `{"knowledge_id":"d99",}`},
			ModelArguments: `{"knowledge_id":"d99",}`,
		},
		0, 0, 1, "session", "message",
	)
	require.Zero(t, unknownTool.calls)
	require.False(t, unknown.Result.Success)
	require.Contains(t, unknown.Result.Error, "unresolved model handles")

	knownEngine, knownTool := newEngine()
	knownEngine.modelContext.RegisterDocument("doc-real")
	known := knownEngine.runToolCall(
		context.Background(),
		types.LLMToolCall{
			ID:             "call-known",
			Function:       types.FunctionCall{Name: knownTool.Name(), Arguments: `{"knowledge_id":"d1",}`},
			ModelArguments: `{"knowledge_id":"d1",}`,
		},
		0, 0, 1, "session", "message",
	)
	require.Equal(t, 1, knownTool.calls)
	require.True(t, known.Result.Success)
	require.Equal(t, "doc-real", known.Args["knowledge_id"])
}

func (m *mockChat) Chat(_ context.Context, _ []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockChat) GetModelName() string { return "mock-model" }
func (m *mockChat) GetModelID() string   { return "mock-id" }

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type testEngineOption func(*types.AgentConfig)

func withMaxIterations(n int) testEngineOption {
	return func(cfg *types.AgentConfig) {
		cfg.MaxIterations = n
	}
}

func withCitationsEnabled(enabled bool) testEngineOption {
	return func(cfg *types.AgentConfig) {
		cfg.CitationEnabled = &enabled
	}
}

func withMaxCompletionTokens(n int) testEngineOption {
	return func(cfg *types.AgentConfig) {
		cfg.MaxCompletionTokens = n
	}
}

func withMaxContextTokens(n int) testEngineOption {
	return func(cfg *types.AgentConfig) {
		cfg.MaxContextTokens = n
	}
}

func TestWithinIterationBudgetUnlimited(t *testing.T) {
	unlimited := newTestEngine(t, &mockChat{}, withMaxIterations(types.UnlimitedMaxIterations))
	require.True(t, unlimited.withinIterationBudget(0))
	require.True(t, unlimited.withinIterationBudget(10_000))
	require.Equal(t, "unlimited", unlimited.maxIterationsDisplay())

	capped := newTestEngine(t, &mockChat{}, withMaxIterations(3))
	require.True(t, capped.withinIterationBudget(0))
	require.True(t, capped.withinIterationBudget(2))
	require.False(t, capped.withinIterationBudget(3))
	require.Equal(t, "3", capped.maxIterationsDisplay())
}

// History may not fill the window: the reply has to land somewhere. A reserve
// expressed as a fraction of the window gets this wrong in both directions —
// too little room on a small window, needlessly early compaction on a big one.
func TestContextCompactionThresholdReservesRoomForTheReply(t *testing.T) {
	engine := newTestEngine(t, &mockChat{},
		withMaxContextTokens(128000), withMaxCompletionTokens(24576))

	// Reserve is the round's own output budget plus estimation slack.
	require.Equal(t, 24576+contextSafetyTokens, engine.contextReserveTokens())
	require.Equal(t, 128000-24576-contextSafetyTokens, engine.compactor.Settings().Threshold())

	// A tiny completion budget still keeps a floor of headroom, rather than
	// letting history run to the very edge of the window.
	small := newTestEngine(t, &mockChat{},
		withMaxContextTokens(128000), withMaxCompletionTokens(1024))
	require.Equal(t, compaction.DefaultReserveTokens, small.contextReserveTokens())

	// An unknown window disables compaction rather than guessing.
	require.Nil(t, newTestEngine(t, &mockChat{}).compactor)
	require.Zero(t, newTestEngine(t, &mockChat{}).compactor.Settings().Threshold())
}

// The usage baseline already includes the assistant reply as its `output`
// half. Starting the delta at that reply counts every completion twice, and
// since the engine re-anchors on fresh usage each round the error rides along
// permanently — inflating the estimate enough to trigger compaction on a
// context that is nowhere near the threshold.
func TestEstimateCurrentTokensDoesNotDoubleCountTheReply(t *testing.T) {
	engine := newTestEngine(t, &mockChat{}, withMaxContextTokens(128000))

	sent := []chat.Message{
		{Role: "system", Content: "you are an agent"},
		{Role: "user", Content: "do the thing"},
	}
	reply := chat.Message{
		Role:             "assistant",
		Content:          "working on it",
		ReasoningContent: strings.Repeat("deliberating carefully. ", 200),
	}
	toolResult := chat.Message{Role: "tool", Name: "t", ToolCallID: "c1", Content: "result"}
	messages := append(append([]chat.Message{}, sent...), reply, toolResult)

	// The provider reported this round: 5000 in, and the reply as output.
	replyTokens := engine.tokenEstimator.EstimateMessage(&reply)
	engine.lastSentMsgCount = len(sent)
	engine.lastUsage = types.TokenUsage{
		PromptTokens:     5000,
		CompletionTokens: replyTokens,
		TotalTokens:      5000 + replyTokens,
	}

	got := engine.estimateCurrentTokens(messages)
	want := 5000 + replyTokens + engine.tokenEstimator.EstimateMessages(messages[len(sent)+1:])
	require.Equal(t, want, got)

	// Stated as the property that actually matters: the reply is counted once.
	require.Less(t, got, 5000+2*replyTokens,
		"the assistant reply must not be billed by both the usage baseline and the delta")
}

// The compaction trigger counts conversation only. Tool schemas ride with
// every request and show up in the provider's usage, but they are not added
// to a no-usage estimate — doing so made a 12k chat with 232 MCP tools look
// like 117k and compact every round, including the first.
func TestEstimateCurrentTokensDoesNotCountToolSchemasWithoutUsage(t *testing.T) {
	engine := newTestEngine(t, &mockChat{}, withMaxContextTokens(128000))

	messages := []chat.Message{
		{Role: "system", Content: "you are an agent"},
		{Role: "user", Content: "do the thing"},
	}
	tools := make([]chat.Tool, 80)
	for i := range tools {
		tools[i] = chat.Tool{
			Type: "function",
			Function: chat.FunctionDef{
				Name:        fmt.Sprintf("tool_%d", i),
				Description: strings.Repeat("does something useful. ", 80),
				Parameters:  []byte(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			},
		}
	}

	got := engine.estimateCurrentTokens(messages)
	require.Equal(t, engine.tokenEstimator.EstimateMessages(messages), got)

	schemaTokens := engine.tokenEstimator.EstimateTools(tools)
	require.Greater(t, schemaTokens, got*50,
		"the fixture has to dwarf the conversation, the way a large MCP tool list does")
	require.False(t, engine.compactor.Settings().ShouldCompact(got),
		"a two-message conversation must not cross the threshold")
}

// summarizerChat counts summarization calls so a test can prove the engine is
// not paying for one every round.
type summarizerChat struct {
	mockChat
	calls int
}

func (s *summarizerChat) Chat(
	context.Context, []chat.Message, *chat.ChatOptions,
) (*types.ChatResponse, error) {
	s.calls++
	return &types.ChatResponse{Content: "## Goal\ndo the thing", FinishReason: "stop"}, nil
}

// The bug this replaces: inside one ReAct turn nothing was compactable, so
// every round crossed the threshold, spent a summarization call, and freed
// nothing. The loop is only broken if a second pass over the compacted context
// declines to call the summarizer again.
func TestContextCompactionDoesNotRunEveryRound(t *testing.T) {
	llm := &summarizerChat{}
	engine := newTestEngine(t, llm,
		withMaxContextTokens(40000), withMaxCompletionTokens(4000))

	// One user message, then many assistant/tool rounds — a ReAct turn with
	// no turn boundary anywhere in it.
	messages := []chat.Message{
		{Role: "system", Content: "you are an agent"},
		{Role: "user", Content: "build me a deck"},
	}
	body := strings.Repeat("tool output content ", 400)
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("call-%d", i)
		messages = append(messages,
			chat.Message{Role: "assistant", ToolCalls: []chat.ToolCall{{
				ID:       id,
				Type:     "function",
				Function: chat.FunctionCall{Name: "write_sandbox_file", Arguments: `{"path":"/w/a.html"}`},
			}}},
			chat.Message{Role: "tool", Name: "write_sandbox_file", ToolCallID: id, Content: body},
		)
	}

	before := engine.tokenEstimator.EstimateMessages(messages)
	require.True(t, engine.compactor.Settings().ShouldCompact(before))

	compacted, changed := engine.manageContextWindow(
		context.Background(), messages, 1, before,
	)
	require.True(t, changed)
	after := engine.tokenEstimator.EstimateMessages(compacted)
	require.Less(t, after, before/2, "compaction has to actually free room")
	callsAfterFirst := llm.calls
	require.Positive(t, callsAfterFirst)

	// Second round over the already-compacted context: no LLM call, because
	// there is nothing left outside the keep-recent budget.
	_, changedAgain := engine.manageContextWindow(
		context.Background(), compacted, 2, after,
	)
	require.False(t, changedAgain)
	require.Equal(t, callsAfterFirst, llm.calls,
		"a context that cannot shrink must not spend another summarization call")
}

// Asking for more output than the window can still hold is rejected outright
// by the provider, which surfaces to the agent as an unexplained failure.
func TestClampCompletionBudgetToContext(t *testing.T) {
	engine := newTestEngine(t, &mockChat{},
		withMaxContextTokens(32000), withMaxCompletionTokens(24576))

	// Plenty of room: the configured budget is untouched.
	require.Equal(t, 24576, engine.clampCompletionBudgetToContext(1000))

	// Filling up: the budget shrinks to what is actually left.
	require.Equal(t, 32000-20000-contextSafetyTokens,
		engine.clampCompletionBudgetToContext(20000))

	// Past full: never returns zero or negative, which providers reject.
	require.Positive(t, engine.clampCompletionBudgetToContext(40000))

	// Unknown window means nothing to clamp against.
	require.Equal(t, 24576,
		newTestEngine(t, &mockChat{}, withMaxCompletionTokens(24576)).
			clampCompletionBudgetToContext(999999))
}

func TestBuildSystemPromptUsesInternalCitationSetting(t *testing.T) {
	model := &mockChat{}
	enabledEngine := newTestEngine(t, model)
	require.Contains(t, enabledEngine.buildSystemPrompt(context.Background()), "Source citations are enabled")

	disabledEngine := newTestEngine(t, model, withCitationsEnabled(false))
	prompt := disabledEngine.buildSystemPrompt(context.Background())
	require.Contains(t, prompt, "Source citations are disabled")
	require.NotContains(t, prompt, "Source citations are enabled")
}

func newTestEngine(t *testing.T, chatModel chat.Chat, opts ...testEngineOption) *AgentEngine {
	t.Helper()
	cfg := &types.AgentConfig{
		MaxIterations: 10,
		Temperature:   0.7,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	engine := NewAgentEngine(
		cfg,
		chatModel,
		nil,
		event.NewEventBus(),
		nil,
		nil,
		"test-session",
		"",
	)
	require.NotNil(t, engine, "NewAgentEngine returned nil (agenttoken.NewEstimator failed?)")
	return engine
}

func emptyMessages() []chat.Message {
	return []chat.Message{
		{Role: "system", Content: "You are a test agent."},
		{Role: "user", Content: "test query"},
	}
}

func emptyTools() []chat.Tool {
	return nil
}

// ---------------------------------------------------------------------------
// TC1: Empty content + stop → should NOT complete with empty FinalAnswer
// ---------------------------------------------------------------------------

func TestExecuteLoop_EmptyContentWithStop_ShouldNotCompleteWithEmpty(t *testing.T) {
	// Simulate: LLM returns empty content with no tool calls (natural stop).
	// The stream closes with no content chunks → streamLLMToEventBus returns fullContent="".
	// streamThinkingToEventBus wraps it as ChatResponse{Content:"", FinishReason:"stop"}.
	// analyzeResponse() returns verdict{isDone:true, finalAnswer:""} → BUG: empty answer.
	//
	// Prepare 3 responses for initial attempt + 2 retries (after fix).
	mock := &mockChat{
		responses: []mockResponse{
			{chunks: []types.StreamResponse{{Done: true}}},
			{chunks: []types.StreamResponse{{Done: true}}},
			{chunks: []types.StreamResponse{{Done: true}}},
		},
	}

	engine := newTestEngine(t, mock)
	state := &types.AgentState{}
	ctx := context.Background()

	_, err := engine.executeLoop(ctx, state, "test query", emptyMessages(), emptyTools(), "sess-1", "msg-1")

	assert.NoError(t, err)
	assert.True(t, state.IsComplete)
	assert.NotEmpty(t, state.FinalAnswer,
		"BUG: FinalAnswer is empty when LLM returns empty content with stop. "+
			"analyzeResponse() should not allow empty content to be accepted as final answer.")
}

// ---------------------------------------------------------------------------
// TC2: Non-empty content + stop → normal completion (regression guard)
// ---------------------------------------------------------------------------

func TestExecuteLoop_NonEmptyContentWithStop_ShouldComplete(t *testing.T) {
	mock := &mockChat{
		responses: []mockResponse{
			{chunks: []types.StreamResponse{
				{Content: "Here is my answer", Done: true},
			}},
		},
	}

	engine := newTestEngine(t, mock)
	state := &types.AgentState{}
	ctx := context.Background()

	_, err := engine.executeLoop(ctx, state, "test query", emptyMessages(), emptyTools(), "sess-1", "msg-1")

	assert.NoError(t, err)
	assert.True(t, state.IsComplete)
	assert.Equal(t, "Here is my answer", state.FinalAnswer)
}

// ---------------------------------------------------------------------------
// TC4: Empty → retry with nudge → non-empty → success
// ---------------------------------------------------------------------------

func TestExecuteLoop_EmptyThenNonEmpty_ShouldRetryAndComplete(t *testing.T) {
	mock := &mockChat{
		responses: []mockResponse{
			// Round 1: empty content → triggers retry + nudge
			{chunks: []types.StreamResponse{{Done: true}}},
			// Round 2: after nudge, LLM produces answer
			{chunks: []types.StreamResponse{
				{Content: "Here is the answer.", Done: true},
			}},
		},
	}

	engine := newTestEngine(t, mock)
	state := &types.AgentState{}
	ctx := context.Background()

	_, err := engine.executeLoop(ctx, state, "test query", emptyMessages(), emptyTools(), "sess-1", "msg-1")

	assert.NoError(t, err)
	assert.True(t, state.IsComplete)
	assert.Equal(t, "Here is the answer.", state.FinalAnswer)
}

// ---------------------------------------------------------------------------
// TC5: FinishReason propagation through streamThinkingToEventBus
// ---------------------------------------------------------------------------

func TestStreamThinkingToEventBus_PropagatesFinishReason(t *testing.T) {
	tests := []struct {
		name         string
		finishReason string
		wantReason   string
	}{
		{"stop", "stop", "stop"},
		{"tool_calls", "tool_calls", "tool_calls"},
		{"length", "length", "length"},
		{"empty_fallback", "", "stop"}, // empty FinishReason → fallback to "stop"
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockChat{
				responses: []mockResponse{
					{chunks: []types.StreamResponse{
						{Content: "test content", Done: true, FinishReason: tt.finishReason},
					}},
				},
			}

			engine := newTestEngine(t, mock)
			ctx := context.Background()
			msgs := []chat.Message{{Role: "user", Content: "test"}}
			tools := []chat.Tool{}

			resp, err := engine.streamThinkingToEventBus(ctx, msgs, tools, 0, "sess-1")

			assert.NoError(t, err)
			assert.Equal(t, tt.wantReason, resp.FinishReason)
		})
	}
}

func TestStreamThinkingToEventBus_SetsCompletionTokenBudget(t *testing.T) {
	t.Run("honors an explicit 4096 budget", func(t *testing.T) {
		mock := &mockChat{
			responses: []mockResponse{
				{chunks: []types.StreamResponse{{Content: "ok", Done: true, FinishReason: "stop"}}},
			},
		}
		engine := newTestEngine(t, mock, withMaxCompletionTokens(4096))
		_, err := engine.streamThinkingToEventBus(context.Background(),
			[]chat.Message{{Role: "user", Content: "test"}}, nil, 0, "sess-1")
		require.NoError(t, err)
		require.Len(t, mock.opts, 1)
		assert.Equal(t, 4096, mock.opts[0].MaxTokens)
		assert.Equal(t, 4096, mock.opts[0].MaxCompletionTokens)
	})

	t.Run("defaults when unset", func(t *testing.T) {
		mock := &mockChat{
			responses: []mockResponse{
				{chunks: []types.StreamResponse{{Content: "ok", Done: true, FinishReason: "stop"}}},
			},
		}
		engine := newTestEngine(t, mock)
		_, err := engine.streamThinkingToEventBus(context.Background(),
			[]chat.Message{{Role: "user", Content: "test"}}, nil, 0, "sess-1")
		require.NoError(t, err)
		require.Len(t, mock.opts, 1)
		assert.Equal(t, types.DefaultSmartReasoningMaxCompletionTokens, mock.opts[0].MaxTokens)
		assert.Equal(t, types.DefaultSmartReasoningMaxCompletionTokens, mock.opts[0].MaxCompletionTokens)
	})

	t.Run("defaults to the write-file budget when a sandbox is bound", func(t *testing.T) {
		mock := &mockChat{
			responses: []mockResponse{
				{chunks: []types.StreamResponse{{Content: "ok", Done: true, FinishReason: "stop"}}},
			},
		}
		engine := newTestEngine(t, mock, func(cfg *types.AgentConfig) {
			cfg.SandboxConfigID = "cfg-a"
		})
		_, err := engine.streamThinkingToEventBus(context.Background(),
			[]chat.Message{{Role: "user", Content: "test"}}, nil, 0, "sess-1")
		require.NoError(t, err)
		require.Len(t, mock.opts, 1)
		assert.Equal(t, types.DefaultAgentMaxCompletionTokens, mock.opts[0].MaxTokens)
		assert.Equal(t, types.DefaultAgentMaxCompletionTokens, mock.opts[0].MaxCompletionTokens)
	})

	t.Run("preserves explicit higher budget", func(t *testing.T) {
		mock := &mockChat{
			responses: []mockResponse{
				{chunks: []types.StreamResponse{{Content: "ok", Done: true, FinishReason: "stop"}}},
			},
		}
		engine := newTestEngine(t, mock, withMaxCompletionTokens(64000))
		_, err := engine.streamThinkingToEventBus(context.Background(),
			[]chat.Message{{Role: "user", Content: "test"}}, nil, 0, "sess-1")
		require.NoError(t, err)
		require.Len(t, mock.opts, 1)
		assert.Equal(t, 64000, mock.opts[0].MaxTokens)
		assert.Equal(t, 64000, mock.opts[0].MaxCompletionTokens)
	})
}

// TestStreamThinkingToEventBus_RoutesReasoningAndAnswerSeparately is the
// regression guard for the "answer first shows under Thinking, then jumps to
// the answer area" UX bug. A natural-stop response that carries reasoning in
// the dedicated reasoning channel (ResponseTypeThinking) plus plain answer
// content (ResponseTypeAnswer) must route the reasoning to thought events and
// the answer live to final-answer events — never the reverse.
func TestStreamThinkingToEventBus_RoutesReasoningAndAnswerSeparately(t *testing.T) {
	mock := &mockChat{
		responses: []mockResponse{
			{chunks: []types.StreamResponse{
				{ResponseType: types.ResponseTypeThinking, Content: "let me reason"},
				{ResponseType: types.ResponseTypeThinking, Content: "", Done: true},
				{ResponseType: types.ResponseTypeAnswer, Content: "The answer "},
				{ResponseType: types.ResponseTypeAnswer, Content: "is 42.", Done: true, FinishReason: "stop"},
			}},
		},
	}

	engine := newTestEngine(t, mock)
	var thoughts, answers string
	engine.eventBus.On(event.EventAgentThought, func(_ context.Context, evt event.Event) error {
		if d, ok := evt.Data.(event.AgentThoughtData); ok {
			thoughts += d.Content
		}
		return nil
	})
	engine.eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		if d, ok := evt.Data.(event.AgentFinalAnswerData); ok {
			answers += d.Content
		}
		return nil
	})

	resp, err := engine.streamThinkingToEventBus(context.Background(),
		emptyMessages(), emptyTools(), 0, "sess-1")
	require.NoError(t, err)

	assert.Equal(t, "let me reason", thoughts, "reasoning_content must stream to thought events")
	assert.Equal(t, "The answer is 42.", answers, "plain answer content must stream live to final-answer events")
	assert.True(t, resp.AnswerStreamed, "AnswerStreamed must be set when answer text was streamed live")
	assert.NotEmpty(t, resp.AnswerEventID, "AnswerEventID must identify the live answer stream")
}

// TestStreamThinkingToEventBus_SplitsInlineThinkBlock verifies that models which
// embed reasoning inline as <think>…</think> in the content channel still have
// their reasoning routed to thought events and only the real answer streamed to
// the final-answer area.
func TestStreamThinkingToEventBus_SplitsInlineThinkBlock(t *testing.T) {
	mock := &mockChat{
		responses: []mockResponse{
			{chunks: []types.StreamResponse{
				{
					ResponseType: types.ResponseTypeAnswer, Content: "<think>hidden reasoning</think>Visible answer.",
					Done: true, FinishReason: "stop",
				},
			}},
		},
	}

	engine := newTestEngine(t, mock)
	var thoughts, answers string
	engine.eventBus.On(event.EventAgentThought, func(_ context.Context, evt event.Event) error {
		if d, ok := evt.Data.(event.AgentThoughtData); ok {
			thoughts += d.Content
		}
		return nil
	})
	engine.eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		if d, ok := evt.Data.(event.AgentFinalAnswerData); ok {
			answers += d.Content
		}
		return nil
	})

	_, err := engine.streamThinkingToEventBus(context.Background(),
		emptyMessages(), emptyTools(), 0, "sess-1")
	require.NoError(t, err)

	assert.Equal(t, "hidden reasoning", thoughts, "inline <think> content must route to thought events")
	assert.Equal(t, "Visible answer.", answers, "answer outside <think> must stream to final-answer events")
}

// TestExecuteLoop_NaturalStop_DoesNotDuplicateAnswer ensures the natural-stop
// branch does not re-emit the full answer (it was already streamed live), so
// the final-answer content appears exactly once instead of streaming under
// Thinking and then "jumping" to a duplicate answer block.
func TestExecuteLoop_NaturalStop_DoesNotDuplicateAnswer(t *testing.T) {
	mock := &mockChat{
		responses: []mockResponse{
			{chunks: []types.StreamResponse{
				{ResponseType: types.ResponseTypeAnswer, Content: "Hello "},
				{ResponseType: types.ResponseTypeAnswer, Content: "world", Done: true, FinishReason: "stop"},
			}},
		},
	}

	engine := newTestEngine(t, mock)
	var answerContent string
	var doneCount int
	engine.eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		if d, ok := evt.Data.(event.AgentFinalAnswerData); ok {
			answerContent += d.Content
			if d.Done {
				doneCount++
			}
		}
		return nil
	})

	state := &types.AgentState{}
	_, err := engine.executeLoop(context.Background(), state, "test query",
		emptyMessages(), emptyTools(), "sess-1", "msg-1")
	require.NoError(t, err)

	assert.True(t, state.IsComplete)
	assert.Equal(t, "Hello world", state.FinalAnswer)
	assert.Equal(t, "Hello world", answerContent,
		"answer content must be emitted exactly once (streamed live, not re-emitted by the natural-stop branch)")
	assert.GreaterOrEqual(t, doneCount, 1, "a Done marker must close the answer stream")
}

// TestExecuteLoop_EndTurnTerminates ensures Anthropic-style end_turn is treated
// like OpenAI's stop when no tool calls are present. Otherwise the ReAct loop
// keeps asking the model again and streams repeated answer chunks.
func TestExecuteLoop_EndTurnTerminates(t *testing.T) {
	mock := &mockChat{
		responses: []mockResponse{
			{chunks: []types.StreamResponse{
				{ResponseType: types.ResponseTypeAnswer, Content: "The answer.", Done: true, FinishReason: "end_turn"},
			}},
		},
	}

	engine := newTestEngine(t, mock)
	state := &types.AgentState{}
	_, err := engine.executeLoop(context.Background(), state, "test query",
		emptyMessages(), emptyTools(), "sess-1", "msg-1")
	require.NoError(t, err)

	assert.True(t, state.IsComplete)
	assert.Equal(t, "The answer.", state.FinalAnswer)
	assert.Equal(t, 1, mock.callCount, "end_turn must end the loop after the first model call")
}

func TestStreamFinalAnswerToEventBus_EmitsDoneWhenProviderEndsWithEmptyChunk(t *testing.T) {
	mock := &mockChat{
		responses: []mockResponse{
			{chunks: []types.StreamResponse{
				{ResponseType: types.ResponseTypeAnswer, Content: "final answer", Done: false},
				{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: "stop"},
			}},
		},
	}

	engine := newTestEngine(t, mock)
	var finalAnswerEvents []event.AgentFinalAnswerData
	engine.eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentFinalAnswerData)
		require.True(t, ok)
		finalAnswerEvents = append(finalAnswerEvents, data)
		return nil
	})

	state := &types.AgentState{}
	err := engine.streamFinalAnswerToEventBus(context.Background(), "test query", state, "sess-1")

	require.NoError(t, err)
	require.Len(t, finalAnswerEvents, 2)
	assert.False(t, finalAnswerEvents[0].Done)
	assert.True(t, finalAnswerEvents[1].Done)
	assert.Equal(t, "final answer", finalAnswerEvents[0].Content+finalAnswerEvents[1].Content,
		"a decoder may hold a short suffix until Done to rule out a split model handle")
	assert.Equal(t, "final answer", state.FinalAnswer)
}
