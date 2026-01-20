package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// generateEventID generates a unique event ID with type suffix for better traceability
func generateEventID(suffix string) string {
	return fmt.Sprintf("%s-%s", uuid.New().String()[:8], suffix)
}

// AgentEngine is the core engine for running ReAct agents
type AgentEngine struct {
	config               *types.AgentConfig
	toolRegistry         *tools.ToolRegistry
	chatModel            chat.Chat
	eventBus             *event.EventBus
	knowledgeBasesInfo   []*KnowledgeBaseInfo      // Detailed knowledge base information for prompt
	selectedDocs         []*SelectedDocumentInfo   // User-selected documents (via @ mention)
	contextManager       interfaces.ContextManager // Context manager for writing agent conversation to LLM context
	sessionID            string                    // Session ID for context management
	systemPromptTemplate string                    // System prompt template (optional, uses default if empty)
}

// listToolNames returns tool.function names for logging
func listToolNames(ts []chat.Tool) []string {
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		names = append(names, t.Function.Name)
	}
	return names
}

// NewAgentEngine creates a new agent engine
func NewAgentEngine(
	config *types.AgentConfig,
	chatModel chat.Chat,
	toolRegistry *tools.ToolRegistry,
	eventBus *event.EventBus,
	knowledgeBasesInfo []*KnowledgeBaseInfo,
	selectedDocs []*SelectedDocumentInfo,
	contextManager interfaces.ContextManager,
	sessionID string,
	systemPromptTemplate string,
) *AgentEngine {
	if eventBus == nil {
		eventBus = event.NewEventBus()
	}
	return &AgentEngine{
		config:               config,
		toolRegistry:         toolRegistry,
		chatModel:            chatModel,
		eventBus:             eventBus,
		knowledgeBasesInfo:   knowledgeBasesInfo,
		selectedDocs:         selectedDocs,
		contextManager:       contextManager,
		sessionID:            sessionID,
		systemPromptTemplate: systemPromptTemplate,
	}
}

// Execute executes the agent with conversation history and streaming output
// All events are emitted to EventBus and handled by subscribers (like Handler layer)
func (e *AgentEngine) Execute(
	ctx context.Context,
	sessionID, messageID, query string,
	llmContext []chat.Message,
) (*types.AgentState, error) {
	logger.Infof(ctx, "========== Agent Execution Started ==========")
	// Ensure tools are cleaned up after execution
	defer e.toolRegistry.Cleanup(ctx)

	logger.Infof(ctx, "[Agent] SessionID: %s, MessageID: %s", sessionID, messageID)
	logger.Infof(ctx, "[Agent] User Query: %s", query)
	logger.Infof(ctx, "[Agent] LLM Context Messages: %d", len(llmContext))
	common.PipelineInfo(ctx, "Agent", "execute_start", map[string]interface{}{
		"session_id":   sessionID,
		"message_id":   messageID,
		"query":        query,
		"context_msgs": len(llmContext),
	})

	// Initialize state
	state := &types.AgentState{
		RoundSteps:    []types.AgentStep{},
		KnowledgeRefs: []*types.SearchResult{},
		IsComplete:    false,
		CurrentRound:  0,
	}

	// Build system prompt using progressive RAG prompt
	systemPrompt := BuildSystemPrompt(
		e.knowledgeBasesInfo,
		e.config.WebSearchEnabled,
		e.selectedDocs,
		e.systemPromptTemplate,
	)
	logger.Debugf(ctx, "[Agent] SystemPrompt Length: %d characters", len(systemPrompt))
	logger.Debugf(ctx, "[Agent] SystemPrompt (stream)\n----\n%s\n----", systemPrompt)

	// Initialize messages with history
	messages := e.buildMessagesWithLLMContext(systemPrompt, query, llmContext)
	logger.Infof(ctx, "[Agent] Total messages for LLM: %d (system: 1, history: %d, user query: 1)",
		len(messages), len(llmContext))

	// Get tool definitions for function calling
	tools := e.buildToolsForLLM()
	toolListStr := strings.Join(listToolNames(tools), ", ")
	logger.Infof(ctx, "[Agent] Tools enabled (%d): %s", len(tools), toolListStr)
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
		return nil, err
	}

	logger.Infof(ctx, "========== Agent Execution Completed Successfully ==========")
	logger.Infof(ctx, "[Agent] Total rounds: %d, Round steps: %d, Is complete: %v",
		state.CurrentRound, len(state.RoundSteps), state.IsComplete)
	common.PipelineInfo(ctx, "Agent", "execute_complete", map[string]interface{}{
		"session_id": sessionID,
		"rounds":     state.CurrentRound,
		"steps":      len(state.RoundSteps),
		"complete":   state.IsComplete,
	})
	return state, nil
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
	for state.CurrentRound < e.config.MaxIterations {
		roundStart := time.Now()
		logger.Infof(ctx, "========== Round %d/%d Started ==========", state.CurrentRound+1, e.config.MaxIterations)
		logger.Infof(ctx, "[Agent][Round-%d] Message history size: %d messages", state.CurrentRound+1, len(messages))
		common.PipelineInfo(ctx, "Agent", "round_start", map[string]interface{}{
			"iteration":      state.CurrentRound,
			"round":          state.CurrentRound + 1,
			"message_count":  len(messages),
			"pending_tools":  len(tools),
			"max_iterations": e.config.MaxIterations,
		})

		// 1. Think: Call LLM with function calling and stream thinking through EventBus
		logger.Infof(ctx, "[Agent][Round-%d] Calling LLM with %d tools available...", state.CurrentRound+1, len(tools))
		common.PipelineInfo(ctx, "Agent", "think_start", map[string]interface{}{
			"iteration": state.CurrentRound,
			"round":     state.CurrentRound + 1,
			"tool_cnt":  len(tools),
		})
		response, err := e.streamThinkingToEventBus(ctx, messages, tools, state.CurrentRound, sessionID)
		if err != nil {
			logger.Errorf(ctx, "[Agent][Round-%d] LLM call failed: %v", state.CurrentRound+1, err)
			common.PipelineError(ctx, "Agent", "think_failed", map[string]interface{}{
				"iteration": state.CurrentRound,
				"error":     err.Error(),
			})
			return state, fmt.Errorf("LLM call failed: %w", err)
		}

		common.PipelineInfo(ctx, "Agent", "think_result", map[string]interface{}{
			"iteration":     state.CurrentRound,
			"finish_reason": response.FinishReason,
			"tool_calls":    len(response.ToolCalls),
			"content_len":   len(response.Content),
		})

		// Debug: log finish reason and tool call count from LLM
		logger.Infof(ctx, "[Agent][Round-%d] LLM response received: finish_reason=%s, tool_calls=%d, content_length=%d",
			state.CurrentRound+1, response.FinishReason, len(response.ToolCalls), len(response.Content))
		logger.Debugf(
			ctx,
			"[Agent] LLM response finish=%s, toolCalls=%d",
			response.FinishReason,
			len(response.ToolCalls),
		)
		if response.Content != "" {
			logger.Debugf(ctx, "[Agent][Round-%d] LLM thought content:\n%s", state.CurrentRound+1, response.Content)
		}

		// Create agent step
		step := types.AgentStep{
			Iteration: state.CurrentRound,
			Thought:   response.Content,
			ToolCalls: make([]types.ToolCall, 0),
			Timestamp: time.Now(),
		}

		// 2. Check finish reason - if stop and no tool calls, agent is done
		if response.FinishReason == "stop" && len(response.ToolCalls) == 0 {
			logger.Infof(ctx, "[Agent][Round-%d] Agent finished - no more tool calls needed", state.CurrentRound+1)
			logger.Infof(ctx, "[Agent] Final answer length: %d characters", len(response.Content))
			common.PipelineInfo(ctx, "Agent", "round_final_answer", map[string]interface{}{
				"iteration":  state.CurrentRound,
				"round":      state.CurrentRound + 1,
				"answer_len": len(response.Content),
			})
			state.FinalAnswer = response.Content
			state.IsComplete = true
			state.RoundSteps = append(state.RoundSteps, step)

			// Emit final answer done marker
			e.eventBus.Emit(ctx, event.Event{
				ID:        generateEventID("answer-done"),
				Type:      event.EventAgentFinalAnswer,
				SessionID: sessionID,
				Data: event.AgentFinalAnswerData{
					Content: "",
					Done:    true,
				},
			})
			logger.Infof(
				ctx,
				"[Agent][Round-%d] Duration: %dms",
				state.CurrentRound+1,
				time.Since(roundStart).Milliseconds(),
			)
			break
		}

		// 3. Act: Execute tool calls if any
		if len(response.ToolCalls) > 0 {
			logger.Infof(
				ctx,
				"[Agent][Round-%d] Executing %d tool calls...",
				state.CurrentRound+1,
				len(response.ToolCalls),
			)

			for i, tc := range response.ToolCalls {
				logger.Infof(ctx, "[Agent][Round-%d][Tool-%d/%d] Tool: %s, ID: %s",
					state.CurrentRound+1, i+1, len(response.ToolCalls), tc.Function.Name, tc.ID)

				var args map[string]any
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					logger.Errorf(ctx, "[Agent][Round-%d][Tool-%d/%d] Failed to parse tool arguments: %v",
						state.CurrentRound+1, i+1, len(response.ToolCalls), err)
					continue
				}

				// Log the arguments in a readable format
				argsJSON, _ := json.MarshalIndent(args, "", "  ")
				logger.Infof(ctx, "[Agent][Round-%d][Tool-%d/%d] Arguments:\n%s",
					state.CurrentRound+1, i+1, len(response.ToolCalls), string(argsJSON))

				toolCallStartTime := time.Now()
				e.eventBus.Emit(ctx, event.Event{
					ID:        tc.ID + "-tool-call",
					Type:      event.EventAgentToolCall,
					SessionID: sessionID,
					Data: event.AgentToolCallData{
						ToolCallID: tc.ID,
						ToolName:   tc.Function.Name,
						Arguments:  args,
						Iteration:  state.CurrentRound,
					},
				})
				logger.Debugf(ctx, "[Agent] ToolCall -> %s args=%s", tc.Function.Name, tc.Function.Arguments)

				// Execute tool
				logger.Infof(ctx, "[Agent][Round-%d][Tool-%d/%d] Executing tool: %s...",
					state.CurrentRound+1, i+1, len(response.ToolCalls), tc.Function.Name)
				common.PipelineInfo(ctx, "Agent", "tool_call_start", map[string]interface{}{
					"iteration":    state.CurrentRound,
					"round":        state.CurrentRound + 1,
					"tool":         tc.Function.Name,
					"tool_call_id": tc.ID,
					"tool_index":   fmt.Sprintf("%d/%d", i+1, len(response.ToolCalls)),
				})
				result, err := e.toolRegistry.ExecuteTool(ctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
				duration := time.Since(toolCallStartTime).Milliseconds()
				logger.Infof(ctx, "[Agent][Round-%d][Tool-%d/%d] Tool execution completed in %dms",
					state.CurrentRound+1, i+1, len(response.ToolCalls), duration)

				toolCall := types.ToolCall{
					ID:       tc.ID,
					Name:     tc.Function.Name,
					Args:     args,
					Result:   result,
					Duration: duration,
				}

				if err != nil {
					logger.Errorf(ctx, "[Agent][Round-%d][Tool-%d/%d] Tool call failed: %s, error: %v",
						state.CurrentRound+1, i+1, len(response.ToolCalls), tc.Function.Name, err)
					toolCall.Result = &types.ToolResult{
						Success: false,
						Error:   err.Error(),
					}
				}

				toolSuccess := toolCall.Result != nil && toolCall.Result.Success
				pipelineFields := map[string]interface{}{
					"iteration":    state.CurrentRound,
					"round":        state.CurrentRound + 1,
					"tool":         tc.Function.Name,
					"tool_call_id": tc.ID,
					"duration_ms":  duration,
					"success":      toolSuccess,
				}
				if toolCall.Result != nil && toolCall.Result.Error != "" {
					pipelineFields["error"] = toolCall.Result.Error
				}
				if err != nil {
					common.PipelineError(ctx, "Agent", "tool_call_result", pipelineFields)
				} else if toolSuccess {
					common.PipelineInfo(ctx, "Agent", "tool_call_result", pipelineFields)
				} else {
					common.PipelineWarn(ctx, "Agent", "tool_call_result", pipelineFields)
				}

				if toolCall.Result != nil {
					logger.Infof(ctx, "[Agent][Round-%d][Tool-%d/%d] Tool result: success=%v, output_length=%d",
						state.CurrentRound+1, i+1, len(response.ToolCalls),
						toolCall.Result.Success, len(toolCall.Result.Output))
					logger.Debugf(ctx, "[Agent] ToolResult <- %s success=%v len(output)=%d",
						tc.Function.Name, toolCall.Result.Success, len(toolCall.Result.Output))

					// Log the output content for debugging
					if toolCall.Result.Output != "" {
						// Truncate if too long for logging
						outputPreview := toolCall.Result.Output
						if len(outputPreview) > 500 {
							outputPreview = outputPreview[:500] + "... (truncated)"
						}
						logger.Debugf(ctx, "[Agent][Round-%d][Tool-%d/%d] Tool output preview:\n%s",
							state.CurrentRound+1, i+1, len(response.ToolCalls), outputPreview)
					}

					if toolCall.Result.Error != "" {
						logger.Warnf(ctx, "[Agent][Round-%d][Tool-%d/%d] Tool error: %s",
							state.CurrentRound+1, i+1, len(response.ToolCalls), toolCall.Result.Error)
					}

					// Log structured data if present
					if toolCall.Result.Data != nil {
						dataJSON, _ := json.MarshalIndent(toolCall.Result.Data, "", "  ")
						logger.Debugf(ctx, "[Agent][Round-%d][Tool-%d/%d] Tool data:\n%s",
							state.CurrentRound+1, i+1, len(response.ToolCalls), string(dataJSON))
					}
				}

				// Store tool call (Observations are now derived from ToolCall.Result.Output)
				step.ToolCalls = append(step.ToolCalls, toolCall)

				// Emit tool result event (include structured data from tool result)
				e.eventBus.Emit(ctx, event.Event{
					ID:        tc.ID + "-tool-result",
					Type:      event.EventAgentToolResult,
					SessionID: sessionID,
					Data: event.AgentToolResultData{
						ToolCallID: tc.ID,
						ToolName:   tc.Function.Name,
						Output:     result.Output,
						Error:      result.Error,
						Success:    result.Success,
						Duration:   duration,
						Iteration:  state.CurrentRound,
						Data:       result.Data, // Pass structured data for frontend rendering
					},
				})

				// Emit tool execution event (for internal monitoring)
				e.eventBus.Emit(ctx, event.Event{
					ID:        tc.ID + "-tool-exec",
					Type:      event.EventAgentTool,
					SessionID: sessionID,
					Data: event.AgentActionData{
						Iteration:  state.CurrentRound,
						ToolName:   tc.Function.Name,
						ToolInput:  args,
						ToolOutput: result.Output,
						Success:    result.Success,
						Error:      result.Error,
						Duration:   duration,
					},
				})

				// Optional: Reflection after each tool call (streaming)
				if e.config.ReflectionEnabled && result != nil {
					reflection, err := e.streamReflectionToEventBus(
						ctx, tc.ID, tc.Function.Name, result.Output,
						state.CurrentRound, sessionID,
					)
					if err != nil {
						logger.Warnf(ctx, "Reflection failed: %v", err)
					} else if reflection != "" {
						// Store reflection in the corresponding tool call
						// Find the tool call we just added and update it
						if len(step.ToolCalls) > 0 {
							lastIdx := len(step.ToolCalls) - 1
							step.ToolCalls[lastIdx].Reflection = reflection
						}
					}
				}
			}
		}

		state.RoundSteps = append(state.RoundSteps, step)
		// 4. Observe: Add tool results to messages and write to context
		messages = e.appendToolResults(ctx, messages, step)
		common.PipelineInfo(ctx, "Agent", "round_end", map[string]interface{}{
			"iteration":   state.CurrentRound,
			"round":       state.CurrentRound + 1,
			"tool_calls":  len(step.ToolCalls),
			"thought_len": len(step.Thought),
		})
		// 5. Check if we should continue
		state.CurrentRound++
	}

	// If loop finished without final answer, generate one
	if !state.IsComplete {
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
			state.FinalAnswer = "抱歉，我无法生成完整的答案。"
		}
		state.IsComplete = true
	}

	// Emit completion event
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
	return state, nil
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

// streamLLMToEventBus streams LLM response through EventBus (generic method)
// emitFunc: callback to emit each chunk event
// Returns: full accumulated content, tool calls (if any), error
func (e *AgentEngine) streamLLMToEventBus(
	ctx context.Context,
	messages []chat.Message,
	opts *chat.ChatOptions,
	emitFunc func(chunk *types.StreamResponse, fullContent string),
) (string, []types.LLMToolCall, error) {
	logger.Debugf(ctx, "[Agent][Stream] Starting LLM stream with %d messages", len(messages))

	stream, err := e.chatModel.ChatStream(ctx, messages, opts)
	if err != nil {
		logger.Errorf(ctx, "[Agent][Stream] Failed to start LLM stream: %v", err)
		return "", nil, err
	}

	fullContent := ""
	var toolCalls []types.LLMToolCall
	chunkCount := 0

	for chunk := range stream {
		chunkCount++

		if chunk.Content != "" {
			fullContent += chunk.Content
		}

		// Collect tool calls if present
		if len(chunk.ToolCalls) > 0 {
			toolCalls = chunk.ToolCalls
		}

		// Emit event through callback
		if emitFunc != nil {
			emitFunc(&chunk, fullContent)
		}
	}

	return fullContent, toolCalls, nil
}

// streamReflectionToEventBus streams reflection process through EventBus
// Note: Reflection is now handled through the think tool in main loop
func (e *AgentEngine) streamReflectionToEventBus(
	ctx context.Context,
	toolCallID string,
	toolName string,
	result string,
	iteration int,
	sessionID string,
) (string, error) {
	// Simplified reflection without BuildReflectionPrompt
	reflectionPrompt := fmt.Sprintf(`请评估刚才调用工具 %s 的结果，并决定下一步行动。

工具返回: %s

思考:
1. 结果是否满足需求？
2. 下一步应该做什么？`, toolName, result)

	messages := []chat.Message{
		{Role: "user", Content: reflectionPrompt},
	}

	// Generate a single ID for this entire reflection stream
	reflectionID := generateEventID("reflection")

	fullReflection, _, err := e.streamLLMToEventBus(
		ctx,
		messages,
		&chat.ChatOptions{Temperature: 0.5},
		func(chunk *types.StreamResponse, fullContent string) {
			if chunk.Content != "" {
				e.eventBus.Emit(ctx, event.Event{
					ID:        reflectionID, // Same ID for all chunks in this stream
					Type:      event.EventAgentReflection,
					SessionID: sessionID,
					Data: event.AgentReflectionData{
						ToolCallID: toolCallID,
						Content:    chunk.Content,
						Iteration:  iteration,
						Done:       chunk.Done,
					},
				})
			}
		},
	)
	if err != nil {
		logger.Warnf(ctx, "Reflection failed: %v", err)
		return "", err
	}

	return fullReflection, nil
}

// streamThinkingToEventBus streams the thinking process through EventBus
func (e *AgentEngine) streamThinkingToEventBus(
	ctx context.Context,
	messages []chat.Message,
	tools []chat.Tool,
	iteration int,
	sessionID string,
) (*types.ChatResponse, error) {
	logger.Infof(ctx, "[Agent][Thinking][Iteration-%d] Starting thinking stream with temperature=%.2f, tools=%d, thinking=%v",
		iteration+1, e.config.Temperature, len(tools), e.config.Thinking)

	opts := &chat.ChatOptions{
		Temperature: e.config.Temperature,
		Tools:       tools,
		Thinking:    e.config.Thinking,
	}
	logger.Debug(context.Background(), "[Agent] streamLLM opts tool_choice=auto temperature=", e.config.Temperature)

	pendingToolCalls := make(map[string]bool)

	// Generate a single ID for this entire thinking stream
	thinkingID := generateEventID("thinking")
	logger.Debugf(ctx, "[Agent][Thinking][Iteration-%d] ThinkingID: %s", iteration+1, thinkingID)

	fullContent, toolCalls, err := e.streamLLMToEventBus(
		ctx,
		messages,
		opts,
		func(chunk *types.StreamResponse, fullContent string) {
			if chunk.ResponseType == types.ResponseTypeToolCall && chunk.Data != nil {
				toolCallID, _ := chunk.Data["tool_call_id"].(string)
				toolName, _ := chunk.Data["tool_name"].(string)

				if toolCallID != "" && toolName != "" && !pendingToolCalls[toolCallID] {
					pendingToolCalls[toolCallID] = true
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

			if chunk.Content != "" {
				// logger.Debugf(ctx, "[Agent][Thinking][Iteration-%d] Emitting thought chunk: %d chars",
				// 	iteration+1, len(chunk.Content))
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
		logger.Errorf(ctx, "[Agent][Thinking][Iteration-%d] Thinking stream failed: %v", iteration+1, err)
		return nil, err
	}

	logger.Infof(ctx, "[Agent][Thinking][Iteration-%d] Thinking completed: content=%d chars, tool_calls=%d",
		iteration+1, len(fullContent), len(toolCalls))

	// Build response
	return &types.ChatResponse{
		Content:      fullContent,
		ToolCalls:    toolCalls,
		FinishReason: "stop",
	}, nil
}

// streamFinalAnswerToEventBus streams the final answer generation through EventBus
func (e *AgentEngine) streamFinalAnswerToEventBus(
	ctx context.Context,
	query string,
	state *types.AgentState,
	sessionID string,
) error {
	logger.Infof(ctx, "[Agent][FinalAnswer] Starting final answer generation")
	totalToolCalls := countTotalToolCalls(state.RoundSteps)
	logger.Infof(ctx, "[Agent][FinalAnswer] Context: %d steps with total %d tool calls",
		len(state.RoundSteps), totalToolCalls)
	common.PipelineInfo(ctx, "Agent", "final_answer_start", map[string]interface{}{
		"session_id":   sessionID,
		"query":        query,
		"steps":        len(state.RoundSteps),
		"tool_results": totalToolCalls,
	})

	// Build messages with all context
	systemPrompt := BuildSystemPrompt(
		e.knowledgeBasesInfo,
		e.config.WebSearchEnabled,
		e.selectedDocs,
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
				Content: fmt.Sprintf("工具 %s 返回: %s", toolCall.Name, toolCall.Result.Output),
			})
			logger.Debugf(ctx, "[Agent][FinalAnswer] Added tool result [Step-%d][Tool-%d]: %s (output: %d chars)",
				stepIdx+1, toolIdx+1, toolCall.Name, len(toolCall.Result.Output))
		}
	}

	logger.Infof(ctx, "[Agent][FinalAnswer] Total context messages: %d (including %d tool results)",
		len(messages), toolResultCount)

	// Add final answer prompt
	finalPrompt := fmt.Sprintf(`基于上述工具调用结果，请为用户问题生成完整答案。

用户问题: %s

要求:
1. 基于实际检索到的内容回答
2. 清晰标注信息来源 (chunk_id, 文档名)
3. 结构化组织答案
4. 如信息不足，诚实说明

现在请生成最终答案:`, query)

	messages = append(messages, chat.Message{
		Role:    "user",
		Content: finalPrompt,
	})

	// Generate a single ID for this entire final answer stream
	answerID := generateEventID("answer")
	logger.Debugf(ctx, "[Agent][FinalAnswer] AnswerID: %s", answerID)

	fullAnswer, _, err := e.streamLLMToEventBus(
		ctx,
		messages,
		&chat.ChatOptions{Temperature: e.config.Temperature, Thinking: e.config.Thinking},
		func(chunk *types.StreamResponse, fullContent string) {
			if chunk.Content != "" {
				logger.Debugf(ctx, "[Agent][FinalAnswer] Emitting answer chunk: %d chars", len(chunk.Content))
				e.eventBus.Emit(ctx, event.Event{
					ID:        answerID, // Same ID for all chunks in this stream
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

	logger.Infof(ctx, "[Agent][FinalAnswer] Final answer generated: %d characters", len(fullAnswer))
	common.PipelineInfo(ctx, "Agent", "final_answer_done", map[string]interface{}{
		"session_id": sessionID,
		"answer_len": len(fullAnswer),
	})
	state.FinalAnswer = fullAnswer
	return nil
}

// countTotalToolCalls counts total tool calls across all steps
func countTotalToolCalls(steps []types.AgentStep) int {
	total := 0
	for _, step := range steps {
		total += len(step.ToolCalls)
	}
	return total
}

// buildMessagesWithLLMContext builds the message array with LLM context
func (e *AgentEngine) buildMessagesWithLLMContext(
	systemPrompt, currentQuery string,
	llmContext []chat.Message,
) []chat.Message {
	messages := []chat.Message{
		{Role: "system", Content: systemPrompt},
	}

	if len(llmContext) > 0 {
		for _, msg := range llmContext {
			if msg.Role == "system" {
				continue
			}
			if msg.Role == "user" || msg.Role == "assistant" || msg.Role == "tool" {
				messages = append(messages, msg)
			}
		}
		logger.Infof(context.Background(), "Added %d history messages to context", len(llmContext))
	}

	messages = append(messages, chat.Message{
		Role:    "user",
		Content: currentQuery,
	})

	return messages
}
