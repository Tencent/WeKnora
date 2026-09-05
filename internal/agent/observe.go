package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/compaction"
	agenttoken "github.com/Tencent/WeKnora/internal/agent/token"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	minToolResultTokens     = 8 * 1024
	maxToolResultTokens     = 32 * 1024
	toolResultTokenFraction = 5 // 20%

	// minFreedFraction is the reciprocal of the share of the context a
	// compaction must reclaim to count as having worked. Below it, the
	// summarization call costs more than the room it bought.
	minFreedFraction = 20 // 5%
)

// manageContextWindow summarizes older conversation away when the context has
// grown past the threshold. The bool reports whether the messages changed, so
// the caller knows when its token estimate is stale.
//
// currentTokens is the caller's best estimate of the current context size
// (API-reported Usage when available, BPE estimation of messages otherwise).
func (e *AgentEngine) manageContextWindow(
	ctx context.Context, messages []chat.Message, round, currentTokens int,
) ([]chat.Message, bool) {
	settings := e.compactor.Settings()
	if !settings.ShouldCompact(currentTokens) {
		return messages, false
	}

	logger.Infof(ctx, "[Agent][Round-%d] Context at %d tokens, over the %d threshold "+
		"(window=%d, reserved=%d, keep_recent=%d); compacting",
		round, currentTokens, settings.Threshold(), settings.MaxContextTokens,
		settings.ReserveTokens, settings.KeepRecentTokens)

	changed := false
	if compacted, ok := e.runCompaction(ctx, messages, round, compaction.ReasonThreshold); ok {
		messages, changed = compacted, true
		currentTokens = e.tokenEstimator.EstimateMessages(messages)
		if !settings.ShouldCompact(currentTokens) {
			return messages, true
		}
	}

	// Still over budget after compaction means the weight is inside the
	// keep-recent window, which the cut point cannot reach — one tool result
	// large enough to matter on its own. Trimming those is lossy, so it stays
	// a fallback rather than a routine step.
	trimmed, ok := e.trimToolResults(ctx, messages, round, settings)
	return trimmed, changed || ok
}

// runCompaction performs one compaction and reports whether the context
// actually got smaller. A false return means no further attempt this turn will
// help either, and the caller must not keep retrying: a compaction that frees
// nothing still costs a full summarization round-trip.
func (e *AgentEngine) runCompaction(
	ctx context.Context, messages []chat.Message, round int, reason compaction.Reason,
) ([]chat.Message, bool) {
	if e.compactor == nil {
		return messages, false
	}
	// The exhausted mark is tied to a message count rather than a bare flag:
	// once the loop appends new rounds there is new history to summarize, and
	// the earlier "nothing to compact" no longer describes the context.
	if e.compactionExhaustedAt > 0 && len(messages) <= e.compactionExhaustedAt {
		return messages, false
	}

	result, err := e.compactor.Compact(ctx, messages, reason)
	if err != nil {
		if errors.Is(err, compaction.ErrNothingToCompact) {
			logger.Infof(ctx, "[Agent][Round-%d] Nothing outside the keep-recent budget; "+
				"skipping compaction", round)
		} else {
			logger.Warnf(ctx, "[Agent][Round-%d] Compaction failed: %v", round, err)
		}
		e.compactionExhaustedAt = len(messages)
		return messages, false
	}
	// "Freed something" is too weak a test. A compaction that returns 240 of
	// 26,700 tokens counts as progress by that rule, so the loop keeps paying
	// for a summarization every round while the context stays where it was.
	if result.Freed() < result.TokensBefore/minFreedFraction {
		logger.Warnf(ctx, "[Agent][Round-%d] Compaction freed too little (%d → %d tokens); "+
			"not attempting again at this size", round, result.TokensBefore, result.TokensAfter)
		e.compactionExhaustedAt = len(messages)
		return messages, false
	}

	logger.Infof(ctx, "[Agent][Round-%d] Compacted (%s): %d → %d tokens, %d → %d messages "+
		"(split_turn=%v, degraded=%v)",
		round, result.Reason, result.TokensBefore, result.TokensAfter,
		result.MessagesBefore, result.MessagesAfter, result.SplitTurn, result.Degraded)
	// Where the surviving tokens went. If the retained tail is far larger than
	// keep_recent, the cut point could not reach past one oversized message.
	logger.Debugf(ctx, "[Agent][Round-%d][ctx] post-compaction: summary=%d tail=%d "+
		"(keep_recent=%d) | %s",
		round, e.tokenEstimator.EstimateString(result.Summary),
		result.TokensAfter-e.tokenEstimator.EstimateString(result.Summary),
		e.compactor.Settings().KeepRecentTokens,
		e.breakdownContext(result.Messages, nil))
	common.PipelineInfo(ctx, "Agent", "context_compacted", map[string]interface{}{
		"round":         round,
		"reason":        string(result.Reason),
		"tokens_before": result.TokensBefore,
		"tokens_after":  result.TokensAfter,
		"degraded":      result.Degraded,
	})
	e.emitContextCompacted(ctx, result, round)

	// The usage baseline described the pre-compaction context; keeping it
	// would have the next round estimate against history that no longer
	// exists and compact again immediately.
	e.lastUsage = types.TokenUsage{}
	e.lastSentMsgCount = 0

	return result.Messages, true
}

func (e *AgentEngine) emitContextCompacted(
	ctx context.Context, result *compaction.Result, round int,
) {
	_ = e.eventBus.Emit(ctx, event.Event{
		ID:        generateEventID("compaction"),
		Type:      event.EventContextCompacted,
		SessionID: e.sessionID,
		Data: event.ContextCompactedData{
			Reason:         string(result.Reason),
			Round:          round,
			TokensBefore:   result.TokensBefore,
			TokensAfter:    result.TokensAfter,
			MessagesBefore: result.MessagesBefore,
			MessagesAfter:  result.MessagesAfter,
			Summary:        result.Summary,
			Degraded:       result.Degraded,
			SplitTurn:      result.SplitTurn,
		},
	})
}

// responseHitContextLimit reports whether the response was shaped by a full
// context window rather than by the completion budget we asked for.
func (e *AgentEngine) responseHitContextLimit(response *types.ChatResponse) bool {
	window := 0
	if e.config != nil {
		window = e.config.MaxContextTokens
	}
	return compaction.ResponseHitContextLimit(response, window, e.getCompletionTokenBudget())
}

// forceCompaction compacts regardless of the threshold, for the case where the
// provider has already told us the window is full and the estimate that let us
// get here is the thing not to be trusted.
func (e *AgentEngine) forceCompaction(
	ctx context.Context, messages []chat.Message, round int,
) []chat.Message {
	compacted, ok := e.runCompaction(ctx, messages, round, compaction.ReasonOverflow)
	if !ok {
		trimmed, _ := e.trimToolResults(ctx, messages, round, e.compactor.Settings())
		return trimmed
	}
	return compacted
}

// trimToolResults replaces tool output with previews until it fits a fraction
// of the window. It is the last resort: unlike compaction, what it removes is
// gone without a summary standing in for it.
func (e *AgentEngine) trimToolResults(
	ctx context.Context, messages []chat.Message, round int, settings compaction.Settings,
) ([]chat.Message, bool) {
	trimmed, ok := trimToolResultsToBudget(
		messages, e.tokenEstimator, toolResultBudget(settings.MaxContextTokens),
	)
	if !ok {
		return messages, false
	}
	logger.Infof(ctx, "[Agent][Round-%d] Trimmed tool results to the token budget", round)
	return trimmed, true
}

func toolResultBudget(maxContextTokens int) int {
	if maxContextTokens <= 0 {
		return maxToolResultTokens
	}
	budget := maxContextTokens / toolResultTokenFraction
	if budget < minToolResultTokens {
		return minToolResultTokens
	}
	if budget > maxToolResultTokens {
		return maxToolResultTokens
	}
	return budget
}

// trimToolResultsToBudget returns a message copy for the next model call.
// It never mutates ToolResult objects used by SSE, diagnostics, or persistence.
// Assistant tool-call messages remain untouched so every trimmed tool result
// retains its provider-required call/result pairing.
//
// Every tool result is a candidate. Scoping this to the current turn was how
// the old implementation defined "recent", but after compaction the retained
// window is all recent by construction, and the one result big enough to
// require trimming is as likely to sit at its head as its tail.
func trimToolResultsToBudget(
	messages []chat.Message,
	estimator *agenttoken.Estimator,
	budget int,
) ([]chat.Message, bool) {
	if estimator == nil || budget <= 0 || len(messages) == 0 {
		return messages, false
	}

	var toolIndexes []int
	total := 0
	for i := range messages {
		if messages[i].Role == "tool" {
			toolIndexes = append(toolIndexes, i)
			total += estimator.EstimateMessage(&messages[i])
		}
	}
	if total <= budget || len(toolIndexes) == 0 {
		return messages, false
	}

	out := append([]chat.Message(nil), messages...)
	baseCosts := make(map[int]int, len(toolIndexes))
	remaining := budget
	for _, idx := range toolIndexes {
		out[idx].Content = compactedToolResultMarker(messages[idx].Content)
		cost := estimator.EstimateMessage(&out[idx])
		baseCosts[idx] = cost
		remaining -= cost
	}
	if remaining < 0 {
		remaining = 0
	}

	// Spend the remaining budget newest-first. A result that cannot fit in
	// full receives the largest head/tail preview that does fit.
	for i := len(toolIndexes) - 1; i >= 0; i-- {
		idx := toolIndexes[i]
		fullCost := estimator.EstimateMessage(&messages[idx])
		extra := fullCost - baseCosts[idx]
		if extra <= remaining {
			out[idx] = messages[idx]
			remaining -= extra
			continue
		}
		out[idx] = compactToolMessage(messages[idx], baseCosts[idx]+remaining, estimator)
		remaining = 0
	}
	return out, true
}

func compactedToolResultMarker(content string) string {
	return fmt.Sprintf(
		"[Tool result compacted: original_bytes=%d. Re-run the tool with narrower filters or a smaller range if more detail is needed.]",
		len(content),
	)
}

func compactToolMessage(msg chat.Message, maxTokens int, estimator *agenttoken.Estimator) chat.Message {
	runes := []rune(msg.Content)
	base := msg
	base.Content = compactedToolResultMarker(msg.Content)
	if len(runes) == 0 || estimator.EstimateMessage(&base) >= maxTokens {
		return base
	}

	best := base
	low, high := 1, len(runes)
	for low <= high {
		keep := low + (high-low)/2
		head := keep / 4
		tail := keep - head
		candidate := base
		candidate.Content = fmt.Sprintf(
			"%s\n\n%s\n...[tool result preview omitted]...\n%s",
			base.Content,
			string(runes[:head]),
			string(runes[len(runes)-tail:]),
		)
		if estimator.EstimateMessage(&candidate) <= maxTokens {
			best = candidate
			low = keep + 1
		} else {
			high = keep - 1
		}
	}
	return best
}

// responseVerdict captures the result of analyzing an LLM response to determine
// whether the agent loop should stop and what the final answer is (if any).
type responseVerdict struct {
	isDone       bool
	finalAnswer  string
	emptyContent bool // LLM returned stop with no tool calls and empty content
	step         types.AgentStep
}

// isNaturalStopFinishReason reports whether a provider finish reason means the
// assistant has ended its message without requesting more tool work.
func isNaturalStopFinishReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "stop", "end_turn", "stop_sequence":
		return true
	default:
		return false
	}
}

// isLengthFinishReason reports whether the provider stopped because the
// completion-token cap was hit. Truncated tool-call JSON then fails
// validation (missing path, unexpected end of JSON, etc.).
func isLengthFinishReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "length", "max_tokens", "max_output_tokens":
		return true
	default:
		return false
	}
}

// analyzeResponse inspects the LLM response for stop conditions:
//   - natural finish reason with no tool calls → agent is done (natural stop)
//   - finish_reason == "content_filter" with no tool calls → agent is done (content filtered)
//
// The agent ends a turn by stopping naturally with its answer as plain
// assistant text (there is no dedicated final_answer tool). Any round that
// still requests tool calls is non-terminal and the caller continues the loop.
// It returns a responseVerdict. If isDone is true the caller should break out of the loop.
func (e *AgentEngine) analyzeResponse(
	ctx context.Context, response *types.ChatResponse,
	step types.AgentStep, iteration int, sessionID string, roundStart time.Time,
) responseVerdict {
	// Case 0: Content was blocked by the model's content filter.
	// Treat this as a terminal condition to avoid an infinite loop where
	// the same filtered response accumulates in the context.
	if response.FinishReason == "content_filter" && len(response.ToolCalls) == 0 {
		logger.Warnf(ctx, "[Agent][Round-%d] Content filter triggered, stopping agent loop (content=%d chars)",
			iteration+1, len(response.Content))
		common.PipelineWarn(ctx, "Agent", "content_filter_stop", map[string]interface{}{
			"iteration":   iteration,
			"round":       iteration + 1,
			"content_len": len(response.Content),
		})

		answer := response.Content
		if answer == "" {
			answer = "Sorry, this request was blocked by the content safety policy. Please try rephrasing your question."
		}

		answerID := generateEventID("answer")
		e.eventBus.Emit(ctx, event.Event{
			ID:        answerID,
			Type:      event.EventAgentFinalAnswer,
			SessionID: sessionID,
			Data: event.AgentFinalAnswerData{
				Content: answer,
				Done:    false,
			},
		})
		e.eventBus.Emit(ctx, event.Event{
			ID:        answerID,
			Type:      event.EventAgentFinalAnswer,
			SessionID: sessionID,
			Data: event.AgentFinalAnswerData{
				Content: "",
				Done:    true,
			},
		})

		return responseVerdict{
			isDone:      true,
			finalAnswer: answer,
			step:        step,
		}
	}

	// Case 1: LLM stopped naturally without requesting any tool calls.
	if isNaturalStopFinishReason(response.FinishReason) && len(response.ToolCalls) == 0 {
		// Strip <think>…</think> blocks that some models embed in content
		// (DeepSeek, Qwen, etc.) before processing or displaying.
		response.Content = agenttools.StripThinkBlocks(response.Content)
		logger.Infof(ctx, "[Agent][Round-%d] Agent finished naturally: answer=%d chars, duration=%dms",
			iteration+1, len(response.Content), time.Since(roundStart).Milliseconds())
		common.PipelineInfo(ctx, "Agent", "round_final_answer", map[string]interface{}{
			"iteration":  iteration,
			"round":      iteration + 1,
			"answer_len": len(response.Content),
		})

		// Emit the final answer. The answer text reaches the UI by one of two
		// paths:
		//   (a) Already streamed live during the think phase — the common case
		//       now that plain assistant content is routed straight to
		//       EventAgentFinalAnswer (response.AnswerStreamed). Re-emitting the
		//       full content here would render it twice and produce the
		//       end-of-stream "jump from Thinking to Answer" the user reported,
		//       so we only close the existing stream with a Done marker on the
		//       same event ID.
		//   (b) Not streamed live (e.g. the content only surfaced in the
		//       accumulated result) — emit the full content, then Done.
		var answerID string
		if response.AnswerStreamed && response.AnswerEventID != "" {
			answerID = response.AnswerEventID
		} else {
			answerID = generateEventID("answer")
			if response.Content != "" {
				e.eventBus.Emit(ctx, event.Event{
					ID:        answerID,
					Type:      event.EventAgentFinalAnswer,
					SessionID: sessionID,
					Data: event.AgentFinalAnswerData{
						Content: response.Content,
						Done:    false,
					},
				})
			}
		}
		e.eventBus.Emit(ctx, event.Event{
			ID:        answerID,
			Type:      event.EventAgentFinalAnswer,
			SessionID: sessionID,
			Data: event.AgentFinalAnswerData{
				Content: "",
				Done:    true,
			},
		})

		return responseVerdict{
			isDone:       true,
			finalAnswer:  response.Content,
			emptyContent: response.Content == "",
			step:         step,
		}
	}

	// Any round that still requests tool calls is non-terminal: the caller
	// executes the tools and loops again. The agent only ends by stopping
	// naturally (Case 1) with its answer as plain assistant text.
	return responseVerdict{isDone: false, step: step}
}

// indentLines prefixes every line of s with indent. Used to nest pre-rendered
// XML blocks inside the `<runtime_context>` envelope without losing readability.
func indentLines(s, indent string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}

// escapeXMLAttr escapes a string for safe inclusion in an XML attribute value.
// Titles and names may contain user-supplied characters like <, >, &, ".
func escapeXMLAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// buildRuntimeContextBlock builds a metadata block with current time, session
// info, and the *active retrieval scope for this turn only*. It is injected
// into the current user message for the LLM call and is not persisted into
// conversation history — replayed user turns keep bare Content so stale scope
// snapshots do not steer follow-up questions.
//
// Per-turn communication_instruction and answer_instruction remind the model
// not to leak internal tool names or IDs in user-visible text, and to end the
// turn by writing its complete answer as plain assistant text.
//
// Emitted as an XML-ish block (not free prose) so it is a visually distinct,
// non-instruction envelope that is hard to conflate with user text and
// prompt-injection-safe.
func buildRuntimeContextBlock(
	sessionID string,
	kbs []*KnowledgeBaseInfo,
	docs []*SelectedDocumentInfo,
) string {
	var sb strings.Builder
	sb.WriteString("<runtime_context scope=\"this_turn\">\n")
	fmt.Fprintf(&sb, "  <current_time>%s</current_time>\n", time.Now().Format("2006-01-02"))
	fmt.Fprintf(&sb, "  <session>%s</session>\n", escapeXMLAttr(sessionID))

	if len(kbs) > 0 {
		// Render the full bound-KB detail (capabilities + recent docs) so the
		// model has everything it needs to route its retrieval in one place.
		// `formatKnowledgeBaseList` already emits a `<knowledge_bases>…</knowledge_bases>`
		// envelope; we wrap it in `<bound_knowledge_bases>` to make the scope
		// semantics explicit and to match the naming the prompt templates use
		// when referring back to this block.
		sb.WriteString("  <bound_knowledge_bases>\n")
		sb.WriteString(indentLines(formatKnowledgeBaseList(kbs), "    "))
		sb.WriteString("\n  </bound_knowledge_bases>\n")
	}

	if len(docs) > 0 {
		sb.WriteString("  <pinned_documents scope=\"authoritative_for_this_turn\">\n")
		for _, d := range docs {
			if d == nil {
				continue
			}
			title := d.Title
			if title == "" {
				title = d.FileName
			}
			if title == "" {
				title = d.KnowledgeID
			}
			if d.FileType != "" {
				fmt.Fprintf(&sb, "    <document knowledge_id=\"%s\" title=\"%s\" file_type=\"%s\" />\n",
					escapeXMLAttr(d.KnowledgeID), escapeXMLAttr(title), escapeXMLAttr(d.FileType))
			} else {
				fmt.Fprintf(&sb, "    <document knowledge_id=\"%s\" title=\"%s\" />\n",
					escapeXMLAttr(d.KnowledgeID), escapeXMLAttr(title))
			}
		}
		sb.WriteString("  </pinned_documents>\n")
		sb.WriteString("  <note>The pinned-document set above is authoritative for THIS turn. ")
		sb.WriteString("Prioritize retrieving content from these documents (e.g. list_knowledge_chunks with the knowledge_id). ")
		sb.WriteString("If an earlier turn analysed a different document, do NOT reuse that analysis — re-query against the current scope.</note>\n")
	}

	sb.WriteString("  <communication_instruction>Do not use internal tool names or identifiers in your answers or in Thought. Say \"keyword retrieval\" instead of grep_chunks, \"semantic retrieval\" instead of knowledge_search, \"browse full document\" instead of list_knowledge_chunks; likewise never expose chunk_id, knowledge_id, or other internal IDs—refer to documents by title or name.</communication_instruction>\n")
	sb.WriteString("  <answer_instruction>When you have gathered enough information, write your complete user-facing answer as your reply and stop—do not request any more tools in that final message. Until then, keep using tools; do not give a partial answer mid-investigation.</answer_instruction>\n")

	sb.WriteString("</runtime_context>")
	return sb.String()
}

// buildMustUseBlock emits a short per-turn hint when the user @mentioned MCP/Skill.
// Tool names are not listed here — they are already in the function-calling schema.
func buildMustUseBlock(mcpServices []*PinnedMCPServiceInfo, skills []*PinnedSkillInfo) string {
	var lines []string
	for _, svc := range mcpServices {
		if svc == nil {
			continue
		}
		prefix := mcpToolNamePrefix(svc)
		if prefix == "" {
			continue
		}
		display := sanitizeMustUseField(svc.Name)
		if display == "" {
			display = sanitizeMustUseField(svc.ID)
		}
		lines = append(lines, fmt.Sprintf("Must use MCP tools whose names start with %s (@%s) to answer the question below.", prefix, display))
	}
	for _, skill := range skills {
		if skill == nil || skill.Name == "" {
			continue
		}
		name := sanitizeMustUseField(skill.Name)
		lines = append(lines, fmt.Sprintf("Must call read_file(path=%q) for @Skill %q before answering.", "skill://"+name+"/SKILL.md", name))
	}
	if len(lines) == 0 {
		return ""
	}
	return "<must_use>\n" + strings.Join(lines, "\n") +
		"\nThese selections do not replace research into the task's factual content or exclude other relevant available sources unless the user explicitly restricts them.\n</must_use>"
}

// sanitizeMustUseField strips newlines and angle brackets so an MCP/skill name
// cannot break out of the <must_use> block or inject extra instruction lines.
func sanitizeMustUseField(s string) string {
	replacer := strings.NewReplacer("\n", " ", "\r", " ", "<", " ", ">", " ")
	return strings.TrimSpace(replacer.Replace(s))
}

// mcpToolNamePrefix returns the shared prefix for an MCP service's registered
// tools (e.g. mcp_iwiki_ from mcp_iwiki_getdocument). Tool names are
// mcp_{sanitized_service_name}_{tool}, and the service slug itself may contain
// underscores (sanitizeName turns spaces/hyphens into "_"), so we take the
// longest common prefix across the service's tools and trim it back to the last
// segment boundary instead of naively cutting at the first underscore.
func mcpToolNamePrefix(svc *PinnedMCPServiceInfo) string {
	if svc == nil || len(svc.ToolNames) == 0 {
		return ""
	}
	const head = "mcp_"
	var mcpNames []string
	for _, toolName := range svc.ToolNames {
		if strings.HasPrefix(toolName, head) {
			mcpNames = append(mcpNames, toolName)
		}
	}
	if len(mcpNames) == 0 {
		return ""
	}
	prefix := mcpNames[0]
	for _, name := range mcpNames[1:] {
		prefix = commonStringPrefix(prefix, name)
	}
	// Trim to the last underscore so the hint names the service prefix
	// (mcp_{service}_) rather than a partial tool name.
	if idx := strings.LastIndex(prefix, "_"); idx >= len(head)-1 {
		prefix = prefix[:idx+1]
	}
	if len(prefix) <= len(head) {
		return ""
	}
	return prefix
}

func commonStringPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}

// RenderUserTurnContent builds the user-turn payload for the current LLM call
// (runtime_context + must_use + query). Used by Execute and finalize paths only;
// not written to rendered_content / history.
func (e *AgentEngine) RenderUserTurnContent(sessionID, query string) string {
	e.registerRuntimeReferences()
	runtimeCtx := buildRuntimeContextBlock(sessionID, e.knowledgeBasesInfo, e.selectedDocs)
	runtimeCtx = e.modelContext.CompactKnownText(runtimeCtx)
	mustUse := buildMustUseBlock(e.pinnedMCPServices, e.pinnedSkills)
	return composeUserTurnContent(runtimeCtx, mustUse, query)
}

// registerRuntimeReferences makes bound KBs, pinned documents and recent
// chunks addressable without exposing their durable IDs to the model.
func (e *AgentEngine) registerRuntimeReferences() {
	if e == nil || e.modelContext == nil {
		return
	}
	for _, kb := range e.knowledgeBasesInfo {
		if kb == nil {
			continue
		}
		e.modelContext.RegisterKnowledgeBase(kb.ID)
		for _, doc := range kb.RecentDocs {
			e.modelContext.RegisterDocument(doc.KnowledgeID)
			if doc.ChunkID != "" {
				title := doc.Title
				if title == "" {
					title = doc.FileName
				}
				e.modelContext.RegisterChunk(modelcontext.ChunkReference{
					ChunkID:         doc.ChunkID,
					KnowledgeID:     doc.KnowledgeID,
					KnowledgeBaseID: firstNonEmptyAgent(doc.KnowledgeBaseID, kb.ID),
					DocumentTitle:   title,
					ChunkType:       doc.Type,
				})
			}
		}
	}
	for _, doc := range e.selectedDocs {
		if doc == nil {
			continue
		}
		e.modelContext.RegisterDocument(doc.KnowledgeID)
		e.modelContext.RegisterKnowledgeBase(doc.KnowledgeBaseID)
	}
}

func firstNonEmptyAgent(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func composeUserTurnContent(parts ...string) string {
	nonEmpty := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	return strings.Join(nonEmpty, "\n\n")
}

// listToolNames returns tool.function names for logging
func listToolNames(ts []chat.Tool) []string {
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		names = append(names, t.Function.Name)
	}
	return names
}

// buildToolsForLLM builds the tools list for LLM function calling
func (e *AgentEngine) buildToolsForLLM() []chat.Tool {
	functionDefs := e.toolRegistry.GetFunctionDefinitions()
	tools := make([]chat.Tool, 0, len(functionDefs))
	for _, def := range functionDefs {
		tools = append(tools, chat.Tool{
			Type: "function",
			Function: chat.FunctionDef{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.Parameters,
			},
		})
	}

	return tools
}

// appendToolResults adds tool results to the in-turn message history following
// OpenAI's tool-calling format. Cross-turn persistence is handled separately:
// the final AgentSteps are written to the assistant message by the SSE handler,
// and rebuilt from DB on the next turn by service.LoadAgentHistory.
func (e *AgentEngine) appendToolResults(
	messages []chat.Message,
	step types.AgentStep,
) []chat.Message {
	// Add assistant message with tool calls (if any)
	if step.Thought != "" || len(step.ToolCalls) > 0 || step.ReasoningContent != "" {
		assistantMsg := chat.Message{
			Role:             "assistant",
			Content:          step.Thought,
			ReasoningContent: step.ReasoningContent,
		}

		// Add tool calls to assistant message (following OpenAI format)
		if len(step.ToolCalls) > 0 {
			assistantMsg.ToolCalls = make([]chat.ToolCall, 0, len(step.ToolCalls))
			for _, tc := range step.ToolCalls {
				// Convert arguments back to JSON string
				argsJSON, _ := json.Marshal(tc.Args)

				assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, chat.ToolCall{
					ID:               tc.ID,
					Type:             "function",
					ProviderMetadata: tc.ProviderMetadata,
					Function: chat.FunctionCall{
						Name:      tc.Name,
						Arguments: string(argsJSON),
					},
				})
			}
		}

		messages = append(messages, assistantMsg)
	}

	// Add tool result messages (role: "tool", following OpenAI format)
	for _, toolCall := range step.ToolCalls {
		resultContent := e.modelContext.ModelToolResultForTool(toolCall.Name, toolCall.Result)

		toolMsg := chat.Message{
			Role:       "tool",
			Content:    resultContent,
			ToolCallID: toolCall.ID,
			Name:       toolCall.Name,
		}

		messages = append(messages, toolMsg)
	}

	if stepContainsMarkdownImage(step) {
		// Keep the requirement at the end of the current prefix. Editing the
		// system prompt would invalidate provider prefix cache for tools and
		// the whole transcript on every later round of this turn.
		messages = appendAgentRetrievedImageRequirement(messages)
	}

	return messages
}

// countTotalToolCalls counts total tool calls across all steps
func countTotalToolCalls(steps []types.AgentStep) int {
	total := 0
	for _, step := range steps {
		total += len(step.ToolCalls)
	}
	return total
}

// kbToolNames lists tools whose results contain knowledge base content that
// may become stale across turns (KB can be switched, updated, or deleted).
// Historical results from these tools are redacted to force fresh retrieval.
var kbToolNames = map[string]bool{
	agenttools.ToolKnowledgeSearch:     true,
	agenttools.ToolGrepChunks:          true,
	agenttools.ToolListKnowledgeChunks: true,
	agenttools.ToolQueryKnowledgeGraph: true,
	agenttools.ToolGetDocumentInfo:     true,
	agenttools.ToolWikiSearch:          true,
	agenttools.ToolWikiReadPage:        true,
	agenttools.ToolWikiReadSourceDoc:   true,
}

// redactHistoryKBResults replaces full KB tool results in historical context
// with brief markers. This prevents the LLM from reusing stale retrieval data
// when the knowledge base has been modified or switched between turns.
func redactHistoryKBResults(llmContext []chat.Message) []chat.Message {
	redacted := make([]chat.Message, 0, len(llmContext))
	for _, msg := range llmContext {
		if msg.Role == "tool" && kbToolNames[msg.Name] {
			redacted = append(redacted, chat.Message{
				Role:       msg.Role,
				Content:    "[Previous retrieval result omitted — knowledge base may have changed. Please perform a fresh search.]",
				ToolCallID: msg.ToolCallID,
				Name:       msg.Name,
			})
		} else {
			redacted = append(redacted, msg)
		}
	}
	return redacted
}

// buildMessagesWithLLMContext builds the message array with LLM context
func (e *AgentEngine) buildMessagesWithLLMContext(
	systemPrompt, currentQuery, sessionID string,
	llmContext []chat.Message,
	imageURLs []string,
) []chat.Message {
	messages := []chat.Message{
		{Role: "system", Content: systemPrompt},
	}

	if len(llmContext) > 0 {
		var sanitized []chat.Message
		if e.config.RetainRetrievalHistory {
			sanitized = llmContext
			logger.Infof(context.Background(), "Retaining full retrieval history in context (RetainRetrievalHistory=true)")
		} else {
			// Redact KB tool results from previous turns to prevent the LLM
			// from reusing stale retrieval data when the KB has been modified.
			sanitized = redactHistoryKBResults(llmContext)
			logger.Infof(context.Background(), "Added %d history messages to context (KB tool results redacted)", len(llmContext))
		}

		for _, msg := range sanitized {
			if msg.Role == "system" {
				continue
			}
			if msg.Role == "user" || msg.Role == "assistant" || msg.Role == "tool" {
				messages = append(messages, msg)
			}
		}
	}

	// Build the current user message through the same registration path used by
	// final synthesis. Calling buildRuntimeContextBlock directly here would put
	// durable bound-KB/document IDs into the first model request before the
	// request-local source registry had seen them.
	userMsg := chat.Message{
		Role:    "user",
		Content: e.RenderUserTurnContent(sessionID, currentQuery),
		Images:  imageURLs,
	}
	messages = append(messages, userMsg)

	return messages
}
