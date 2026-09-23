package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/compaction"
	"github.com/Tencent/WeKnora/internal/agent/intentgate"
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
	questionOrigin       *QuestionOriginInfo     // Source of a picked suggested question, if any
	sessionID            string                  // Session ID for logging and event emission
	systemPromptTemplate string                  // System prompt template (optional, uses default if empty)
	memoryPrompt         string                  // Long-term memory envelope appended to the system prompt
	skillsManager        *skills.Manager         // Skills manager for Progressive Disclosure (optional)
	appConfig            *appconfig.Config       // Application config for prompt template resolution (optional)
	imageDescriber       ImageDescriberFunc      // VLM function for describing images in tool results (optional)
	tokenEstimator       *agenttoken.Estimator   // Token estimator for context window management, calibrated
	rawEstimator         *agenttoken.Estimator   // Same tokenizer at scale 1, for measuring the calibration
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
	// checkpointSink, when set, persists compactions that end on a stored
	// turn so later turns start from them. Nil keeps compaction turn-local.
	checkpointSink types.ContextCheckpointSink
	// calibration learns the provider's tokens per estimated token from
	// consecutive requests (see calibrateEstimator).
	calibration tokenCalibration
	// contextRewrites counts compactions and tool-result trims. Either one
	// changes messages an earlier request already sent, so the next request
	// cannot be compared with that one for calibration.
	contextRewrites int
	modelContext    *modelcontext.Registry // single request-local boundary for every model handle
	// steerSink, when set, lets users append messages into the running turn.
	// Drained at every round boundary; nil disables mid-run injection.
	steerSink         types.SteerSink
	allowSteerOverrun bool // one extra ReAct round after a loop-end inject past MaxIterations
	steerOverruns     int  // how many times this turn has already used the extra round
	// intentGate, when set, is consulted before every tool execution
	// (IntentGate 语义门禁层, see docs/plans/2026-09-21-intent-gate-design.md).
	// nil (the default) keeps the pre-IntentGate behavior exactly.
	intentGate intentgate.Gate
	// intentVerdictWriter 是 verdict 的异步落库接缝（设计 §7 verdicts 字段、
	// §6.2 intent_verdicts 表）。nil（默认）只记 Langfuse span + 结构化日志，
	// 不落库。写入必须非阻塞：它是观测面，不是判定链路的一部分。
	intentVerdictWriter intentgate.VerdictWriter
	// intentGateIntent 是当前轮判定的意图基准快照（原始 user prompt +
	// 历史窗口，设计 §8.2 规则 1）：由 runReActIteration 在每轮 Act 前从
	// messages 管线写入、Act 后清除；本轮内的并行工具调用并发只读。
	// 被审对象（当前轮 assistant 输出）此刻尚未 append 进 messages，
	// 快照天然不含它。nil 表示本轮未填（gate 未启用时保持 nil）。
	intentGateIntent *gateIntentSnapshot
	// intentGateAuditor 在 enforce deny 实际拦截时回调（T43）：把
	// intent_policy.enforced_deny 写进 audit log。nil（默认）不写。
	intentGateAuditor func(ctx context.Context, info EnforceDenyInfo)
	// intentApprovalRecorder 在人工审批决策落地后回写 verdict 行的
	// human_override（T60）。nil（默认）不写。
	intentApprovalRecorder func(toolCallID string, approved, modified bool)
}

// EnforceDenyInfo 是一次 enforce 拦截的审计上下文（T43）：actor 从 ctx
// 的 Principal 解析（在 auditor 实现内），这里只带执行点现成字段。
type EnforceDenyInfo struct {
	TenantID   uint64
	SessionID  string
	ToolName   string
	ToolCallID string
	Verdict    intentgate.Verdict
}

// maxSteerOverruns caps loop-end injects past MaxIterations. One extra round
// lets a last-moment nudge revise the answer; further injects stay queued for
// a follow-up turn instead of stretching the same run indefinitely.
const maxSteerOverruns = 1

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
	// The previous turn's measured scale, so this turn's first compaction
	// check is calibrated before it has a provider count of its own.
	tokenEst.SetScale(config.ContextTokenScale)
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
		rawEstimator:         tokenEst.Unscaled(),
		modelContext:         modelcontext.NewRegistry(config.CitationsEnabled()),
	}

	engine.compactor = compaction.New(chatModel, tokenEst, compaction.Settings{
		Enabled:          true,
		MaxContextTokens: config.MaxContextTokens,
		ReserveTokens:    engine.contextReserveTokens(),
		KeepRecentTokens: config.CompactionKeepRecentTokens,
		MaxSummaryTokens: engine.getCompletionTokenBudget(),
		StallTimeout:     engine.getLLMStallTimeout(),
	})

	return engine
}

// SetQuestionOrigin records the knowledge source of a suggested question the
// user picked for this turn; nil clears it.
func (e *AgentEngine) SetQuestionOrigin(origin *QuestionOriginInfo) {
	e.questionOrigin = origin
}

// SetIntentGate installs the IntentGate consulted before every tool
// execution. Nil (the default) disables the gate and keeps behavior
// identical to a build without IntentGate.
func (e *AgentEngine) SetIntentGate(g intentgate.Gate) {
	e.intentGate = g
}

// SetIntentVerdictWriter 安装 verdict 的异步落库写入端（T11）。nil
// （默认）时 verdict 只进 Langfuse span metadata 与结构化日志，不落库。
// 写入端必须非阻塞且 fail-open（见 intentgate.AsyncVerdictWriter）。
func (e *AgentEngine) SetIntentVerdictWriter(w intentgate.VerdictWriter) {
	e.intentVerdictWriter = w
}

// SetIntentGateAuditor 安装 enforce deny 的审计回调（T43）。nil（默认）
// 不写 audit。回调必须自身 fail-open：审计是观测/合规面，失败绝不能再
// 放大成判定链路错误（拦截已经发生，agent 已在自我纠错路径上）。
func (e *AgentEngine) SetIntentGateAuditor(fn func(ctx context.Context, info EnforceDenyInfo)) {
	e.intentGateAuditor = fn
}

// SetIntentApprovalRecorder 安装审批决策回写回调（T60）。nil 不安装。
func (e *AgentEngine) SetIntentApprovalRecorder(fn func(toolCallID string, approved, modified bool)) {
	e.intentApprovalRecorder = fn
}

// SetPinnedMentions sets per-turn @mention scope for MCP services and skills.
func (e *AgentEngine) SetPinnedMentions(mcpServices []*PinnedMCPServiceInfo, skills []*PinnedSkillInfo) {
	e.pinnedMCPServices = mcpServices
	e.pinnedSkills = skills
}

func (e *AgentEngine) systemPromptOptions(ctx context.Context) *BuildSystemPromptOptions {
	opts := &BuildSystemPromptOptions{
		Language:         types.LanguageNameFromContext(ctx),
		Config:           e.appConfig,
		SkillInstallMode: e.config.SkillInstallMode(),
		MemoryPrompt:     e.memoryPrompt,
		ProtocolPrompt:   e.modelContext.ProtocolPrompt(),
	}
	if e.skillsManager != nil && e.skillsManager.IsEnabled() {
		opts.SkillsMetadata = e.skillsManager.GetAllMetadata()
	}
	if e.toolRegistry != nil {
		opts.SelectedTools = e.toolRegistry.ListTools()
		if _, err := e.toolRegistry.GetTool("local_browser"); err == nil {
			metadata := make([]*skills.SkillMetadata, 0, len(opts.SkillsMetadata))
			for _, item := range opts.SkillsMetadata {
				if item != nil && item.Name != "browser" && item.Name != "browser-skill" {
					metadata = append(metadata, item)
				}
			}
			opts.SkillsMetadata = metadata
		}
		_, err := e.toolRegistry.GetTool(agenttools.ToolShellExec)
		opts.ShellExecEnabled = err == nil
	}
	return opts
}

func (e *AgentEngine) buildSystemPrompt(ctx context.Context) string {
	sections := BuildSystemPromptSections(
		e.knowledgeBasesInfo,
		e.config.WebSearchEnabled,
		e.systemPromptOptions(ctx),
		e.systemPromptTemplate,
	)
	for _, section := range sections {
		logger.Debugf(ctx, "[Agent][Prompt] section=%s bytes=%d", section.Name, len(section.Content))
	}
	return renderSystemPromptSections(sections)
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
		// A turn that measures no scale of its own (no tool call, so no two
		// requests to compare) passes on the one it started from, so the
		// newest turn always carries the latest known scale and the loader
		// never has to look past its first page for one.
		TurnUsage: types.TokenUsage{ContextTokenScale: e.config.ContextTokenScale},
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
	if e.toolRegistry != nil {
		e.toolRegistry.RememberMCPHistory(messages)
		e.toolRegistry.RefreshMCPTools(ctx)
	}

	// Get tool definitions for function calling
	tools := e.buildToolsForLLM()
	toolListStr := strings.Join(listToolNames(tools), ", ")
	logger.Infof(ctx, "[Agent] Ready: %d messages, %d tools [%s], mcp_catalog=%d chars, %d images",
		len(messages), len(tools), toolListStr, mcpCatalogDescriptionLen(tools), len(imgs))
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

// loopGuards is the per-turn state the ReAct loop uses to notice it is not
// making progress. All three counters exist because a model can keep the loop
// spinning without ever finishing: empty answers, the same answer repeated,
// and rounds cut off at the completion-token cap.
type loopGuards struct {
	// emptyRetries counts nudges sent after a stop with no content.
	emptyRetries int
	// consecutiveSameContent counts plain rounds that repeated the previous
	// plain round verbatim, an identical lack of content included.
	consecutiveSameContent int
	// sawPlainRound records whether lastResponseContent holds a real
	// observation yet, so the first plain round of a turn is never counted as
	// a repeat of the zero value.
	sawPlainRound       bool
	lastResponseContent string
	// consecutiveLength counts rounds in a row that the provider cut off at
	// the completion-token cap.
	consecutiveLength int
}

// closeAnswerStream emits the Done:true marker for an answer that is actually
// finishing. Loop-end inject skips this so the client does not drop isReplying
// while the engine continues. truncated rides along on the marker because an
// answer streamed live only finds out about the completion cap at the close.
func (e *AgentEngine) closeAnswerStream(ctx context.Context, sessionID, answerID string, truncated bool) {
	if e.eventBus == nil || answerID == "" {
		return
	}
	_ = e.eventBus.Emit(ctx, event.Event{
		ID:        answerID,
		Type:      event.EventAgentFinalAnswer,
		SessionID: sessionID,
		Data: event.AgentFinalAnswerData{
			Content:   "",
			Done:      true,
			Truncated: truncated,
		},
	})
}

// finishStalledTurn ends a turn that a guard stopped rather than the model.
// Whatever text the last round produced stands as the answer; when the round
// produced none, a fallback says why instead of completing with an empty
// message. The answer stream is closed here because no natural-stop path will
// reach it once the loop breaks.
func (e *AgentEngine) finishStalledTurn(
	ctx context.Context,
	state *types.AgentState,
	sessionID string,
	response *types.ChatResponse,
	truncated bool,
) {
	// Text that arrived alongside tool calls is a preamble — "let me look that
	// up" — from a round that meant to keep working. Presenting it as the final
	// answer would show an opening line and call the turn finished, so the
	// fallback speaks for it instead.
	answer := response.Content
	if len(response.ToolCalls) > 0 {
		answer = ""
	}
	answerID := ""
	if response.AnswerStreamed && answer != "" {
		answerID = response.AnswerEventID
	}
	if strings.TrimSpace(answer) == "" {
		answer = stalledAnswerFallback
		if truncated {
			answer = truncatedAnswerFallback
		}
		answerID = ""
	}
	// No answer stream reached the client for this text: the round never
	// streamed it, or it is the fallback just chosen. Emit it once so the turn
	// does not end on a Done marker for content the client never saw.
	if answerID == "" && e.eventBus != nil {
		answerID = generateEventID("answer")
		_ = e.eventBus.Emit(ctx, event.Event{
			ID:        answerID,
			Type:      event.EventAgentFinalAnswer,
			SessionID: sessionID,
			Data: event.AgentFinalAnswerData{
				Content:   answer,
				Done:      false,
				Truncated: truncated,
			},
		})
	}
	e.closeAnswerStream(ctx, sessionID, answerID, truncated)
	state.FinalAnswer = answer
	state.IsComplete = true
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

	guards := &loopGuards{}
loop:
	for e.withinIterationBudget(state.CurrentRound) || e.allowSteerOverrun {
		e.allowSteerOverrun = false
		// Check for context cancellation (request timeout, user cancel, etc.)
		select {
		case <-ctx.Done():
			logger.Warnf(ctx, "[Agent] Context cancelled at round %d: %v",
				state.CurrentRound+1, ctx.Err())
			// Try to salvage existing results
			if totalTC := countTotalToolCalls(state.RoundSteps); totalTC > 0 {
				logger.Infof(ctx, "[Agent] Synthesizing final answer from %d existing tool results",
					totalTC)
				_ = e.streamFinalAnswerToEventBus(ctx, query, state, sessionID, messages)
				state.IsComplete = true
			}
			return state, ctx.Err()
		default:
		}

		// A slow startup, OAuth discovery or explicit refresh may have produced
		// new definitions since the previous response. Publish them only here,
		// after all previous tool calls have finished, and rebuild the wire list.
		if e.toolRegistry != nil {
			e.toolRegistry.RefreshMCPTools(ctx)
			tools = e.buildToolsForLLM()
		}

		// Each iteration runs inside an "agent.round.<N>" Langfuse span.
		// We execute the body in a closure so `defer span.Finish()` fires at
		// every exit path (break/continue/next) without having to sprinkle
		// manual finish calls throughout the many branches below.
		outcome, iterErr := e.runReActIteration(ctx, state, &messages, tools,
			sessionID, messageID, query, guards)
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
		e.handleMaxIterations(ctx, query, state, sessionID, messages)
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
// The mutable loop state (messages and the no-progress guards) is shared
// across iterations so each round can see what the previous ones did.
func (e *AgentEngine) runReActIteration(
	parentCtx context.Context,
	state *types.AgentState,
	messagesPtr *[]chat.Message,
	tools []chat.Tool,
	sessionID, assistantMessageID, query string,
	guards *loopGuards,
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

	// Mid-run steering: drain any user messages queued while the previous
	// round was thinking/executing tools. Runs after compression (injected
	// text stays inside the protected tail) and before lastSentMsgCount is
	// updated (the injected text counts as new delta tokens for the next
	// call), so the very next LLM call sees the user's addition.
	e.drainSteerMessages(ctx, state, messagesPtr, sessionID, assistantMessageID)

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
	// Calibration needs only a prompt count; some providers report no total.
	e.calibrateEstimator(ctx, round, *messagesPtr, tools, response.Usage, state)
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

	// Every round in a row that the provider cut off at the completion cap.
	// A truncated *answer* already ends the turn in analyzeResponse, so a run
	// of these means the truncation keeps landing inside tool-call arguments:
	// act.go refuses the calls, the model is told to re-issue them, writes an
	// even longer call, and hits the cap again. Give up rather than spend the
	// whole round budget on it (#3446).
	if isLengthFinishReason(response.FinishReason) {
		guards.consecutiveLength++
		if guards.consecutiveLength >= maxConsecutiveLengthRounds {
			logger.Warnf(ctx, "[Agent][Round-%d] %d consecutive rounds cut off at the completion cap "+
				"(finish=%s, completion=%d of %d); stopping",
				round, guards.consecutiveLength, response.FinishReason,
				response.Usage.CompletionTokens, e.getCompletionTokenBudget())
			common.PipelineWarn(ctx, "Agent", "consecutive_length_stop", map[string]interface{}{
				"iteration":          state.CurrentRound,
				"round":              round,
				"consecutive_length": guards.consecutiveLength,
			})
			// Record the round the way every other round is recorded. The
			// refused tool calls are also what tells the UI that this round's
			// plain text was a preamble: without them the "let me look that
			// up" line stays sitting in the answer area, exactly as if it were
			// the answer.
			step := types.AgentStep{
				Iteration:        state.CurrentRound,
				Thought:          response.Content,
				ReasoningContent: response.ReasoningContent,
				ToolCalls:        make([]types.ToolCall, 0),
				Timestamp:        time.Now(),
				Truncated:        true,
			}
			if len(response.ToolCalls) > 0 {
				e.failTruncatedToolCalls(ctx, response, &step, state.CurrentRound, sessionID)
			}
			state.RoundSteps = append(state.RoundSteps, step)
			e.finishStalledTurn(ctx, state, sessionID, response, true)
			return iterOutcomeBreak, nil
		}
	} else {
		guards.consecutiveLength = 0
	}

	// Detect stuck loops: a round with no tool calls that repeats the previous
	// such round is not making progress. An identical *lack* of content counts
	// — a model burning its whole budget on reasoning returns empty every
	// round, which used to land in the else branch and reset the counter, so
	// the guard could never fire on the one shape that needs it most (#3446).
	if len(response.ToolCalls) == 0 {
		if guards.sawPlainRound && response.Content == guards.lastResponseContent {
			guards.consecutiveSameContent++
		} else {
			guards.consecutiveSameContent = 0
		}
		guards.sawPlainRound = true
		guards.lastResponseContent = response.Content
		if guards.consecutiveSameContent >= maxRepeatedResponseRounds {
			logger.Warnf(ctx, "[Agent][Round-%d] Detected stuck loop: same content repeated %d times (finish=%s), stopping",
				round, guards.consecutiveSameContent+1, response.FinishReason)
			e.finishStalledTurn(ctx, state, sessionID, response, false)
			return iterOutcomeBreak, nil
		}
	} else {
		guards.consecutiveSameContent = 0
		guards.sawPlainRound = false
		guards.lastResponseContent = ""
	}

	// Create agent step
	step := types.AgentStep{
		UserMessagesBefore: state.PendingSteerMessages,
		Iteration:          state.CurrentRound,
		Thought:            response.Content,
		ReasoningContent:   response.ReasoningContent,
		ToolCalls:          make([]types.ToolCall, 0),
		Timestamp:          time.Now(),
	}
	state.PendingSteerMessages = nil

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
		if step.Thought != "" || len(step.ToolCalls) > 0 || len(step.UserMessagesBefore) > 0 {
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
			guards.emptyRetries++
			if guards.emptyRetries <= maxEmptyResponseRetries {
				state.PendingSteerMessages = step.UserMessagesBefore
				logger.Warnf(ctx, "[Agent][Round-%d] Empty content with stop - retrying (%d/%d)",
					round, guards.emptyRetries, maxEmptyResponseRetries)
				*messagesPtr = append(*messagesPtr, chat.Message{
					Role:    "user",
					Content: "Please provide your complete answer now as plain text.",
				})
				return iterOutcomeContinue, nil
			}
			// Retries exhausted — use fallback message rather than empty answer.
			// analyzeResponse emitted nothing for the empty rounds (they were
			// retryable), so the fallback must be emitted here as the turn's
			// sole terminal answer event (#2906).
			logger.Warnf(ctx, "[Agent][Round-%d] Empty content after %d retries - using fallback",
				round, maxEmptyResponseRetries)
			fallback := stalledAnswerFallback
			answerID := generateEventID("answer")
			_ = e.eventBus.Emit(ctx, event.Event{
				ID:        answerID,
				Type:      event.EventAgentFinalAnswer,
				SessionID: sessionID,
				Data: event.AgentFinalAnswerData{
					Content: fallback,
					Done:    false,
				},
			})
			_ = e.eventBus.Emit(ctx, event.Event{
				ID:        answerID,
				Type:      event.EventAgentFinalAnswer,
				SessionID: sessionID,
				Data: event.AgentFinalAnswerData{
					Content: "",
					Done:    true,
				},
			})
			state.FinalAnswer = fallback
			state.IsComplete = true
			state.RoundSteps = append(state.RoundSteps, verdict.step)
			e.closeAnswerStream(ctx, sessionID, verdict.answerID, verdict.truncated)
			return iterOutcomeBreak, nil
		}
		// Loop-end inject: a user message queued while this finishing round
		// ran should keep the agent going instead of emitting a final answer.
		// Content-filter stops are terminal and do not take this path.
		// Past MaxIterations only the first inject gets an extra round;
		// anything after that stays in the queue for a follow-up turn.
		if response.FinishReason != "content_filter" {
			nextRound := state.CurrentRound + 1
			canContinue := e.withinIterationBudget(nextRound) || e.steerOverruns < maxSteerOverruns
			if canContinue {
				*messagesPtr = append(*messagesPtr, chat.Message{
					Role:             "assistant",
					Content:          verdict.finalAnswer,
					ReasoningContent: response.ReasoningContent,
				})
				injected := e.drainSteerMessages(ctx, state, messagesPtr, sessionID, assistantMessageID)
				if injected > 0 {
					verdict.step.IntermediateAnswer = true
					state.RoundSteps = append(state.RoundSteps, verdict.step)
					if !e.withinIterationBudget(nextRound) {
						e.steerOverruns++
						e.allowSteerOverrun = true
					}
					return iterOutcomeNext, nil
				}
			}
		}
		state.FinalAnswer = verdict.finalAnswer
		state.IsComplete = true
		state.RoundSteps = append(state.RoundSteps, verdict.step)
		e.closeAnswerStream(ctx, sessionID, verdict.answerID, verdict.truncated)
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
	e.intentGateIntent = newGateIntentSnapshot(query, *messagesPtr)
	e.executeToolCalls(ctx, response, &step, state.CurrentRound, sessionID, assistantMessageID)
	e.intentGateIntent = nil
	toolCallCount = len(step.ToolCalls)

	// 4. Observe: Add tool results to messages and write to context
	state.RoundSteps = append(state.RoundSteps, step)
	*messagesPtr = e.appendToolResults(*messagesPtr, step)
	*messagesPtr = e.appendToolImages(ctx, *messagesPtr, step)
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
		if desc = strings.TrimSpace(desc); desc != "" {
			descriptions = append(descriptions, desc)
		}
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
