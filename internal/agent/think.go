package agent

import (
	"context"
	"fmt"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// streamLLMResult holds accumulated output from a streaming LLM call.
type streamLLMResult struct {
	Content         string
	ThinkingContent string // accumulated thinking/reasoning content, kept separate from answer
	ToolCalls       []types.LLMToolCall
	Usage           *types.TokenUsage
	FinishReason    string // actual finish_reason from LLM (captured from last stream chunk)
	StreamError     string // error message from stream (e.g., timeout), kept separate from Content
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

	llmCtx, llmCancel := context.WithTimeout(ctx, e.getLLMCallTimeout())
	defer llmCancel()

	stream, err := e.chatModel.ChatStream(llmCtx, messages, opts)
	if err != nil {
		logger.Errorf(ctx, "[Agent][Stream] Failed to start LLM stream: %v", err)
		return nil, err
	}

	result := &streamLLMResult{}
	chunkCount := 0
	responseTypeCounts := make(map[string]int)
	firstChunkTime := time.Time{}

	for chunk := range stream {
		chunkCount++
		if chunkCount == 1 {
			firstChunkTime = time.Now()
		}
		responseTypeCounts[string(chunk.ResponseType)]++

		// Capture error messages from the stream (e.g., "context deadline exceeded")
		// but do NOT append them to result.Content — they would leak to the user
		// as if they were part of the LLM answer.
		if chunk.ResponseType == types.ResponseTypeError {
			result.StreamError = chunk.Content
			continue
		}

		if chunk.Content != "" {
			isExtracted := chunk.Data != nil && chunk.Data["source"] != nil
			if !isExtracted {
				if chunk.ResponseType == types.ResponseTypeThinking {
					result.ThinkingContent += chunk.Content
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

	// Stream diagnostic summary: helps identify non-streaming patterns
	streamDuration := time.Duration(0)
	if !firstChunkTime.IsZero() {
		streamDuration = time.Since(firstChunkTime)
	}
	logger.Infof(ctx, "[Agent][Stream] Completed: chunks=%d, content_len=%d, tool_calls=%d, "+
		"stream_duration=%dms, type_distribution=%v",
		chunkCount, len(result.Content), len(result.ToolCalls),
		streamDuration.Milliseconds(), responseTypeCounts)

	// If the stream produced an error and no usable content/tool calls,
	// surface it as a Go error so the caller can retry or degrade gracefully.
	if result.StreamError != "" && result.Content == "" && len(result.ToolCalls) == 0 {
		return result, fmt.Errorf("LLM stream error: %s", result.StreamError)
	}

	return result, nil
}

// streamThinkingToEventBus streams the thinking process through EventBus
func (e *AgentEngine) streamThinkingToEventBus(
	ctx context.Context,
	messages []chat.Message,
	tools []chat.Tool,
	iteration int,
	sessionID string,
) (*types.ChatResponse, error) {
	logger.Debugf(ctx, "[Agent][Thinking] Iteration-%d: temp=%.2f, tools=%d, thinking=%v",
		iteration+1, e.config.Temperature, len(tools), e.config.Thinking)

	parallelToolCalls := true
	opts := &chat.ChatOptions{
		Temperature:       e.config.Temperature,
		Tools:             tools,
		Thinking:          e.config.Thinking,
		ParallelToolCalls: &parallelToolCalls,
	}

	pendingToolCalls := make(map[string]bool)
	thinkingToolIDs := make(map[string]string) // tool_call_id -> event ID for thinking tool streams

	// Track which event types we emitted for diagnostics
	emittedEventTypes := make(map[string]int)

	// Generate IDs for this stream
	thinkingID := generateEventID("thinking")
	answerID := generateEventID("answer")

	llmResult, err := e.streamLLMToEventBus(
		ctx,
		messages,
		opts,
		func(chunk *types.StreamResponse, fullContent string) {
			if chunk.ResponseType == types.ResponseTypeToolCall && chunk.Data != nil {
				toolCallID, _ := chunk.Data["tool_call_id"].(string)
				toolName, _ := chunk.Data["tool_name"].(string)

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
							Iteration:  iteration,
						},
					})
				}
			}

			// Handle final_answer tool's streaming answer content
			if chunk.ResponseType == types.ResponseTypeAnswer {
				if source, _ := chunk.Data["source"].(string); source == "final_answer_tool" {
					emittedEventTypes["final_answer_chunk"]++
					e.eventBus.Emit(ctx, event.Event{
						ID:        answerID,
						Type:      event.EventAgentFinalAnswer,
						SessionID: sessionID,
						Data: event.AgentFinalAnswerData{
							Content: chunk.Content,
							Done:    false,
						},
					})
					return
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

			if chunk.Content != "" {
				emittedEventTypes["thought_chunk"]++
				e.eventBus.Emit(ctx, event.Event{
					ID:        thinkingID, // Same ID for all chunks in this stream
					Type:      event.EventAgentThought,
					SessionID: sessionID,
					Data: event.AgentThoughtData{
						Content:   chunk.Content,
						Iteration: iteration,
						Done:      chunk.Done,
					},
				})
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
		Content:      fullContent,
		ToolCalls:    llmResult.ToolCalls,
		FinishReason: finishReason,
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
func (e *AgentEngine) callLLMWithRetry(
	ctx context.Context, messages []chat.Message, tools []chat.Tool,
	state *types.AgentState, query string, iteration int, sessionID string,
) (*types.ChatResponse, error) {
	round := iteration + 1

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
		if response.FinishReason == "stop" {
			logger.Infof(ctx, "[Agent][Round-%d] Natural-stop candidate detected (finish=stop, tool_calls=0, content=%d chars)",
				round, len(response.Content))
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
