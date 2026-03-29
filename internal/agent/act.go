package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"golang.org/x/sync/errgroup"
)

// toolDisplayNames maps internal tool names to user-friendly display labels.
var toolDisplayNames = map[string]string{
	agenttools.ToolThinking:            "深度思考",
	agenttools.ToolTodoWrite:           "制定计划",
	agenttools.ToolGrepChunks:          "关键词搜索",
	agenttools.ToolKnowledgeSearch:     "知识搜索",
	agenttools.ToolListKnowledgeChunks: "查看文档分块",
	agenttools.ToolQueryKnowledgeGraph: "查询知识图谱",
	agenttools.ToolGetDocumentInfo:     "获取文档信息",
	agenttools.ToolDatabaseQuery:       "查询数据",
	agenttools.ToolDataAnalysis:        "数据分析",
	agenttools.ToolDataSchema:          "查看数据结构",
	agenttools.ToolWebSearch:           "搜索网页",
	agenttools.ToolWebFetch:            "获取网页",
	agenttools.ToolFinalAnswer:         "最终回答",
	agenttools.ToolExecuteSkillScript:  "执行技能脚本",
	agenttools.ToolReadSkill:           "读取技能",
}

// toolHintSensitiveArgs lists tools whose arguments should NOT be shown in hints
// (e.g., database_query exposes raw SQL which leaks implementation details).
var toolHintSensitiveArgs = map[string]bool{
	agenttools.ToolDatabaseQuery: true,
}

// formatToolHint returns a concise human-readable hint for a tool call, e.g. `搜索网页("query text")`.
// Uses display names instead of internal tool names, and hides sensitive arguments.
func formatToolHint(name string, args map[string]any) string {
	displayName := name
	if dn, ok := toolDisplayNames[name]; ok {
		displayName = dn
	}

	if len(args) == 0 || toolHintSensitiveArgs[name] {
		return displayName
	}
	for _, v := range args {
		if s, ok := v.(string); ok {
			if len(s) > 40 {
				s = s[:40] + "…"
			}
			return fmt.Sprintf(`%s("%s")`, displayName, s)
		}
	}
	return displayName
}

// executeToolCalls runs every tool call in the LLM response, appending results to step.ToolCalls.
// It also emits tool-call and tool-result events, and optionally runs reflection after each call.
// When ParallelToolCalls is enabled and there are 2+ tool calls, they execute concurrently.
func (e *AgentEngine) executeToolCalls(
	ctx context.Context, response *types.ChatResponse,
	step *types.AgentStep, iteration int, sessionID string,
) {
	if len(response.ToolCalls) == 0 {
		return
	}

	round := iteration + 1
	n := len(response.ToolCalls)
	logger.Infof(ctx, "[Agent][Round-%d] Executing %d tool call(s)", round, n)

	// Use parallel execution when enabled and there are multiple tool calls
	if e.config.ParallelToolCalls && n >= 2 {
		e.executeToolCallsParallel(ctx, response, step, iteration, sessionID)
		return
	}

	for i, tc := range response.ToolCalls {
		e.executeSingleToolCall(ctx, tc, i, step, iteration, round, sessionID)
	}
}

// executeToolCallsParallel runs all tool calls concurrently using errgroup,
// collecting results in original order.
func (e *AgentEngine) executeToolCallsParallel(
	ctx context.Context, response *types.ChatResponse,
	step *types.AgentStep, iteration int, sessionID string,
) {
	round := iteration + 1
	n := len(response.ToolCalls)
	logger.Infof(ctx, "[Agent][Round-%d] Parallel execution of %d tool calls", round, n)

	results := make([]types.ToolCall, n)
	var mu sync.Mutex
	g, gCtx := errgroup.WithContext(ctx)

	for i, tc := range response.ToolCalls {
		i, tc := i, tc // capture loop vars
		g.Go(func() error {
			toolCall := e.runToolCall(gCtx, tc, i, iteration, round, sessionID)
			mu.Lock()
			results[i] = toolCall
			mu.Unlock()
			return nil // best-effort: don't cancel siblings on failure
		})
	}

	_ = g.Wait()

	// Append results and emit events in original order
	for _, toolCall := range results {
		step.ToolCalls = append(step.ToolCalls, toolCall)

		result := toolCall.Result
		if result == nil {
			result = &types.ToolResult{Success: false, Error: "no result"}
		}

		e.eventBus.Emit(ctx, event.Event{
			ID:        toolCall.ID + "-tool-result",
			Type:      event.EventAgentToolResult,
			SessionID: sessionID,
			Data: event.AgentToolResultData{
				ToolCallID: toolCall.ID,
				ToolName:   toolCall.Name,
				Output:     result.Output,
				Error:      result.Error,
				Success:    result.Success,
				Duration:   toolCall.Duration,
				Iteration:  iteration,
				Data:       result.Data,
			},
		})

		e.eventBus.Emit(ctx, event.Event{
			ID:        toolCall.ID + "-tool-exec",
			Type:      event.EventAgentTool,
			SessionID: sessionID,
			Data: event.AgentActionData{
				Iteration:  iteration,
				ToolName:   toolCall.Name,
				ToolInput:  toolCall.Args,
				ToolOutput: result.Output,
				Success:    result.Success,
				Error:      result.Error,
				Duration:   toolCall.Duration,
			},
		})
	}
}

// executeSingleToolCall runs one tool call sequentially (original behavior).
func (e *AgentEngine) executeSingleToolCall(
	ctx context.Context, tc types.LLMToolCall, i int,
	step *types.AgentStep, iteration, round int, sessionID string,
) {
	toolCall := e.runToolCall(ctx, tc, i, iteration, round, sessionID)
	step.ToolCalls = append(step.ToolCalls, toolCall)

	result := toolCall.Result
	if result == nil {
		result = &types.ToolResult{Success: false, Error: "no result"}
	}

	e.eventBus.Emit(ctx, event.Event{
		ID:        toolCall.ID + "-tool-result",
		Type:      event.EventAgentToolResult,
		SessionID: sessionID,
		Data: event.AgentToolResultData{
			ToolCallID: toolCall.ID,
			ToolName:   toolCall.Name,
			Output:     result.Output,
			Error:      result.Error,
			Success:    result.Success,
			Duration:   toolCall.Duration,
			Iteration:  iteration,
			Data:       result.Data,
		},
	})

	e.eventBus.Emit(ctx, event.Event{
		ID:        toolCall.ID + "-tool-exec",
		Type:      event.EventAgentTool,
		SessionID: sessionID,
		Data: event.AgentActionData{
			Iteration:  iteration,
			ToolName:   toolCall.Name,
			ToolInput:  toolCall.Args,
			ToolOutput: result.Output,
			Success:    result.Success,
			Error:      result.Error,
			Duration:   toolCall.Duration,
		},
	})
}

// runToolCall handles argument parsing, execution, logging, and pipeline events for a single tool call.
// It returns the completed ToolCall struct. Safe to call from multiple goroutines.
func (e *AgentEngine) runToolCall(
	ctx context.Context, tc types.LLMToolCall, i int,
	iteration, round int, sessionID string,
) types.ToolCall {
	tc.ID = agenttools.NormalizeToolCallID(tc.ID, tc.Function.Name, i)
	total := "?" // unknown in isolation; callers log the batch size
	toolTag := fmt.Sprintf("[Agent][Round-%d][Tool %s (%d/%s)]",
		round, tc.Function.Name, i+1, total)

	var args map[string]any
	argsStr := tc.Function.Arguments
	if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
		repaired := agenttools.RepairJSON(argsStr)
		if repairErr := json.Unmarshal([]byte(repaired), &args); repairErr != nil {
			logger.Errorf(ctx, "%s Failed to parse arguments (repair failed): %v", toolTag, err)
			return types.ToolCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: map[string]any{"_raw": argsStr},
				Result: &types.ToolResult{
					Success: false,
					Error: fmt.Sprintf(
						"Failed to parse tool arguments: %v", err,
					) + "\n\n[Analyze the error above and try a different approach.]",
				},
			}
		}
		logger.Warnf(ctx, "%s Repaired malformed JSON arguments", toolTag)
		tc.Function.Arguments = repaired
	}

	logger.Debugf(ctx, "%s Args: %s", toolTag, tc.Function.Arguments)

	toolCallStartTime := time.Now()

	// Emit tool hint for UI progress display
	toolHint := formatToolHint(tc.Function.Name, args)
	e.eventBus.Emit(ctx, event.Event{
		ID:        tc.ID + "-tool-hint",
		Type:      event.EventAgentToolCall,
		SessionID: sessionID,
		Data: event.AgentToolCallData{
			ToolCallID: tc.ID,
			ToolName:   tc.Function.Name,
			Arguments:  args,
			Iteration:  iteration,
			Hint:       toolHint,
		},
	})

	common.PipelineInfo(ctx, "Agent", "tool_call_start", map[string]interface{}{
		"iteration":    iteration,
		"round":        round,
		"tool":         tc.Function.Name,
		"tool_call_id": tc.ID,
		"tool_index":   fmt.Sprintf("%d/%s", i+1, total),
	})

	toolCtx, toolCancel := context.WithTimeout(ctx, defaultToolExecTimeout)
	result, err := e.toolRegistry.ExecuteTool(
		toolCtx, tc.Function.Name,
		json.RawMessage(tc.Function.Arguments),
	)
	toolCancel()
	duration := time.Since(toolCallStartTime).Milliseconds()

	toolCall := types.ToolCall{
		ID:       tc.ID,
		Name:     tc.Function.Name,
		Args:     args,
		Result:   result,
		Duration: duration,
	}

	if err != nil {
		logger.Errorf(ctx, "%s Failed in %dms: %v", toolTag, duration, err)
		toolCall.Result = &types.ToolResult{
			Success: false,
			Error:   err.Error(),
		}
	} else {
		success := result != nil && result.Success
		outputLen := 0
		if result != nil {
			outputLen = len(result.Output)
		}
		logger.Infof(ctx, "%s Completed in %dms: success=%v, output=%d chars",
			toolTag, duration, success, outputLen)
	}

	// Pipeline event for monitoring
	toolSuccess := toolCall.Result != nil && toolCall.Result.Success
	pipelineFields := map[string]interface{}{
		"iteration":    iteration,
		"round":        round,
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

	if toolCall.Result != nil && toolCall.Result.Output != "" {
		preview := toolCall.Result.Output
		if len(preview) > 500 {
			preview = preview[:500] + "... (truncated)"
		}
		logger.Debugf(ctx, "%s Output preview:\n%s", toolTag, preview)
	}
	if toolCall.Result != nil && toolCall.Result.Error != "" {
		logger.Debugf(ctx, "%s Tool error: %s", toolTag, toolCall.Result.Error)
	}

	return toolCall
}
