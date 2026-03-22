package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

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
	language := types.LanguageNameFromContext(ctx)
	systemPrompt := BuildSystemPromptWithOptions(
		e.knowledgeBasesInfo,
		e.config.WebSearchEnabled,
		e.selectedDocs,
		&BuildSystemPromptOptions{
			Language: language,
			Config:   e.appConfig,
		},
		e.systemPromptTemplate,
	)

	messages := []chat.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: query},
	}

	// Add all tool call results as context
	toolResultCount := 0
	for stepIdx, step := range state.RoundSteps {
		for toolIdx, toolCall := range step.ToolCalls {
			toolResultCount++
			messages = append(messages, chat.Message{
				Role:    "user",
				Content: fmt.Sprintf("Tool %s returned: %s", toolCall.Name, toolCall.Result.Output),
			})
			logger.Debugf(ctx, "[Agent][FinalAnswer] Added tool result [Step-%d][Tool-%d]: %s (output: %d chars)",
				stepIdx+1, toolIdx+1, toolCall.Name, len(toolCall.Result.Output))
		}
	}

	logger.Debugf(ctx, "[Agent][FinalAnswer] Built context: %d messages, %d tool results",
		len(messages), toolResultCount)

	// Add final answer prompt
	finalPrompt := fmt.Sprintf(`Based on the above tool call results, generate a complete answer for the user's question.

User question: %s

Requirements:
1. Answer based on the actually retrieved content
2. Clearly cite information sources (chunk_id, document name)
3. Organize the answer in a structured format
4. If information is insufficient, honestly state so
5. IMPORTANT: Respond in the same language as the user's question

Now generate the final answer:`, query)

	messages = append(messages, chat.Message{
		Role:    "user",
		Content: finalPrompt,
	})

	// Generate a single ID for this entire final answer stream
	answerID := generateEventID("answer")
	logger.Debugf(ctx, "[Agent][FinalAnswer] AnswerID: %s", answerID)

	llmResult, err := e.streamLLMToEventBus(
		ctx,
		messages,
		&chat.ChatOptions{Temperature: e.config.Temperature, Thinking: e.config.Thinking},
		func(chunk *types.StreamResponse, fullContent string) {
			if chunk.Content != "" {
				logger.Debugf(ctx, "[Agent][FinalAnswer] Emitting answer chunk: %d chars", len(chunk.Content))
				e.eventBus.Emit(ctx, event.Event{
					ID:        answerID,
					Type:      event.EventAgentFinalAnswer,
					SessionID: sessionID,
					Data: event.AgentFinalAnswerData{
						Content: chunk.Content,
						Done:    chunk.Done,
					},
				})
			}
		},
	)
	if err != nil {
		logger.Errorf(ctx, "[Agent][FinalAnswer] Final answer generation failed: %v", err)
		common.PipelineError(ctx, "Agent", "final_answer_stream_failed", map[string]interface{}{
			"session_id": sessionID,
			"error":      err.Error(),
		})
		return err
	}

	fullAnswer := llmResult.Content
	logger.Infof(ctx, "[Agent][FinalAnswer] Final answer generated: %d characters", len(fullAnswer))
	common.PipelineInfo(ctx, "Agent", "final_answer_done", map[string]interface{}{
		"session_id": sessionID,
		"answer_len": len(fullAnswer),
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
		state.FinalAnswer = "Sorry, I was unable to generate a complete answer."
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
			TotalSteps:      len(state.RoundSteps),
			TotalDurationMs: time.Since(startTime).Milliseconds(),
			MessageID:       messageID, // Include message ID for proper message update
		},
	})

	logger.Infof(ctx, "Agent execution completed in %d rounds", state.CurrentRound)
}
