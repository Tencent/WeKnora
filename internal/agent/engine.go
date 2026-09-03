package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/compaction"
	"github.com/Tencent/WeKnora/internal/agent/skills"
	agenttoken "github.com/Tencent/WeKnora/internal/agent/token"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/common"
	appconfig "github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
)

// langfuseQueryPreview caps the query length we ship as the agent.execute
// span input — long quoted-context queries can be many KB of prose.
const langfuseQueryPreview = 2000

// AgentEngine is the core engine for running ReAct agents.
//
// History persistence note: the engine is stateless across turns. Conversation
// history is rebuilt from the DB once per turn by the caller
// (see service.LoadAgentHistory) and passed into Execute as llmContext. The
// engine therefore does not maintain its own cache, system-prompt store, or
// cross-turn buffer.
type AgentEngine struct {
	config               *types.AgentConfig
	toolRegistry         *agenttools.ToolRegistry
	chatModel            chat.Chat
	eventBus             *event.EventBus
	knowledgeBasesInfo   []*KnowledgeBaseInfo    // Detailed knowledge base information for prompt
	selectedDocs         []*SelectedDocumentInfo // User-selected documents (via @ mention)
	pinnedMCPServices    []*PinnedMCPServiceInfo // User @mentioned MCP services for this turn
	pinnedSkills         []*PinnedSkillInfo      // User @mentioned skills for this turn
	sessionID            string                  // Session ID for logging and event emission
	systemPromptTemplate string                  // System prompt template (optional, uses default if empty)
	memoryPrompt         string                  // Long-term memory envelope appended to the system prompt
	skillsManager        *skills.Manager         // Skills manager for Progressive Disclosure (optional)
	appConfig            *appconfig.Config       // Application config for prompt template resolution (optional)
	imageDescriber       ImageDescriberFunc      // VLM function for describing images in tool results (optional)
	tokenEstimator       *agenttoken.Estimator   // Token estimator for context window management
	compactor            *compaction.Compactor   // Summarizes older history to fit the context window (nil = disabled)
	lastUsage            types.TokenUsage        // Token usage from the most recent LLM call
	lastSentMsgCount     int                     // Number of messages sent in the most recent LLM call
	// overflowRecovered records that this turn already spent its one
	// compact-and-retry on a context overflow. A second attempt would mean
	// history size was never the problem, and looping on it burns the round
	// budget without changing anything.
	overflowRecovered bool
	// compactionExhaustedAt is the message count at which compaction last
	// reported it could free nothing. Below that count the answer has not
	// changed, so there is no reason to spend another summarization call.
	compactionExhaustedAt int
	modelContext          *modelcontext.Registry // single request-local boundary for every model handle
}

// ImageDescriberFunc generates a text description of an image.
// Signature matches vlm.VLM.Predict so it can be injected without importing the vlm package.
type ImageDescriberFunc func(ctx context.Context, imgBytes []byte, prompt string) (string, error)

// NewAgentEngine creates a new agent engine
func NewAgentEngine(
	config *types.AgentConfig,
	chatModel chat.Chat,
	toolRegistry *agenttools.ToolRegistry,
	eventBus *event.EventBus,
	knowledgeBasesInfo []*KnowledgeBaseInfo,
	selectedDocs []*SelectedDocumentInfo,
	sessionID string,
	systemPromptTemplate string,
) *AgentEngine {
	if eventBus == nil {
		eventBus = event.NewEventBus()
	}
	tokenEst, err := agenttoken.NewEstimator()
	if err != nil {
		return nil
	}
	engine := &AgentEngine{
		config:               config,
		toolRegistry:         toolRegistry,
		chatModel:            chatModel,
		eventBus:             eventBus,
		knowledgeBasesInfo:   knowledgeBasesInfo,
		selectedDocs:         selectedDocs,
		sessionID:            sessionID,
		systemPromptTemplate: systemPromptTemplate,
		tokenEstimator:       tokenEst,
		modelContext:         modelcontext.NewRegistry(config.CitationsEnabled()),
	}

	engine.compactor = compaction.New(chatModel, tokenEst, compaction.Settings{
		Enabled:          true,
		MaxContextTokens: config.MaxContextTokens,
		ReserveTokens:    engine.contextReserveTokens(),
		KeepRecentTokens: config.CompactionKeepRecentTokens,
		MaxSummaryTokens: engine.getCompletionTokenBudget(),
	})

	return engine
}

// SetPinnedMentions sets per-turn @mention scope for MCP services and skills.
func (e *AgentEngine) SetPinnedMentions(mcpServices []*PinnedMCPServiceInfo, skills []*PinnedSkillInfo) {
	e.pinnedMCPServices = mcpServices
	e.pinnedSkills = skills
}

func (e *AgentEngine) systemPromptOptions(ctx context.Context) *BuildSystemPromptOptions {
	opts := &BuildSystemPromptOptions{
		Language: types.LanguageNameFromContext(ctx),
		Config:   e.appConfig,
	}
	if e.skillsManager != nil && e.skillsManager.IsEnabled() {
		opts.SkillsMetadata = e.skillsManager.GetAllMetadata()
	}
	if e.toolRegistry != nil {
		_, err := e.toolRegistry.GetTool(agenttools.ToolShellExec)
		opts.ShellExecEnabled = err == nil
	}
	return opts
}

func (e *AgentEngine) buildSystemPrompt(ctx context.Context) string {
	prompt := BuildSystemPromptWithOptions(
		e.knowledgeBasesInfo,
		e.config.WebSearchEnabled,
		e.systemPromptOptions(ctx),
		e.systemPromptTemplate,
	)
	// Memory has to ride in the system prompt: buildMessagesWithLLMContext
	// drops system messages coming from history, so a separate memory message
	// would be silently discarded from the second turn onward.
	return strings.TrimRight(prompt, " \t\r\n") + e.memoryPrompt + e.modelContext.ProtocolPrompt()
}

// SetMemoryPrompt supplies the long-term memory envelope for this run. Empty
// input leaves the system prompt untouched.
func (e *AgentEngine) SetMemoryPrompt(prompt string) {
	e.memoryPrompt = prompt
}

// NewAgentEngineWithSkills creates a new agent engine with skills support
func NewAgentEngineWithSkills(
	config *types.AgentConfig,
	chatModel chat.Chat,
	toolRegistry *agenttools.ToolRegistry,
	eventBus *event.EventBus,
	knowledgeBasesInfo []*KnowledgeBaseInfo,
	selectedDocs []*SelectedDocumentInfo,
	sessionID string,
	systemPromptTemplate string,
	skillsManager *skills.Manager,
) *AgentEngine {
	engine := NewAgentEngine(
		config,
		chatModel,
		toolRegistry,
		eventBus,
		knowledgeBasesInfo,
		selectedDocs,
		sessionID,
		systemPromptTemplate,
	)
	engine.skillsManager = skillsManager
	return engine
}

// SetAppConfig sets the application config for prompt template resolution.
// This allows the engine to read default prompts from config/prompt_templates/ YAML files.
func (e *AgentEngine) SetAppConfig(cfg *appconfig.Config) {
	e.appConfig = cfg
}

// SetImageDescriber sets the VLM function for generating text descriptions of images
// in tool results. When set, MCP tool result images are automatically analyzed and
// their descriptions are appended to the tool message content.
// This follows the same pattern as Handler.analyzeImageAttachments() in the handler layer.
func (e *AgentEngine) SetImageDescriber(fn ImageDescriberFunc) {
	e.imageDescriber = fn
}

// SetSkillsManager sets the skills manager for the engine
func (e *AgentEngine) SetSkillsManager(manager *skills.Manager) {
	e.skillsManager = manager
}

// GetSkillsManager returns the skills manager
func (e *AgentEngine) GetSkillsManager() *skills.Manager {
	return e.skillsManager
}

// estimateCurrentTokens returns the best estimate of the current context token
// count:
//
//   - When API usage from a previous round is available, that number is the
//     baseline (the provider already billed tools + system + history) and only
//     the messages appended since then are BPE-estimated.
//   - Otherwise it is a pure message-size estimate. Tool schemas are NOT
//     added here. Adding 232 tool schemas (~105k) to a 12k conversation made
//     every round look over the 111k threshold even after a successful
//     compaction.
//
// The baseline already contains the assistant reply, as the `output` half of
// the round that produced it. So the delta must start *after* that reply.
func (e *AgentEngine) estimateCurrentTokens(messages []chat.Message) int {
	if baseline := contextTokensFromUsage(e.lastUsage); baseline > 0 &&
		e.lastSentMsgCount > 0 && e.lastSentMsgCount <= len(messages) {
		return baseline + e.tokenEstimator.EstimateMessages(messages[e.deltaStart(messages):])
	}
	return e.tokenEstimator.EstimateMessages(messages)
}

// deltaStart is the first message not already accounted for by e.lastUsage.
func (e *AgentEngine) deltaStart(messages []chat.Message) int {
	start := e.lastSentMsgCount
	// The reply to the previous request lands here. Its tokens are the usage's
	// completion half, so skip it — but only if it is really there, since a
	// failed round appends nothing.
	if start < len(messages) && messages[start].Role == "assistant" {
		start++
	}
	return start
}

// contextTokensFromUsage reduces a usage report to the size of the context it
// describes. Cache counters are not added in: WeKnora normalizes PromptTokens
// to the provider's full input count, with read/write/miss as descriptive
// subsets of it (see types.TokenUsage.SetPromptCacheUsage).
func contextTokensFromUsage(usage types.TokenUsage) int {
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return usage.PromptTokens + usage.CompletionTokens
}

// Execute executes the agent with conversation history and streaming output
// All events are emitted to EventBus and handled by subscribers (like Handler layer)
func (e *AgentEngine) Execute(
	ctx context.Context,
	sessionID, messageID, query string,
	llmContext []chat.Message,
	imageURLs ...[]string,
) (*types.AgentState, error) {
	logger.Infof(ctx, "[Agent] Starting execution: session=%s, message=%s, query_len=%d, context_msgs=%d",
		sessionID, messageID, len(query), len(llmContext))
	// Ensure tools are cleaned up after execution
	defer e.toolRegistry.Cleanup(ctx)

	common.PipelineInfo(ctx, "Agent", "execute_start", map[string]interface{}{
		"session_id":   sessionID,
		"message_id":   messageID,
		"query":        query,
		"context_msgs": len(llmContext),
	})

	// Open a top-level Langfuse span so the agent run — including every
	// round's LLM call and every tool execution — groups under a single
	// node in the Langfuse UI instead of being flat children of the HTTP
	// trace. No-op when Langfuse is disabled.
	imgCount := 0
	if len(imageURLs) > 0 {
		imgCount = len(imageURLs[0])
	}
	kbIDs := make([]string, 0, len(e.knowledgeBasesInfo))
	for _, kb := range e.knowledgeBasesInfo {
		if kb != nil {
			kbIDs = append(kbIDs, kb.ID)
		}
	}
	spanCtx, agentSpan := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name: "agent.execute",
		Input: map[string]interface{}{
			"query":        truncateRunes(query, langfuseQueryPreview),
			"query_len":    len(query),
			"context_msgs": len(llmContext),
			"image_count":  imgCount,
		},
		Metadata: map[string]interface{}{
			"session_id":          sessionID,
			"message_id":          messageID,
			"max_iterations":      e.config.MaxIterations,
			"parallel_tool_calls": e.config.ParallelToolCalls,
			"web_search":          e.config.WebSearchEnabled,
			"multi_turn":          e.config.MultiTurnEnabled,
			"knowledge_base_ids":  kbIDs,
			"allowed_tools":       e.config.AllowedTools,
		},
	})
	ctx = spanCtx

	// Initialize state
	state := &types.AgentState{
		RoundSteps:    []types.AgentStep{},
		KnowledgeRefs: []*types.SearchResult{},
		IsComplete:    false,
		CurrentRound:  0,
	}

	// Build system prompt using progressive RAG prompt
	// If skills are enabled, include skills metadata (Level 1 - Progressive Disclosure)
	systemPrompt := e.buildSystemPrompt(ctx)
	logger.Debugf(ctx, "[Agent] SystemPrompt: %d chars", len(systemPrompt))

	// Initialize messages with history
	var imgs []string
	if len(imageURLs) > 0 {
		imgs = imageURLs[0]
	}
	messages := e.buildMessagesWithLLMContext(systemPrompt, query, sessionID, llmContext, imgs)

	// Get tool definitions for function calling
	tools := e.buildToolsForLLM()
	toolListStr := strings.Join(listToolNames(tools), ", ")
	logger.Infof(ctx, "[Agent] Ready: %d messages, %d tools [%s], %d images",
		len(messages), len(tools), toolListStr, len(imgs))
	common.PipelineInfo(ctx, "Agent", "tools_ready", map[string]interface{}{
		"session_id": sessionID,
		"tool_count": len(tools),
		"tools":      toolListStr,
	})

	_, err := e.executeLoop(ctx, state, query, messages, tools, sessionID, messageID)
	if err != nil {
		logger.Errorf(ctx, "[Agent] Execution failed: %v", err)
		e.eventBus.Emit(ctx, event.Event{
			ID:        generateEventID("error"),
			Type:      event.EventError,
			SessionID: sessionID,
			Data: event.ErrorData{
				Error:     err.Error(),
				Stage:     "agent_execution",
				SessionID: sessionID,
			},
		})
		finishAgentSpan(agentSpan, state, err)
		return nil, err
	}

	logger.Infof(ctx, "[Agent] Completed: %d rounds, %d steps, complete=%v",
		state.CurrentRound, len(state.RoundSteps), state.IsComplete)
	common.PipelineInfo(ctx, "Agent", "execute_complete", map[string]interface{}{
		"session_id": sessionID,
		"rounds":     state.CurrentRound,
		"steps":      len(state.RoundSteps),
		"complete":   state.IsComplete,
	})
	finishAgentSpan(agentSpan, state, nil)
	return state, nil
}

// finishAgentSpan records the final outcome of an agent execution onto the
// top-level Langfuse span. Extracted so the same payload is used for both
// success and error return paths in Execute().
func finishAgentSpan(span *langfuse.Span, state *types.AgentState, err error) {
	if span == nil {
		return
	}
	totalToolCalls := 0
	for _, step := range state.RoundSteps {
		totalToolCalls += len(step.ToolCalls)
	}
	output := map[string]interface{}{
		"rounds":           state.CurrentRound,
		"steps":            len(state.RoundSteps),
		"tool_calls":       totalToolCalls,
		"complete":         state.IsComplete,
		"final_answer_len": len(state.FinalAnswer),
		"final_answer":     truncateRunes(state.FinalAnswer, langfuseQueryPreview),
	}
	span.Finish(output, map[string]interface{}{
		"rounds":     state.CurrentRound,
		"steps":      len(state.RoundSteps),
		"tool_calls": totalToolCalls,
		"complete":   state.IsComplete,
	}, err)
}

// truncateRunes caps s to n runes and appends "…" when truncated. Identical
// in spirit to the helper in act.go, but kept locally so both files stay
// independent and the truncation budget can diverge if needed.
func truncateRunes(s string, n int) string {
	if n <= 0 || s == "" {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// withinIterationBudget reports whether another ReAct round is allowed.
// A negative MaxIterations is unlimited: the loop still stops on a natural
// finish, user cancel, empty-response retries, or a repeated-content stall.
func (e *AgentEngine) withinIterationBudget(round int) bool {
	if e.config == nil {
		return false
	}
	if e.config.UnlimitedIterations() {
		return true
	}
	return round < e.config.MaxIterations
}

func (e *AgentEngine) maxIterationsDisplay() string {
	if e.config != nil && e.config.UnlimitedIterations() {
		return "unlimited"
	}
	n := 0
	if e.config != nil {
		n = e.config.MaxIterations
	}
	return fmt.Sprintf("%d", n)
}

// executeLoop executes the main ReAct loop
// All events are emitted through EventBus with the given sessionID
func (e *AgentEngine) executeLoop(
	ctx context.Context,
	state *types.AgentState,
	query string,
	messages []chat.Message,
	tools []chat.Tool,
	sessionID string,
	messageID string,
) (*types.AgentState, error) {
	startTime := time.Now()
	common.PipelineInfo(ctx, "Agent", "loop_start", map[string]interface{}{
		"max_iterations": e.config.MaxIterations,
	})

	// Guarantee exactly-one EventAgentComplete emission on every exit path
	// (normal finish, ctx cancel observed at the loop head, or iteration error
	// bubbling up while ctx was cancelled). The emission triggers
	// agent_stream_handler.handleComplete, which is where state.RoundSteps
	// (thinking + tool_call history) gets written onto assistantMessage.AgentSteps
	// so it can be persisted by the caller's defer. Use WithoutCancel so a
	// cancelled ctx does not short-circuit the emit on the stop path.
	completionEmitted := false
	emitCompletion := func() {
		if completionEmitted {
			return
		}
		completionEmitted = true
		e.emitCompletionEvent(context.WithoutCancel(ctx), state, sessionID, messageID, startTime)
	}
	defer emitCompletion()

	emptyRetries := 0
	consecutiveSameContent := 0
	lastResponseContent := ""
loop:
	for e.withinIterationBudget(state.CurrentRound) {
		// Check for context cancellation (request timeout, user cancel, etc.)
		select {
		case <-ctx.Done():
			logger.Warnf(ctx, "[Agent] Context cancelled at round %d: %v",
				state.CurrentRound+1, ctx.Err())
			// Try to salvage existing results
			if totalTC := countTotalToolCalls(state.RoundSteps); totalTC > 0 {
				logger.Infof(ctx, "[Agent] Synthesizing final answer from %d existing tool results",
					totalTC)
				_ = e.streamFinalAnswerToEventBus(ctx, query, state, sessionID)
				state.IsComplete = true
			}
			return state, ctx.Err()
		default:
		}

		// Each iteration runs inside an "agent.round.<N>" Langfuse span.
		// We execute the body in a closure so `defer span.Finish()` fires at
		// every exit path (break/continue/next) without having to sprinkle
		// manual finish calls throughout the many branches below.
		outcome, iterErr := e.runReActIteration(ctx, state, &messages, tools,
			sessionID, messageID, query, &emptyRetries, &consecutiveSameContent, &lastResponseContent)
		if iterErr != nil {
			return state, iterErr
		}
		switch outcome {
		case iterOutcomeContinue:
			continue loop
		case iterOutcomeBreak:
			break loop
		case iterOutcomeNext:
			state.CurrentRound++
		}
	}

	// If loop finished without final answer, generate one — but skip this
	// when the context was cancelled (user pressed stop). In that case the
	// fallback LLM call would fail on the already-cancelled ctx and set
	// state.FinalAnswer to the generic "Sorry, I was unable to generate a
	// complete answer." message, which then leaks to the UI as the final
	// answer for a conversation the user deliberately stopped.
	if !state.IsComplete && ctx.Err() == nil {
		e.handleMaxIterations(ctx, query, state, sessionID)
	}

	return state, nil
}

// iterOutcome directs executeLoop's control flow after one ReAct iteration.
// Using a sentinel (rather than bare return values from runReActIteration)
// keeps the loop's break/continue/next branches explicit in one place.
type iterOutcome int

const (
	// iterOutcomeNext advances state.CurrentRound and loops again.
	iterOutcomeNext iterOutcome = iota
	// iterOutcomeContinue re-runs the loop without advancing the round
	// counter. Used by the empty-content retry path.
	iterOutcomeContinue
	// iterOutcomeBreak exits the loop (final answer, stuck loop, or end).
	iterOutcomeBreak
)

// runReActIteration executes one ReAct step: think → analyze → act → observe.
// Extracted from executeLoop so the whole iteration body can live inside a
// single `defer span.Finish()` scope — otherwise we'd need to sprinkle
// manual finish calls across every break/continue/return branch.
//
// The mutable loop state (messages, empty-retry counter, stuck-loop detector)
// is passed by pointer so iterations share progress.
func (e *AgentEngine) runReActIteration(
	parentCtx context.Context,
	state *types.AgentState,
	messagesPtr *[]chat.Message,
	tools []chat.Tool,
	sessionID, assistantMessageID, query string,
	emptyRetries, consecutiveSameContent *int,
	lastResponseContent *string,
) (outcome iterOutcome, retErr error) {
	roundStart := time.Now()
	round := state.CurrentRound + 1

	// Open the round-level Langfuse span. Any chat/tool calls made inside
	// this iteration will attach under it via ctx, giving the UI a clean
	// trace → agent.execute → agent.round.N → (chat + tools) structure.
	ctx, roundSpan := langfuse.GetManager().StartSpan(parentCtx, langfuse.SpanOptions{
		Name: fmt.Sprintf("agent.round.%d", round),
		Input: map[string]interface{}{
			"round":          round,
			"message_count":  len(*messagesPtr),
			"max_iterations": e.config.MaxIterations,
		},
		Metadata: map[string]interface{}{
			"iteration":  state.CurrentRound,
			"round":      round,
			"session_id": sessionID,
		},
	})

	var (
		response      *types.ChatResponse
		toolCallCount int
	)
	defer func() {
		if roundSpan == nil {
			return
		}
		out := map[string]interface{}{
			"round":      round,
			"outcome":    outcome.String(),
			"tool_calls": toolCallCount,
		}
		if response != nil {
			out["has_tool_calls"] = len(response.ToolCalls) > 0
			out["finish_reason"] = response.FinishReason
			out["content_len"] = len(response.Content)
			if response.Usage.TotalTokens > 0 {
				out["prompt_tokens"] = response.Usage.PromptTokens
				out["completion_tokens"] = response.Usage.CompletionTokens
				out["total_tokens"] = response.Usage.TotalTokens
			}
		}
		out["duration_ms"] = time.Since(roundStart).Milliseconds()
		roundSpan.Finish(out, map[string]interface{}{
			"round":       round,
			"tool_calls":  toolCallCount,
			"outcome":     outcome.String(),
			"duration_ms": time.Since(roundStart).Milliseconds(),
		}, retErr)
	}()

	// Compact older history before the next assistant response when the
	// estimate is over the threshold. The trigger is estimateCurrentTokens —
	// usage+delta when a previous round reported one, otherwise the message
	// list alone. Tool schemas are not added here.
	currentTokens := e.estimateCurrentTokens(*messagesPtr)
	managed, changed := e.manageContextWindow(ctx, *messagesPtr, round, currentTokens)
	if changed {
		*messagesPtr = managed
		currentTokens = e.tokenEstimator.EstimateMessages(managed)
	}

	logger.Infof(ctx, "[Agent][Round-%d/%s] Starting: %d messages, %d tools, est_tokens=%d",
		round, e.maxIterationsDisplay(), len(*messagesPtr), len(tools), currentTokens)
	e.logContextPrediction(ctx, round, *messagesPtr, tools, currentTokens)
	common.PipelineInfo(ctx, "Agent", "round_start", map[string]interface{}{
		"iteration":      state.CurrentRound,
		"round":          round,
		"message_count":  len(*messagesPtr),
		"pending_tools":  len(tools),
		"max_iterations": e.config.MaxIterations,
	})

	// 1. Think: Call LLM with function calling (includes retry + graceful degradation)
	e.lastSentMsgCount = len(*messagesPtr)
	resp, err := e.callLLMWithRetry(ctx, messagesPtr, tools, state, query, state.CurrentRound, sessionID)
	if err != nil {
		retErr = err
		return iterOutcomeNext, err
	}
	if resp == nil {
		return iterOutcomeBreak, nil
	}

	// The round's own token estimate can be wrong — history is estimated, not
	// counted — so a request that looked safe can still come back having hit
	// the window. Compacting and retrying once turns that into a recovered
	// round instead of a wasted one. Once per turn: if the retry overflows
	// too, the problem is not the history size.
	if !e.overflowRecovered && e.responseHitContextLimit(resp) {
		e.overflowRecovered = true
		logger.Warnf(ctx, "[Agent][Round-%d] Response hit the context window (finish=%s, "+
			"completion=%d of %d requested); compacting and retrying once",
			round, resp.FinishReason, resp.Usage.CompletionTokens, e.getCompletionTokenBudget())
		*messagesPtr = e.forceCompaction(ctx, *messagesPtr, round)
		e.lastSentMsgCount = len(*messagesPtr)
		resp, err = e.callLLMWithRetry(ctx, messagesPtr, tools, state, query, state.CurrentRound, sessionID)
		if err != nil {
			retErr = err
			return iterOutcomeNext, err
		}
		if resp == nil {
			return iterOutcomeBreak, nil
		}
	}
	response = resp
	e.logContextDrift(ctx, round, currentTokens, response.Usage)
	if response.Usage.TotalTokens > 0 {
		e.lastUsage = response.Usage
		state.TurnUsage.Accumulate(response.Usage)
		logger.Infof(ctx, "[Agent][Round-%d] Usage: prompt=%d, completion=%d, total=%d, "+
			"cache_read=%d, cache_write=%d, cache_hit_rate=%.1f%%, cache_status=%s",
			round, response.Usage.PromptTokens,
			response.Usage.CompletionTokens, response.Usage.TotalTokens,
			response.Usage.CacheReadTokens, response.Usage.CacheWriteTokens,
			response.Usage.PromptCacheHitRate(), response.Usage.CacheStatus)
	}

	// Detect stuck loops: if the LLM keeps returning the same content
	// without tool calls (e.g., an unhandled finish reason), break early.
	if len(response.ToolCalls) == 0 && response.Content != "" {
		if response.Content == *lastResponseContent {
			*consecutiveSameContent++
		} else {
			*consecutiveSameContent = 0
		}
		*lastResponseContent = response.Content
		if *consecutiveSameContent >= maxRepeatedResponseRounds {
			logger.Warnf(ctx, "[Agent][Round-%d] Detected stuck loop: same content repeated %d times (finish=%s), stopping",
				round, *consecutiveSameContent+1, response.FinishReason)
			state.FinalAnswer = response.Content
			state.IsComplete = true
			return iterOutcomeBreak, nil
		}
	} else {
		*consecutiveSameContent = 0
		*lastResponseContent = ""
	}

	// Create agent step
	step := types.AgentStep{
		Iteration:        state.CurrentRound,
		Thought:          response.Content,
		ReasoningContent: response.ReasoningContent,
		ToolCalls:        make([]types.ToolCall, 0),
		Timestamp:        time.Now(),
	}

	// If the request was cancelled while the LLM was streaming (e.g. the
	// user pressed "stop"), the stream driver still returns a usable
	// response (partial content / finish_reason="stop" / no tool calls).
	// Do NOT let analyzeResponse treat that partial thinking text as the
	// final answer — it would pollute Message.Content with mid-stream
	// thinking and show up as a duplicate card next to the intermediate-
	// steps tree. Preserve the partial thinking as an AgentStep and break
	// out of the loop without marking state.IsComplete. executeLoop's
	// deferred emitCompletionEvent will persist the step onto
	// Message.AgentSteps so it still appears in the tree on refresh.
	if ctx.Err() != nil {
		logger.Warnf(ctx, "[Agent][Round-%d] Context cancelled during LLM call; preserving partial step",
			round)
		if step.Thought != "" || len(step.ToolCalls) > 0 {
			state.RoundSteps = append(state.RoundSteps, step)
		}
		return iterOutcomeBreak, nil
	}

	// 2. Analyze: Check for stop conditions (natural stop with no tool calls)
	verdict := e.analyzeResponse(ctx, response, step, state.CurrentRound, sessionID, roundStart)
	if verdict.isDone {
		// Guard against empty content: when the LLM stops naturally with no
		// content and no tool calls (e.g., thinking-only loop without KB),
		// retry with a nudge message instead of accepting an empty answer.
		if verdict.emptyContent {
			*emptyRetries++
			if *emptyRetries <= maxEmptyResponseRetries {
				logger.Warnf(ctx, "[Agent][Round-%d] Empty content with stop - retrying (%d/%d)",
					round, *emptyRetries, maxEmptyResponseRetries)
				*messagesPtr = append(*messagesPtr, chat.Message{
					Role:    "user",
					Content: "Please provide your complete answer now as plain text.",
				})
				return iterOutcomeContinue, nil
			}
			// Retries exhausted — use fallback message rather than empty answer
			logger.Warnf(ctx, "[Agent][Round-%d] Empty content after %d retries - using fallback",
				round, maxEmptyResponseRetries)
			state.FinalAnswer = "I'm sorry, I was unable to generate a response. Please try again."
			state.IsComplete = true
			state.RoundSteps = append(state.RoundSteps, verdict.step)
			return iterOutcomeBreak, nil
		}
		state.FinalAnswer = verdict.finalAnswer
		state.IsComplete = true
		state.RoundSteps = append(state.RoundSteps, verdict.step)
		return iterOutcomeBreak, nil
	}

	// This round is non-terminal (it will execute tools and loop again). Any
	// plain assistant text streamed live to the answer area this round was a
	// preamble (e.g. "let me search the knowledge base…"), not the final
	// answer. No explicit retraction signal is emitted: the agent only ends by
	// stopping naturally with plain text and no tool calls, so the upcoming
	// tool-call events are themselves the authoritative "that wasn't the final
	// answer" marker. Both the stream handler and the UI treat any answer text
	// preceding a tool call in the same stream as a preamble and relocate it
	// into the steps tree. The preamble is still preserved as this round's
	// Thought.

	// 3. Act: Execute tool calls
	e.executeToolCalls(ctx, response, &step, state.CurrentRound, sessionID, assistantMessageID)
	toolCallCount = len(step.ToolCalls)

	// 4. Observe: Add tool results to messages and write to context
	state.RoundSteps = append(state.RoundSteps, step)
	*messagesPtr = e.appendToolResults(*messagesPtr, step)
	common.PipelineInfo(ctx, "Agent", "round_end", map[string]interface{}{
		"iteration":   state.CurrentRound,
		"round":       round,
		"tool_calls":  toolCallCount,
		"thought_len": len(step.Thought),
	})

	return iterOutcomeNext, nil
}

// String returns a stable label for Langfuse output payloads.
func (o iterOutcome) String() string {
	switch o {
	case iterOutcomeNext:
		return "next"
	case iterOutcomeContinue:
		return "continue"
	case iterOutcomeBreak:
		return "break"
	default:
		return "unknown"
	}
}

// ---------------------------------------------------------------------------
// Tool result image VLM description helpers
// ---------------------------------------------------------------------------

const toolImageAnalysisPrompt = "Describe the content of this image in detail. " +
	"If it contains text, extract all readable text. " +
	"If it contains charts or diagrams, describe the data and structure."

// describeImages generates text descriptions for tool result images using the
// configured imageDescriber (VLM). Each image is decoded from a data URI and
// analyzed independently. Failures are logged and skipped gracefully.
// This follows the same pattern as Handler.analyzeImageAttachments().
func (e *AgentEngine) describeImages(ctx context.Context, imageDataURIs []string) []string {
	if e.imageDescriber == nil {
		return nil
	}
	var descriptions []string
	for i, dataURI := range imageDataURIs {
		if ctx.Err() != nil {
			logger.Warnf(ctx, "[Agent] Context cancelled, skipping remaining %d tool result images", len(imageDataURIs)-i)
			break
		}
		imgBytes, err := decodeDataURIBytes(dataURI)
		if err != nil {
			logger.Warnf(ctx, "[Agent] Failed to decode tool result image %d: %v", i, err)
			continue
		}
		desc, err := e.imageDescriber(ctx, imgBytes, toolImageAnalysisPrompt)
		if err != nil {
			logger.Warnf(ctx, "[Agent] VLM analysis failed for tool result image %d: %v", i, err)
			continue
		}
		descriptions = append(descriptions, strings.TrimSpace(desc))
	}
	return descriptions
}

// decodeDataURIBytes extracts raw bytes from a "data:mime;base64,..." URI.
// Retries with RawStdEncoding when standard base64 decoding fails (some MCP
// servers omit trailing '=' padding).
func decodeDataURIBytes(dataURI string) ([]byte, error) {
	if !strings.HasPrefix(dataURI, "data:") {
		return nil, fmt.Errorf("not a data URI")
	}
	idx := strings.Index(dataURI, ";base64,")
	if idx < 0 {
		return nil, fmt.Errorf("unsupported data URI encoding (expected base64)")
	}
	raw := dataURI[idx+8:]
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		// Retry without padding — some MCP servers omit trailing '='
		decoded, err = base64.RawStdEncoding.DecodeString(raw)
	}
	return decoded, err
}
