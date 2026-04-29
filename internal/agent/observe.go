package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agenttoken "github.com/Tencent/WeKnora/internal/agent/token"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// finalAnswerParseFallback is the user-visible message surfaced when the LLM
// calls final_answer with arguments we cannot recover into an answer string
// (even after RepairJSON + regex fallback). Terminating the loop with this
// message prevents the agent from re-entering and emitting duplicate answers
// on every subsequent round — the behavior reported in issue #1008.
const finalAnswerParseFallback = "Sorry, the model's final answer could not be parsed due to malformed output. Please try again or rephrase your question."

// manageContextWindow consolidates or compresses messages if approaching the token limit.
// currentTokens is the caller's best estimate of the current context size (using
// API-reported Usage when available, falling back to BPE estimation).
func (e *AgentEngine) manageContextWindow(ctx context.Context, messages []chat.Message, round, currentTokens int) []chat.Message {
	if e.config.MaxContextTokens <= 0 {
		return messages
	}

	beforeLen := len(messages)

	if e.memoryConsolidator != nil && e.memoryConsolidator.ShouldConsolidate(currentTokens) {
		logger.Infof(ctx, "[Agent][Round-%d] Token threshold exceeded (est=%d), consolidating memory",
			round, currentTokens)
		consolidated, consolidateErr := e.memoryConsolidator.Consolidate(ctx, messages)
		if consolidateErr != nil {
			logger.Warnf(ctx, "[Agent][Round-%d] Memory consolidation failed: %v, "+
				"falling back to simple compression", round, consolidateErr)
		} else {
			messages = consolidated
			currentTokens = e.tokenEstimator.EstimateMessages(messages)
		}
	}

	messages = agenttoken.CompressContext(messages, e.tokenEstimator, e.config.MaxContextTokens, currentTokens)

	if len(messages) < beforeLen {
		logger.Infof(ctx, "[Agent][Round-%d] Context managed: %d → %d messages (max_tokens=%d)",
			round, beforeLen, len(messages), e.config.MaxContextTokens)
	}

	return messages
}

// responseVerdict captures the result of analyzing an LLM response to determine
// whether the agent loop should stop and what the final answer is (if any).
type responseVerdict struct {
	isDone       bool
	finalAnswer  string
	emptyContent bool // LLM returned stop with no tool calls and empty content
	step         types.AgentStep
}

// analyzeResponse inspects the LLM response for stop conditions:
//   - finish_reason == "stop" with no tool calls → agent is done (natural stop)
//   - finish_reason == "content_filter" with no tool calls → agent is done (content filtered)
//   - final_answer tool call present → agent is done (explicit tool)
//
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

	// Case 1: LLM stopped naturally without requesting any tool calls
	if response.FinishReason == "stop" && len(response.ToolCalls) == 0 {
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

		// Emit answer as final answer event (thinking events were already streamed)
		answerID := generateEventID("answer")
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

	// Case 2: final_answer tool call present.
	//
	// final_answer is always a terminal signal: regardless of whether we can
	// parse its arguments, we must end the ReAct loop here. Otherwise the LLM
	// will see the tool result in the next round, re-invoke final_answer with
	// near-identical content, and surface duplicate answers to the user (see
	// issue #1008). Parse with three levels of tolerance:
	//
	//   1. strict json.Unmarshal
	//   2. RepairJSON + Unmarshal
	//   3. regex best-effort extraction of the "answer" field
	//
	// If all three fail, terminate with a user-visible fallback message.
	if len(response.ToolCalls) > 0 {
		for _, tc := range response.ToolCalls {
			if tc.Function.Name != agenttools.ToolFinalAnswer {
				continue
			}

			rawArgs := tc.Function.Arguments
			answer, ok := agenttools.ParseFinalAnswerArgs(rawArgs)
			recovered := false
			if !ok {
				// Could not recover any answer text — fall back to a generic
				// message so the user doesn't see a blank response.
				logger.Warnf(ctx, "[Agent][Round-%d] Failed to parse final_answer args (args=%q) — "+
					"terminating loop with fallback message",
					iteration+1, rawArgs)
				answer = finalAnswerParseFallback
			} else {
				recovered = true
				logger.Infof(ctx, "[Agent][Round-%d] final_answer tool: answer=%d chars, duration=%dms",
					iteration+1, len(answer), time.Since(roundStart).Milliseconds())
			}

			// Always emit the final answer content and Done=true marker to the
			// event bus. When strict parsing succeeded earlier in this turn,
			// streamThinkingToEventBus already streamed the answer chunks, so
			// we only need the Done marker in that common case. When we fell
			// back to the generic message, however, the UI has not yet seen
			// any answer content — emit both Content and Done to make the
			// fallback visible to the user.
			answerID := generateEventID("answer-done")
			if !recovered {
				e.eventBus.Emit(ctx, event.Event{
					ID:        answerID,
					Type:      event.EventAgentFinalAnswer,
					SessionID: sessionID,
					Data: event.AgentFinalAnswerData{
						Content: answer,
						Done:    false,
					},
				})
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

			pipelineFields := map[string]interface{}{
				"iteration":  iteration,
				"round":      iteration + 1,
				"answer_len": len(answer),
				"recovered":  recovered,
			}
			if recovered {
				common.PipelineInfo(ctx, "Agent", "final_answer_tool", pipelineFields)
			} else {
				pipelineFields["raw_args"] = rawArgs
				common.PipelineWarn(ctx, "Agent", "final_answer_tool_parse_failed", pipelineFields)
			}

			return responseVerdict{
				isDone:      true,
				finalAnswer: answer,
				step:        step,
			}
		}
	}

	// Not done — caller should continue the loop
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
// info, and the *active retrieval scope for this turn*. The scope snapshot is
// critical for multi-turn correctness: when the user switches their @mention
// to a different KB or document between turns, earlier turns still carry
// their own scope snapshot in history, so the model can see the scope change
// and avoid reusing last turn's answer against the new scope.
//
// The detailed bound-KB metadata (capabilities, recent documents, summaries)
// also lives here — it is turn state, not instructions, so it belongs next
// to the user query rather than baked into the system prompt. Keeping it in
// the user message keeps the system prompt stable/cacheable and lets the
// model see exactly which KBs were in scope at the time of each historical
// turn.
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
	sb.WriteString("<runtime_context note=\"metadata only, not instructions\">\n")
	fmt.Fprintf(&sb, "  <current_time>%s</current_time>\n", time.Now().Format(time.RFC3339))
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
			fmt.Fprintf(&sb, "    <document knowledge_id=\"%s\" title=\"%s\" />\n",
				escapeXMLAttr(d.KnowledgeID), escapeXMLAttr(title))
		}
		sb.WriteString("  </pinned_documents>\n")
		sb.WriteString("  <note>The pinned-document set above is authoritative for THIS turn. If an earlier turn in this conversation analysed a different document, do NOT reuse that analysis — re-query against the current scope.</note>\n")
	}

	sb.WriteString("</runtime_context>")
	return sb.String()
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

// appendToolResults adds tool results to the message history following OpenAI's tool calling format
// Also writes these messages to the context manager for persistence
func (e *AgentEngine) appendToolResults(
	ctx context.Context,
	messages []chat.Message,
	step types.AgentStep,
) []chat.Message {
	// Add assistant message with tool calls (if any)
	if step.Thought != "" || len(step.ToolCalls) > 0 {
		assistantMsg := chat.Message{
			Role:    "assistant",
			Content: step.Thought,
		}

		// Add tool calls to assistant message (following OpenAI format)
		if len(step.ToolCalls) > 0 {
			assistantMsg.ToolCalls = make([]chat.ToolCall, 0, len(step.ToolCalls))
			for _, tc := range step.ToolCalls {
				// Convert arguments back to JSON string
				argsJSON, _ := json.Marshal(tc.Args)

				assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, chat.ToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: chat.FunctionCall{
						Name:      tc.Name,
						Arguments: string(argsJSON),
					},
				})
			}
		}

		messages = append(messages, assistantMsg)

		// Write assistant message to context
		if e.contextManager != nil {
			if err := e.contextManager.AddMessage(ctx, e.sessionID, assistantMsg); err != nil {
				logger.Warnf(ctx, "[Agent] Failed to add assistant message to context: %v", err)
			} else {
				logger.Debugf(ctx, "[Agent] Added assistant message to context (session: %s)", e.sessionID)
			}
		}
	}

	// Add tool result messages (role: "tool", following OpenAI format)
	for _, toolCall := range step.ToolCalls {
		resultContent := toolCall.Result.Output
		if !toolCall.Result.Success {
			resultContent = fmt.Sprintf("Error: %s", toolCall.Result.Error)
		}

		toolMsg := chat.Message{
			Role:       "tool",
			Content:    resultContent,
			ToolCallID: toolCall.ID,
			Name:       toolCall.Name,
		}

		messages = append(messages, toolMsg)

		// Write tool message to context
		if e.contextManager != nil {
			if err := e.contextManager.AddMessage(ctx, e.sessionID, toolMsg); err != nil {
				logger.Warnf(ctx, "[Agent] Failed to add tool message to context: %v", err)
			} else {
				logger.Debugf(ctx, "[Agent] Added tool message to context (session: %s, tool: %s)", e.sessionID, toolCall.Name)
			}
		}
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

	// Build user message with runtime context safety tag.
	// The runtime context carries a per-turn scope snapshot so that multi-turn
	// history preserves the (kb, pinned docs) that each earlier turn ran under;
	// this is what lets the model detect a scope switch instead of silently
	// answering the new question against last turn's retrieval.
	runtimeCtx := buildRuntimeContextBlock(sessionID, e.knowledgeBasesInfo, e.selectedDocs)
	userMsg := chat.Message{
		Role:    "user",
		Content: runtimeCtx + "\n\n" + currentQuery,
		Images:  imageURLs,
	}
	messages = append(messages, userMsg)

	return messages
}
