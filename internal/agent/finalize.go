package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

const maxFinalAnswerSegments = 4

const finalAnswerContinuationPrompt = "The previous assistant message was cut off by the output token limit. " +
	"Continue exactly where it stopped. Do not repeat or summarize earlier content. " +
	"Preserve the original language and structure, and finish the answer."

func finalAnswerImageRequirement(hasRetrievedImage bool) string {
	if !hasRetrievedImage {
		return ""
	}
	return `
5. Retrieved tool results contain Markdown images. Unless the user explicitly requested text-only output or every image is clearly unrelated, the final answer MUST include at least one relevant Markdown image copied verbatim from the tool results. Preserve its complete URL exactly. Use ASCII half-width parentheses exactly as ![alt](url) and never use full-width （ or ）. Place the image immediately after the paragraph it supports. When multiple images support different sections, distribute them across those sections instead of stopping after the first image.
6. Before finishing, silently verify that the answer contains a Markdown image when requirement 5 applies.`
}

// streamFinalAnswerToEventBus streams the final answer generation through EventBus
func (e *AgentEngine) streamFinalAnswerToEventBus(
	ctx context.Context,
	query string,
	state *types.AgentState,
	sessionID string,
) error {
	totalToolCalls := countTotalToolCalls(state.RoundSteps)
	logger.Infof(ctx, "[Agent][FinalAnswer] Synthesizing from %d steps, %d tool calls",
		len(state.RoundSteps), totalToolCalls)
	common.PipelineInfo(ctx, "Agent", "final_answer_start", map[string]interface{}{
		"session_id":   sessionID,
		"query":        query,
		"steps":        len(state.RoundSteps),
		"tool_results": totalToolCalls,
	})

	// Build messages with all context
	systemPrompt := e.buildSystemPrompt(ctx)
	userTurn := e.RenderUserTurnContent(sessionID, query)

	messages := []chat.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userTurn},
	}

	// Add all tool call results as context
	toolResultCount := 0
	hasRetrievedImage := false
	for stepIdx, step := range state.RoundSteps {
		for toolIdx, toolCall := range step.ToolCalls {
			toolResultCount++
			if searchutil.MarkdownImageRegex.MatchString(toolCall.Result.Output) {
				hasRetrievedImage = true
			}
			modelOutput := e.modelContext.ModelToolResultForTool(toolCall.Name, toolCall.Result)
			messages = append(messages, chat.Message{
				Role:    "user",
				Content: fmt.Sprintf("Tool %s returned: %s", toolCall.Name, modelOutput),
			})
			logger.Debugf(ctx, "[Agent][FinalAnswer] Added tool result [Step-%d][Tool-%d]: %s (output: %d chars)",
				stepIdx+1, toolIdx+1, toolCall.Name, len(toolCall.Result.Output))
		}
	}

	logger.Debugf(ctx, "[Agent][FinalAnswer] Built context: %d messages, %d tool results",
		len(messages), toolResultCount)

	imageRequirement := finalAnswerImageRequirement(hasRetrievedImage)

	// Add final answer prompt
	finalPrompt := fmt.Sprintf(`Based on the above tool call results, generate a complete answer for the user's question.

User question: %s

Requirements:
1. Answer based on the actually retrieved content
2. Organize the answer in a structured format
3. If information is insufficient, honestly state so
4. IMPORTANT: Respond in the same language as the user's question
%s

Now generate the final answer:`, query, imageRequirement)

	messages = append(messages, chat.Message{
		Role:    "user",
		Content: finalPrompt,
	})

	// Generate a single ID for this entire final answer stream
	answerID := generateEventID("answer")
	return e.streamAnswerSegments(ctx, messages, state, sessionID, answerID, "", maxFinalAnswerSegments)
}

// streamAnswerSegments keeps a truncated answer in one stream and one persisted
// value. Continuations have no tools: an output limit is not another ReAct step.
func (e *AgentEngine) streamAnswerSegments(
	ctx context.Context, messages []chat.Message, state *types.AgentState,
	sessionID, answerID, prefix string, maxSegments int,
) error {
	logger.Debugf(ctx, "[Agent][FinalAnswer] AnswerID: %s", answerID)
	fullAnswer := prefix
	finishReason := ""
	segments := 0
	thinking := false
	for segment := 0; segment < maxSegments; segment++ {
		if err := ctx.Err(); err != nil {
			state.FinalAnswer = fullAnswer
			return err
		}
		budget := e.clampCompletionBudgetToContext(e.tokenEstimator.EstimateMessages(messages))
		splitter := agenttools.NewThinkStreamSplitter()
		var segmentContent strings.Builder
		emitAnswer := func(content string) {
			if content == "" {
				return
			}
			segmentContent.WriteString(content)
			_ = e.eventBus.Emit(ctx, event.Event{
				ID: answerID, Type: event.EventAgentFinalAnswer, SessionID: sessionID,
				Data: event.AgentFinalAnswerData{Content: content, Done: false},
			})
		}
		llmResult, err := e.streamLLMToEventBus(
			ctx,
			messages,
			&chat.ChatOptions{
				Temperature:         e.config.Temperature,
				MaxCompletionTokens: budget,
				PromptCacheKey:      sessionID,
				Thinking:            &thinking,
			}, // Thinking disabled for final answer synthesis
			func(chunk *types.StreamResponse, _ string) {
				// Defensive filter: only emit answer content, skip thinking chunks.
				// The provider's Done marker closes one segment, not necessarily the
				// whole answer: length-limited segments are continued below.
				if chunk.ResponseType == types.ResponseTypeThinking || chunk.Content == "" {
					return
				}
				_, answer := splitter.Feed(chunk.Content)
				emitAnswer(answer)
			},
		)
		_, tail := splitter.Flush()
		emitAnswer(tail)
		fullAnswer += segmentContent.String()
		// Keep already generated text even when the provider fails mid-stream.
		if llmResult != nil && llmResult.Usage != nil {
			state.TurnUsage.Accumulate(*llmResult.Usage)
		}
		state.FinalAnswer = agenttools.StripThinkBlocks(fullAnswer)
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			logger.Errorf(ctx, "[Agent][FinalAnswer] Final answer generation failed: %v", err)
			common.PipelineError(ctx, "Agent", "final_answer_stream_failed", map[string]interface{}{
				"session_id": sessionID,
				"error":      err.Error(),
			})
			return err
		}

		segments++
		finishReason = llmResult.FinishReason

		// Preserve segment-edge whitespace until every continuation is joined.
		// StripThinkBlocks trims its input, so applying it per segment would turn
		// "first half " + "second half" into "first halfsecond half".
		segmentAnswer := segmentContent.String()
		if strings.TrimSpace(segmentAnswer) == "" {
			return fmt.Errorf("final answer generation returned no answer content (finish_reason=%s)", finishReason)
		}
		if !isLengthFinishReason(finishReason) {
			break
		}
		if segments >= maxSegments {
			logger.Warnf(ctx, "[Agent][FinalAnswer] Still length-limited after %d segments; stopping continuation",
				segments)
			common.PipelineWarn(ctx, "Agent", "final_answer_continuation_exhausted", map[string]interface{}{
				"session_id": sessionID,
				"segments":   segments,
			})
			return fmt.Errorf("answer reached the continuation limit; partial answer was preserved")
		}

		logger.Infof(ctx, "[Agent][FinalAnswer] Segment %d hit the completion-token cap; continuing", segments)
		if segmentAnswer != "" {
			messages = append(messages, chat.Message{Role: "assistant", Content: segmentAnswer})
		}
		messages = append(messages, chat.Message{Role: "user", Content: finalAnswerContinuationPrompt})
	}
	// Safety net: strip residual inline <think> blocks once, after segment
	// boundaries have been preserved.
	fullAnswer = agenttools.StripThinkBlocks(fullAnswer)

	// Close the single user-visible answer stream only after every continuation
	// segment has finished. Closing each provider segment made the UI persist a
	// partial answer before the continuation could arrive.
	_ = e.eventBus.Emit(ctx, event.Event{
		ID:        answerID,
		Type:      event.EventAgentFinalAnswer,
		SessionID: sessionID,
		Data: event.AgentFinalAnswerData{
			Content: "",
			Done:    true,
		},
	})

	logger.Infof(ctx, "[Agent][FinalAnswer] Final answer generated: %d characters", len(fullAnswer))
	common.PipelineInfo(ctx, "Agent", "final_answer_done", map[string]interface{}{
		"session_id":    sessionID,
		"answer_len":    len(fullAnswer),
		"segments":      segments,
		"finish_reason": finishReason,
	})
	state.FinalAnswer = fullAnswer
	return nil
}

// handleMaxIterations generates a final answer when the agent loop exhausted all iterations
// without the LLM producing a natural stop. It marks state.IsComplete = true.
func (e *AgentEngine) handleMaxIterations(
	ctx context.Context, query string, state *types.AgentState, sessionID string,
) {
	logger.Info(ctx, "Reached max iterations, generating final answer")
	common.PipelineWarn(ctx, "Agent", "max_iterations_reached", map[string]interface{}{
		"iterations": state.CurrentRound,
		"max":        e.config.MaxIterations,
	})

	// Stream final answer generation through EventBus
	if err := e.streamFinalAnswerToEventBus(ctx, query, state, sessionID); err != nil {
		logger.Errorf(ctx, "Failed to synthesize final answer: %v", err)
		common.PipelineError(ctx, "Agent", "final_answer_failed", map[string]interface{}{
			"error": err.Error(),
		})
		if state.FinalAnswer == "" {
			state.FinalAnswer = "Sorry, I was unable to generate a complete answer."
		}
	}
	state.IsComplete = true
}

// emitCompletionEvent emits the EventAgentComplete event with execution summary.
func (e *AgentEngine) emitCompletionEvent(
	ctx context.Context, state *types.AgentState, sessionID, messageID string, startTime time.Time,
) {
	// Convert knowledge refs to interface{} slice for event data
	knowledgeRefsInterface := make([]interface{}, 0, len(state.KnowledgeRefs))
	for _, ref := range state.KnowledgeRefs {
		knowledgeRefsInterface = append(knowledgeRefsInterface, ref)
	}

	e.eventBus.Emit(ctx, event.Event{
		ID:        generateEventID("complete"),
		Type:      event.EventAgentComplete,
		SessionID: sessionID,
		Data: event.AgentCompleteData{
			FinalAnswer:     state.FinalAnswer,
			KnowledgeRefs:   knowledgeRefsInterface,
			AgentSteps:      state.RoundSteps, // Include detailed execution steps for message storage
			Usage:           turnUsage(state),
			TotalSteps:      len(state.RoundSteps),
			TotalDurationMs: time.Since(startTime).Milliseconds(),
			MessageID:       messageID, // Include message ID for proper message update
		},
	})

	logger.Infof(ctx, "Agent execution completed in %d rounds", state.CurrentRound)
}

// turnUsage returns the turn's aggregated LLM usage, or nil when no round
// reported usage so the field stays absent from the completion event and the
// persisted message alike.
func turnUsage(state *types.AgentState) *types.TokenUsage {
	if state == nil || state.TurnUsage.TotalTokens == 0 {
		return nil
	}
	usage := state.TurnUsage
	return &usage
}
