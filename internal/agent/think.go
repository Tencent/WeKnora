package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/compaction"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// streamLLMResult holds accumulated output from a streaming LLM call.
type streamLLMResult struct {
	Content          string
	ReasoningContent string // accumulated reasoning content, kept separate from answer
	ToolCalls        []types.LLMToolCall
	Usage            *types.TokenUsage
	FinishReason     string // actual finish_reason from LLM (captured from last stream chunk)
	StreamError      string // error message from stream (e.g., timeout), kept separate from Content
}

// streamLLMToEventBus streams LLM response through EventBus (generic method)
// emitFunc: callback to emit each chunk event
func (e *AgentEngine) streamLLMToEventBus(
	ctx context.Context,
	messages []chat.Message,
	opts *chat.ChatOptions,
	emitFunc func(chunk *types.StreamResponse, fullContent string),
) (*streamLLMResult, error) {
	logger.Debugf(ctx, "[Agent][Stream] Starting LLM stream with %d messages", len(messages))

	// No wall-clock deadline here: a round that streams a large tool-call
	// payload (a whole file body inside write_sandbox_file) legitimately runs
	// for minutes while producing output the entire time. Cutting it off by
	// total elapsed time discards the half-assembled call. What must be caught
	// is a stream that stops producing, which the watchdog below handles.
	// The provider transport still applies its own overall ceiling.
	llmCtx, llmCancel := context.WithCancel(ctx)
	defer llmCancel()

	// Seeded before the request goes out so the watchdog also bounds
	// time-to-first-token, not just the gaps between chunks.
	var lastChunkAt atomic.Int64
	lastChunkAt.Store(time.Now().UnixNano())

	// Model-context encoding owns codec ordering and temporary-handle lifecycle.
	messages = e.modelContext.EncodeMessages(messages)
	prefixFingerprint := chat.PromptPrefixFingerprint(messages, opts)
	llmCtx = types.WithLLMCallMetadata(llmCtx, "agent_round", prefixFingerprint)
	stream, err := e.chatModel.ChatStream(llmCtx, messages, opts)
	if err != nil {
		logger.Errorf(ctx, "[Agent][Stream] Failed to start LLM stream: %v", err)
		return nil, err
	}

	result := &streamLLMResult{}
	chunkCount := 0
	responseTypeCounts := make(map[string]int)
	firstChunkTime := time.Time{}
	answerDecoder := e.modelContext.StreamDecoder()
	thinkingDecoder := e.modelContext.StreamDecoder()

	stallTimeout := e.getLLMStallTimeout()
	stalled, stopWatchdog := watchStreamStall(ctx, llmCancel, stallTimeout, &lastChunkAt)
	defer stopWatchdog()

	for chunk := range stream {
		lastChunkAt.Store(time.Now().UnixNano())
		chunkCount++
		if chunkCount == 1 {
			firstChunkTime = time.Now()
		}
		responseTypeCounts[string(chunk.ResponseType)]++

		// Capture error messages from the stream (e.g., "context deadline exceeded")
		// but do NOT append them to result.Content — they would leak to the user
		// as if they were part of the LLM answer. The tool calls and finish
		// reason riding along on the error chunk are what the provider had
		// assembled when the stream broke; keeping them lets the caller log and
		// reason about a partial call instead of seeing a clean empty response.
		if chunk.ResponseType == types.ResponseTypeError {
			result.StreamError = chunk.Content
			if len(chunk.ToolCalls) > 0 {
				result.ToolCalls = chunk.ToolCalls
			}
			if chunk.FinishReason != "" {
				result.FinishReason = chunk.FinishReason
			}
			continue
		}
		if chunk.ResponseType == types.ResponseTypeThinking {
			chunk.Content = thinkingDecoder.Feed(chunk.Content)
			if chunk.Done {
				chunk.Content += thinkingDecoder.Flush()
			}
		} else {
			chunk.Content = answerDecoder.Feed(chunk.Content)
			if chunk.Done {
				chunk.Content += answerDecoder.Flush()
			}
		}
		e.modelContext.DecodeToolCalls(chunk.ToolCalls)

		if chunk.Content != "" {
			isExtracted := chunk.Data != nil && chunk.Data["source"] != nil
			if !isExtracted {
				if chunk.ResponseType == types.ResponseTypeThinking {
					result.ReasoningContent += chunk.Content
				} else {
					result.Content += chunk.Content
				}
			}
		}

		if len(chunk.ToolCalls) > 0 {
			result.ToolCalls = chunk.ToolCalls
		}

		if chunk.Usage != nil {
			result.Usage = chunk.Usage
		}

		if chunk.FinishReason != "" {
			result.FinishReason = chunk.FinishReason
		}

		if emitFunc != nil {
			emitFunc(&chunk, result.Content)
		}
	}
	answerTail := answerDecoder.Flush()
	thinkingTail := thinkingDecoder.Flush()
	result.Content += answerTail
	result.ReasoningContent += thinkingTail
	if emitFunc != nil {
		if thinkingTail != "" {
			emitFunc(&types.StreamResponse{ResponseType: types.ResponseTypeThinking, Content: thinkingTail}, result.Content)
		}
		if answerTail != "" {
			emitFunc(&types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: answerTail}, result.Content)
		}
	}
	// Some providers stream tool-call argument fragments but expose the
	// accumulated call in the final chunk. Decode once more after assembly so
	// handles split across provider chunks cannot leak into tool execution.
	e.modelContext.DecodeToolCalls(result.ToolCalls)
	for _, toolCall := range result.ToolCalls {
		if len(toolCall.UnresolvedHandles) == 0 {
			continue
		}
		logger.Warnf(ctx,
			"[Agent][Stream] Tool %s (%s) contains unresolvable model handle(s): %v",
			toolCall.Function.Name, toolCall.ID, toolCall.UnresolvedHandles,
		)
	}
	if orphans := e.modelContext.OrphanResourceHandles(result.Content); len(orphans) > 0 {
		logger.Warnf(ctx, "[Agent][Stream] Model emitted %d unresolvable resource handle(s): %v",
			len(orphans), orphans)
	}

	// The watchdog cancels the provider context, so the stream surfaces a
	// generic cancellation. Restate it as a stall so the retry path and the
	// logs name the actual condition.
	if stalled.Load() {
		result.StreamError = fmt.Sprintf("LLM stream stalled: no output for %s", stallTimeout)
	}

	// Stream diagnostic summary: helps identify non-streaming patterns
	streamDuration := time.Duration(0)
	if !firstChunkTime.IsZero() {
		streamDuration = time.Since(firstChunkTime)
	}
	logger.Infof(ctx, "[Agent][Stream] Completed: chunks=%d, content_len=%d, tool_calls=%d, "+
		"stream_duration=%dms, type_distribution=%v",
		chunkCount, len(result.Content), len(result.ToolCalls),
		streamDuration.Milliseconds(), responseTypeCounts)

	// A stream that ended on an error never delivered a finish reason, so
	// everything collected is a partial response — a cut-off preamble, a
	// half-serialized tool call. Always surface it as a Go error so the caller
	// retries or degrades. Returning it as success once let a 238-character
	// preamble stand in as the final answer while a 30 KB write_sandbox_file
	// call was silently dropped.
	if result.StreamError != "" {
		return result, fmt.Errorf("LLM stream error: %s", result.StreamError)
	}

	return result, nil
}

// watchStreamStall cancels an LLM stream that stops producing output for
// stallTimeout. It deliberately does not bound total duration: a round that
// streams a large tool-call payload keeps producing for minutes and must be
// left alone. Returns the stall flag and a stop function the caller must defer.
func watchStreamStall(
	ctx context.Context,
	cancel context.CancelFunc,
	stallTimeout time.Duration,
	lastChunkAt *atomic.Int64,
) (*atomic.Bool, func()) {
	stalled := &atomic.Bool{}
	done := make(chan struct{})

	go func() {
		// Poll well inside the window so the detected gap stays close to the
		// configured timeout instead of rounding up to twice it.
		ticker := time.NewTicker(stallTimeout / 4)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				idle := time.Since(time.Unix(0, lastChunkAt.Load()))
				if idle < stallTimeout {
					continue
				}
				logger.Errorf(ctx,
					"[Agent][Stream] No output for %s (stall timeout %s); cancelling LLM stream",
					idle.Truncate(time.Second), stallTimeout)
				stalled.Store(true)
				cancel()
				return
			}
		}
	}()

	var once sync.Once
	return stalled, func() { once.Do(func() { close(done) }) }
}

// streamThinkingToEventBus streams the thinking process through EventBus
func (e *AgentEngine) streamThinkingToEventBus(
	ctx context.Context,
	messages []chat.Message,
	tools []chat.Tool,
	iteration int,
	sessionID string,
) (*types.ChatResponse, error) {
	budget := e.clampCompletionBudgetToContext(e.tokenEstimator.EstimateMessages(messages))
	logger.Debugf(ctx, "[Agent][Thinking] Iteration-%d: temp=%.2f, tools=%d, thinking=%v, max_tokens=%d",
		iteration+1, e.config.Temperature, len(tools), e.config.Thinking, budget)

	parallelToolCalls := true
	opts := &chat.ChatOptions{
		Temperature:         e.config.Temperature,
		MaxTokens:           budget,
		MaxCompletionTokens: budget,
		Tools:               tools,
		Thinking:            e.config.Thinking,
		ParallelToolCalls:   &parallelToolCalls,
		PromptCacheKey:      sessionID,
	}

	pendingToolCalls := make(map[string]bool)
	thinkingToolIDs := make(map[string]string) // tool_call_id -> event ID for thinking tool streams

	// Track which event types we emitted for diagnostics
	emittedEventTypes := make(map[string]int)

	// Generate IDs for this stream
	thinkingID := generateEventID("thinking")
	answerID := generateEventID("answer")

	// Routing state shared across chunk callbacks:
	//   - splitter separates inline <think>…</think> reasoning from answer text
	//     in the plain `content` channel (models that don't use reasoning_content).
	//   - thinkingOpen tracks whether the thought stream still needs a Done marker.
	//   - answerStreamed records that user-facing answer text was sent live to
	//     the final-answer area, so the natural-stop branch only emits Done.
	splitter := agenttools.NewThinkStreamSplitter()
	thinkingOpen := false
	answerStreamed := false

	emitThought := func(content string, done bool) {
		if content == "" && !done {
			return
		}
		emittedEventTypes["thought_chunk"]++
		e.eventBus.Emit(ctx, event.Event{
			ID:        thinkingID,
			Type:      event.EventAgentThought,
			SessionID: sessionID,
			Data: event.AgentThoughtData{
				Content:   content,
				Iteration: iteration,
				Done:      done,
			},
		})
	}
	// closeThinking emits the thought Done marker once, used right before the
	// first answer chunk so the UI flips the thinking card to "completed"
	// instead of leaving it spinning while the answer streams.
	closeThinking := func() {
		if thinkingOpen {
			emitThought("", true)
			thinkingOpen = false
		}
	}
	emitAnswer := func(content string) {
		if content == "" {
			return
		}
		// Suppress whitespace-only content emitted before the real answer has
		// started. OpenAI-compatible models frequently prepend a stray newline
		// (e.g. "\n\n") to the plain content channel in the same chunk where
		// they request tool calls. Routing that to the final-answer area leaks
		// spurious empty "answer" events interleaved with tool_call events.
		// Once genuine answer text has streamed (answerStreamed), preserve all
		// whitespace so the answer's own formatting stays intact.
		if !answerStreamed && strings.TrimSpace(content) == "" {
			return
		}
		closeThinking()
		answerStreamed = true
		emittedEventTypes["final_answer_chunk"]++
		e.eventBus.Emit(ctx, event.Event{
			ID:        answerID,
			Type:      event.EventAgentFinalAnswer,
			SessionID: sessionID,
			Data: event.AgentFinalAnswerData{
				Content: content,
				Done:    false,
			},
		})
	}

	llmResult, err := e.streamLLMToEventBus(
		ctx,
		messages,
		opts,
		func(chunk *types.StreamResponse, fullContent string) {
			if chunk.ResponseType == types.ResponseTypeToolCall && chunk.Data != nil {
				toolCallID, _ := chunk.Data["tool_call_id"].(string)
				toolName, _ := chunk.Data["tool_name"].(string)
				args, _ := chunk.Data["arguments"].(map[string]any)

				if toolCallID != "" && toolName != "" && !pendingToolCalls[toolCallID] {
					pendingToolCalls[toolCallID] = true
					emittedEventTypes["tool_call_pending"]++
					e.eventBus.Emit(ctx, event.Event{
						ID:        fmt.Sprintf("%s-tool-call-pending", toolCallID),
						Type:      event.EventAgentToolCall,
						SessionID: sessionID,
						Data: event.AgentToolCallData{
							ToolCallID: toolCallID,
							ToolName:   toolName,
							Arguments:  args,
							Iteration:  iteration,
						},
					})
				} else if toolCallID != "" && pendingToolCalls[toolCallID] && args != nil {
					emittedEventTypes["tool_call_progress"]++
					_ = e.eventBus.Emit(ctx, event.Event{
						ID:        fmt.Sprintf("%s-tool-call-progress", toolCallID),
						Type:      event.EventAgentToolCall,
						SessionID: sessionID,
						Data: event.AgentToolCallData{
							ToolCallID: toolCallID,
							ToolName:   toolName,
							Arguments:  args,
							Iteration:  iteration,
						},
					})
				}
			}

			// Handle thinking tool's streaming thought content
			if chunk.ResponseType == types.ResponseTypeThinking && chunk.Data != nil {
				if source, _ := chunk.Data["source"].(string); source == "thinking_tool" {
					toolCallID, _ := chunk.Data["tool_call_id"].(string)
					eventID, exists := thinkingToolIDs[toolCallID]
					if !exists {
						eventID = generateEventID("thinking-tool")
						thinkingToolIDs[toolCallID] = eventID
					}
					emittedEventTypes["thinking_tool_chunk"]++
					e.eventBus.Emit(ctx, event.Event{
						ID:        eventID,
						Type:      event.EventAgentThought,
						SessionID: sessionID,
						Data: event.AgentThoughtData{
							Content:   chunk.Content,
							Iteration: iteration,
							Done:      false,
						},
					})
					return
				}
			}

			// reasoning_content (separate thinking channel, e.g. DeepSeek V4) →
			// thought area. Forward the Done marker the provider sends when it
			// transitions from reasoning to answer.
			if chunk.ResponseType == types.ResponseTypeThinking {
				if chunk.Content != "" {
					thinkingOpen = true
					emitThought(chunk.Content, false)
				} else if chunk.Done && thinkingOpen {
					closeThinking()
				}
				return
			}

			// Plain content channel. Streamed live to the answer area
			// (optimistically rendered as the final answer). If the round turns
			// out to call tools, this was a preamble; the subsequent tool-call
			// events let the UI retract it from the answer area and relocate it
			// into the steps. Split out any inline <think> reasoning so it goes
			// to the thought area instead.
			if chunk.Content != "" {
				thinkPart, answerPart := splitter.Feed(chunk.Content)
				if thinkPart != "" {
					thinkingOpen = true
					emitThought(thinkPart, false)
				}
				emitAnswer(answerPart)
			}
			if chunk.Done {
				thinkPart, answerPart := splitter.Flush()
				if thinkPart != "" {
					thinkingOpen = true
					emitThought(thinkPart, false)
				}
				emitAnswer(answerPart)
				closeThinking()
			}
		},
	)
	if err != nil {
		logger.Errorf(ctx, "[Agent][Thinking] Iteration-%d failed: %v", iteration+1, err)
		return nil, err
	}

	// Emit diagnostics: helps identify when answer content went to "thought" vs "final_answer" events
	logger.Infof(ctx, "[Agent][Thinking] Iteration-%d completed: content=%d chars, tool_calls=%d, emitted_events=%v",
		iteration+1, len(llmResult.Content), len(llmResult.ToolCalls), emittedEventTypes)

	fullContent := agenttools.StripThinkBlocks(llmResult.Content)

	// Use actual finish_reason from LLM stream instead of hardcoding "stop".
	// Fallback to "stop" when the stream did not report a finish_reason
	// (e.g., certain Ollama models or providers that omit the field).
	finishReason := llmResult.FinishReason
	if finishReason == "" {
		finishReason = "stop"
	}

	resp := &types.ChatResponse{
		Content:          fullContent,
		ReasoningContent: llmResult.ReasoningContent,
		ToolCalls:        llmResult.ToolCalls,
		FinishReason:     finishReason,
		AnswerStreamed:   answerStreamed,
	}
	if answerStreamed {
		resp.AnswerEventID = answerID
	}
	if llmResult.Usage != nil {
		resp.Usage = *llmResult.Usage
	}
	return resp, nil
}

// callLLMWithRetry logs messages, sanitizes them, calls the LLM with retry on transient errors,
// and handles graceful degradation when prior tool results exist.
// Returns nil response (with state.IsComplete=true) when graceful degradation succeeds.
// Returns a non-nil error only when the call fails irrecoverably.
// callLLMWithRetry runs one ReAct round's LLM call with retry and graceful
// degradation. messagesPtr is a pointer because an overflow recovery compacts
// the history in place: the caller has to keep the compacted list, or the next
// round rebuilds the request that was just rejected.
func (e *AgentEngine) callLLMWithRetry(
	ctx context.Context, messagesPtr *[]chat.Message, tools []chat.Tool,
	state *types.AgentState, query string, iteration int, sessionID string,
) (*types.ChatResponse, error) {
	round := iteration + 1
	messages := *messagesPtr

	// Log message summary; only detail the tail messages to avoid repeating what prior rounds already logged
	const maxDetailMsgs = 4
	logger.Infof(ctx, "[Agent][Round-%d] Calling LLM: %d messages, %d tools",
		round, len(messages), len(tools))
	startIdx := 0
	if len(messages) > maxDetailMsgs {
		startIdx = len(messages) - maxDetailMsgs
		logger.Debugf(ctx, "[Agent][Round-%d] (skipping msg[0..%d], already logged in prior rounds)",
			round, startIdx-1)
	}
	for i := startIdx; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role == "tool" {
			logger.Debugf(ctx, "[Agent][Round-%d] msg[%d]: role=tool, name=%s, len=%d",
				round, i, msg.Name, len(msg.Content))
		} else if len(msg.ToolCalls) > 0 {
			tcNames := make([]string, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				tcNames[j] = tc.Function.Name
			}
			logger.Debugf(ctx, "[Agent][Round-%d] msg[%d]: role=%s, len=%d, tool_calls=%v",
				round, i, msg.Role, len(msg.Content), tcNames)
		} else {
			preview := msg.Content
			if len(preview) > 100 {
				preview = preview[:100] + "..."
			}
			logger.Debugf(ctx, "[Agent][Round-%d] msg[%d]: role=%s, len=%d, content=%s",
				round, i, msg.Role, len(msg.Content), preview)
		}
	}
	common.PipelineInfo(ctx, "Agent", "think_start", map[string]interface{}{
		"iteration": iteration,
		"round":     round,
		"tool_cnt":  len(tools),
	})

	// Sanitize messages before sending to LLM (fix consecutive roles, orphaned tool results)
	messages = agenttools.SanitizeMessages(messages)

	response, err := e.streamThinkingToEventBus(ctx, messages, tools, iteration, sessionID)

	// A rejected-for-size request is neither transient nor fatal, and reading
	// it as either is wrong in a specific way: retried unchanged it fails
	// identically every time, and surfaced as an error it ends a turn that a
	// compaction would have rescued. It gets its own one-shot recovery.
	if err != nil && !e.overflowRecovered && compaction.IsOverflowError(err) {
		e.overflowRecovered = true
		logger.Warnf(ctx, "[Agent][Round-%d] Provider rejected the request as too large; "+
			"compacting and retrying once: %v", round, err)
		// The engine's copy stays unsanitized. Sanitizing merges consecutive
		// same-role messages, and the summary is a `user` message that can end
		// up adjacent to a real one — merging them folds live conversation
		// into the summary envelope, where the next compaction cannot read it
		// back out. The provider gets the sanitized view; the engine keeps the
		// structured one.
		compacted := e.forceCompaction(ctx, messages, round)
		*messagesPtr = compacted
		messages = agenttools.SanitizeMessages(compacted)
		e.lastSentMsgCount = len(compacted)
		response, err = e.streamThinkingToEventBus(ctx, messages, tools, iteration, sessionID)
	}

	if err != nil && isTransientError(err) {
		// Retry transient errors (timeout, rate limit, server errors) up to maxLLMRetries times
		for retry := 1; retry <= maxLLMRetries; retry++ {
			retryDelay := time.Duration(retry) * time.Second
			logger.Warnf(ctx, "[Agent][Round-%d] LLM transient error (attempt %d/%d), retrying in %v: %v",
				round, retry, maxLLMRetries, retryDelay, err)
			time.Sleep(retryDelay)

			response, err = e.streamThinkingToEventBus(ctx, messages, tools, iteration, sessionID)
			if err == nil || !isTransientError(err) {
				break
			}
		}
	}
	if err != nil {
		logger.Errorf(ctx, "[Agent][Round-%d] LLM call failed: %v", round, err)
		common.PipelineError(ctx, "Agent", "think_failed", map[string]interface{}{
			"iteration": iteration,
			"error":     err.Error(),
		})

		// Graceful degradation: if we have tool results from previous rounds,
		// try to synthesize a final answer from them instead of losing everything.
		if totalTC := countTotalToolCalls(state.RoundSteps); totalTC > 0 {
			logger.Warnf(ctx, "[Agent] LLM failed but have %d steps with %d tool calls — "+
				"attempting final answer synthesis from existing results",
				len(state.RoundSteps), totalTC)
			common.PipelineWarn(ctx, "Agent", "llm_failed_synthesizing", map[string]interface{}{
				"steps":      len(state.RoundSteps),
				"tool_calls": totalTC,
			})
			if synthErr := e.streamFinalAnswerToEventBus(ctx, query, state, sessionID); synthErr != nil {
				logger.Errorf(ctx, "[Agent] Final answer synthesis also failed: %v", synthErr)
				return nil, fmt.Errorf("LLM call failed: %w (synthesis also failed: %v)", err, synthErr)
			}
			state.IsComplete = true
			return nil, nil // graceful degradation succeeded
		}

		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	common.PipelineInfo(ctx, "Agent", "think_result", map[string]interface{}{
		"iteration":     iteration,
		"finish_reason": response.FinishReason,
		"tool_calls":    len(response.ToolCalls),
		"content_len":   len(response.Content),
	})

	// Log LLM response summary
	if len(response.ToolCalls) > 0 {
		tcNames := make([]string, len(response.ToolCalls))
		for i, tc := range response.ToolCalls {
			tcNames[i] = tc.Function.Name
		}
		logger.Infof(ctx, "[Agent][Round-%d] LLM responded: finish=%s, content=%d chars, tools=%v",
			round, response.FinishReason, len(response.Content), tcNames)
	} else {
		logger.Infof(ctx, "[Agent][Round-%d] LLM responded: finish=%s, content=%d chars, tool_calls=0",
			round, response.FinishReason, len(response.Content))
		// Early signal for natural-stop path: this round will be analyzed as a
		// likely final answer (no tool call branch).
		if isNaturalStopFinishReason(response.FinishReason) {
			logger.Infof(ctx, "[Agent][Round-%d] Natural-stop candidate detected (finish=%s, tool_calls=0, content=%d chars)",
				round, response.FinishReason, len(response.Content))
		}
	}
	if response.Content != "" {
		preview := response.Content
		if len(preview) > 300 {
			preview = preview[:300] + "..."
		}
		logger.Debugf(ctx, "[Agent][Round-%d] LLM content preview:\n%s", round, preview)
	}

	return response, nil
}
