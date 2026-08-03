package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"golang.org/x/sync/errgroup"
)

// langfuseToolOutputPreview caps the Output field we send to Langfuse for a
// tool call. Tool outputs are already truncated by the registry to
// DefaultMaxToolOutput (16KB) before this point, but rendering 16KB in the
// Langfuse UI for every tool call is noisy. We keep a generous slice so the
// gist is preserved, and include the original length in metadata.
const langfuseToolOutputPreview = 4000

// truncateForLangfuse returns s truncated to at most n runes, with a "…"
// marker appended when truncated. Runes (not bytes) are used so multi-byte
// CJK content is never split mid-character.
func truncateForLangfuse(s string, n int) string {
	if n <= 0 || len(s) == 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// argKeys returns the sorted list of top-level keys in a tool's argument
// map. Used when we choose not to send the raw arguments to Langfuse
// (e.g. database_query's SQL) but still want to signal what was passed in.
func argKeys(args map[string]any) []string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// traceArgumentValue keeps valid model-emitted JSON structured in Langfuse
// while preserving malformed payloads verbatim for diagnosis.
func traceArgumentValue(raw string) interface{} {
	var value interface{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	return value
}

// buildToolSpanInput exposes both sides of the model-context boundary:
// model_arguments is exactly what the model emitted (including temporary
// handles), while resolved_arguments is the durable payload actually executed.
// Langfuse never performs mapping itself; it only observes the registry's audit.
func buildToolSpanInput(tc types.LLMToolCall, resolvedArgs map[string]any, sensitive bool) map[string]interface{} {
	modelArguments := tc.ModelArguments
	if modelArguments == "" {
		modelArguments = tc.Function.Arguments
	}
	resolution := tc.ArgumentResolution
	if resolution == "" {
		resolution = modelcontext.ArgumentResolutionUnchanged
	}
	if sensitive {
		modelArgKeys := []string(nil)
		if parsed, ok := traceArgumentValue(modelArguments).(map[string]interface{}); ok {
			modelArgKeys = argKeys(parsed)
		}
		return map[string]interface{}{
			"tool_call_id":            tc.ID,
			"model_arg_keys":          modelArgKeys,
			"resolved_arg_keys":       argKeys(resolvedArgs),
			"argument_resolution":     resolution,
			"unresolved_handle_count": len(tc.UnresolvedHandles),
			"args_redacted":           true,
		}
	}
	return map[string]interface{}{
		"tool_call_id":        tc.ID,
		"model_arguments":     traceArgumentValue(modelArguments),
		"resolved_arguments":  resolvedArgs,
		"argument_resolution": resolution,
		"unresolved_handles":  tc.UnresolvedHandles,
	}
}

// finishToolSpan serialises a completed tool call into a Langfuse span
// update. Extracted from runToolCall so the tool-call pipeline keeps
// a single assignment per line and the observability-specific logic
// (payload shaping, error classification) lives in one place.
func finishToolSpan(span *langfuse.Span, tc types.ToolCall, execErr error, durationMs int64) {
	if span == nil {
		return
	}
	success := tc.Result != nil && tc.Result.Success
	output := map[string]interface{}{
		"success":     success,
		"duration_ms": durationMs,
	}
	if tc.Result != nil {
		if tc.Result.Output != "" {
			output["output"] = truncateForLangfuse(tc.Result.Output, langfuseToolOutputPreview)
			output["output_len"] = len(tc.Result.Output)
		}
		if tc.Result.Error != "" {
			output["error"] = tc.Result.Error
		}
		if len(tc.Result.Data) > 0 {
			// Data is structured but can be arbitrarily large (e.g. full
			// search-result payloads). Only report key shape so Langfuse
			// users see what was surfaced without blowing up trace size.
			output["data_keys"] = dataKeys(tc.Result.Data)
		}
		if len(tc.Result.Images) > 0 {
			output["image_count"] = len(tc.Result.Images)
		}
	}
	// Classify the span's outcome: a non-nil execErr is always an error, and
	// a result with Success=false is treated as an error too (matches the
	// user-visible behaviour — the LLM would see this as a failed tool call
	// and try a different approach).
	var spanErr error
	switch {
	case execErr != nil:
		spanErr = execErr
	case tc.Result != nil && !tc.Result.Success:
		msg := tc.Result.Error
		if msg == "" {
			msg = "tool returned success=false"
		}
		spanErr = errors.New(msg)
	}
	span.Finish(output, map[string]interface{}{
		"success":     success,
		"duration_ms": durationMs,
	}, spanErr)
}

// dataKeys returns the sorted top-level keys of a tool's Data map.
func dataKeys(data map[string]interface{}) []string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

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
	step *types.AgentStep, iteration int, sessionID, assistantMessageID string,
) {
	if len(response.ToolCalls) == 0 {
		return
	}

	round := iteration + 1
	n := len(response.ToolCalls)
	logger.Infof(ctx, "[Agent][Round-%d] Executing %d tool call(s)", round, n)

	// Use parallel execution when enabled and there are multiple tool calls
	if e.config.ParallelToolCalls && n >= 2 {
		e.executeToolCallsParallel(ctx, response, step, iteration, sessionID, assistantMessageID)
		return
	}

	for i, tc := range response.ToolCalls {
		e.executeSingleToolCall(ctx, tc, i, step, iteration, round, sessionID, assistantMessageID)
	}
}

// executeToolCallsParallel runs all tool calls concurrently using errgroup,
// collecting results in original order.
func (e *AgentEngine) executeToolCallsParallel(
	ctx context.Context, response *types.ChatResponse,
	step *types.AgentStep, iteration int, sessionID, assistantMessageID string,
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
			toolCall := e.runToolCall(gCtx, tc, i, iteration, round, sessionID, assistantMessageID)
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
	step *types.AgentStep, iteration, round int, sessionID, assistantMessageID string,
) {
	toolCall := e.runToolCall(ctx, tc, i, iteration, round, sessionID, assistantMessageID)
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
	iteration, round int, sessionID, assistantMessageID string,
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
				ID:               tc.ID,
				Name:             tc.Function.Name,
				Args:             map[string]any{"_raw": argsStr},
				ProviderMetadata: tc.ProviderMetadata,
				Result: &types.ToolResult{
					Success: false,
					Error: fmt.Sprintf(
						"Failed to parse tool arguments: %v", err,
					) + "\n\n[Analyze the error above and try a different approach.]",
				},
			}
		}
		logger.Warnf(ctx, "%s Repaired malformed JSON arguments", toolTag)
		// The initial model-context pass could not inspect malformed JSON.
		// Decode the repaired payload before execution, while preserving the
		// exact provider payload already stored in tc.ModelArguments.
		decoded := tc
		decoded.ModelArguments = ""
		decoded.Function.Arguments = repaired
		decodedCalls := []types.LLMToolCall{decoded}
		e.modelContext.DecodeToolCalls(decodedCalls)
		tc.Function.Arguments = decodedCalls[0].Function.Arguments
		tc.ArgumentResolution = decodedCalls[0].ArgumentResolution
		tc.UnresolvedHandles = decodedCalls[0].UnresolvedHandles
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return types.ToolCall{
				ID:               tc.ID,
				Name:             tc.Function.Name,
				Args:             map[string]any{"_raw": tc.Function.Arguments},
				ProviderMetadata: tc.ProviderMetadata,
				Result: &types.ToolResult{
					Success: false,
					Error:   fmt.Sprintf("Failed to parse repaired tool arguments: %v", err),
				},
			}
		}
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

	// Open a Langfuse span for the tool invocation so the Langfuse UI shows
	// trace → agent.execute → agent.round.N → agent.tool.<name>, alongside
	// any nested generations (embedding/rerank/VLM) that the tool itself
	// triggers. No-op when Langfuse is disabled.
	mgr := langfuse.GetManager()
	// database_query's SQL is treated as sensitive by the UI hint layer
	// (toolHintSensitiveArgs) because it exposes implementation details.
	// Mirror that policy for Langfuse: redact raw arguments to avoid
	// leaking raw SQL into the observability backend.
	toolSpanInput := buildToolSpanInput(tc, args, toolHintSensitiveArgs[tc.Function.Name])
	argumentResolution, _ := toolSpanInput["argument_resolution"].(string)
	toolCtx, toolSpan := mgr.StartSpan(ctx, langfuse.SpanOptions{
		Name:  "agent.tool." + tc.Function.Name,
		Input: toolSpanInput,
		Metadata: map[string]interface{}{
			"iteration":               iteration,
			"round":                   round,
			"tool_index":              i + 1,
			"tool_call_id":            tc.ID,
			"session_id":              sessionID,
			"argument_resolution":     argumentResolution,
			"unresolved_handle_count": len(tc.UnresolvedHandles),
		},
	})

	principal, _ := types.PrincipalFromContext(ctx)
	toolExecCtx := agenttools.WithToolExecContext(toolCtx, &agenttools.ToolExecContext{
		SessionID:          sessionID,
		AssistantMessageID: assistantMessageID,
		EventBus:           e.eventBus,
		ToolCallID:         tc.ID,
		UserID:             principal.StorageID(),
		// ApprovalCtx keeps the round-level ctx without the per-tool 60s timeout,
		// so MCP tool human-approval (issue #1173) can legitimately block longer.
		ApprovalCtx: toolCtx,
		ExecTimeout: defaultToolExecTimeout,
	})

	var result *types.ToolResult
	var err error
	if len(tc.UnresolvedHandles) > 0 {
		// A temporary handle is not an application identity. Fail before tool
		// execution so a hallucinated/stale cN/dN/bN/wN/iN/res:// token can
		// never reach persistence, an external service, or a routing decision.
		err = fmt.Errorf("tool arguments contain unresolved model handles: %v", tc.UnresolvedHandles)
	} else {
		execCtx, toolCancel := context.WithTimeout(toolExecCtx, defaultToolExecTimeout)
		result, err = e.toolRegistry.ExecuteTool(
			execCtx, tc.Function.Name,
			json.RawMessage(tc.Function.Arguments),
		)
		toolCancel()
	}
	duration := time.Since(toolCallStartTime).Milliseconds()

	toolCall := types.ToolCall{
		ID:               tc.ID,
		Name:             tc.Function.Name,
		Args:             args,
		Result:           result,
		Duration:         duration,
		ProviderMetadata: tc.ProviderMetadata,
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

	finishToolSpan(toolSpan, toolCall, err, duration)

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

	// Surface retrieved chunks to the feedback attribution pipeline so a
	// later like/dislike can be credited to the chunks the LLM actually
	// read. The KB pipeline emits the same event before the answer stream
	// (#1248); the agent path skipped it because each tool call carries its
	// own Data["results"] payload instead of one merged search result, so
	// we re-emit here per call. The downstream handler in qa.go accumulates
	// refs across events, so multiple knowledge_search / hybrid_search calls
	// in the same session still produce the union of contributing chunks.
	if refs := chunksFromToolResult(ctx, toolCall); len(refs) > 0 {
		e.eventBus.Emit(ctx, event.Event{
			ID:        toolCall.ID + "-refs",
			Type:      event.EventAgentReferences,
			SessionID: sessionID,
			Data: event.AgentReferencesData{
				References: types.References(refs),
				Iteration:  iteration,
			},
		})
	}

	return toolCall
}

// chunksFromToolResult extracts SearchResult rows out of a tool call's
// structured Data payload. Only retrieval-shaped tools contribute; document
// metadata tools (get_document_info) either omit chunk ids or carry them in
// a non-uniform shape that breaks the chunk-level unique index, so we skip
// them and rely on knowledge_search / list_knowledge_chunks to cover the
// answers that actually quote chunk content.
func chunksFromToolResult(ctx context.Context, tc types.ToolCall) []*types.SearchResult {
	if tc.Result == nil || !tc.Result.Success || len(tc.Result.Data) == 0 {
		return nil
	}
	switch tc.Name {
	case agenttools.ToolKnowledgeSearch:
		// ToolKnowledgeSearch is also the registry name for the
		// hybrid_search backend, so it covers both surfaces.
		return searchResultsFromMap(tc.Result.Data, "results")
	case agenttools.ToolListKnowledgeChunks:
		// list_knowledge_chunks returns the full chunk set under Data["chunks"];
		// the persistence sanitiser strips that field for db size, but the
		// in-memory tool result still carries it, so emit here before storage.
		// Different chunk-id keys per codepath: list_by_knowledge uses "id",
		// the single-chunk (faq_id / chunk_id) path also uses "id".
		rawChunks := tc.Result.Data["chunks"]
		rows := toChunkRowSlice(rawChunks)
		if len(rows) == 0 {
			return nil
		}
		out := make([]*types.SearchResult, 0, len(rows))
		for _, m := range rows {
			id := stringField(m, "chunk_id")
			if id == "" {
				id = stringField(m, "id")
			}
			kbID := stringField(m, "knowledge_base")
			if kbID == "" {
				kbID = stringField(m, "knowledge_base_id")
			}
			if id == "" || kbID == "" {
				continue
			}
			out = append(out, &types.SearchResult{
				ID:              id,
				KnowledgeID:     stringField(m, "knowledge_id"),
				KnowledgeBaseID: kbID,
				Content:         stringField(m, "content"),
			})
		}
		return out
	case agenttools.ToolGrepChunks:
		// grep_chunks returns per-KB rollups (Data["knowledge_results"]) but
		// not individual chunk ids in the persisted payload, so it cannot
		// contribute chunk-level attribution. Skip; knowledge_search covers
		// the answers that follow a grep.
		return nil
	}
	return nil
}

// searchResultsFromMap converts a slice of map-shaped chunk rows under the
// given key into *types.SearchResult values. Rows missing chunk_id or
// knowledge_base_id are skipped — MessageChunkReference requires both.
func searchResultsFromMap(data map[string]interface{}, key string) []*types.SearchResult {
	rows := toChunkRowSlice(data[key])
	if len(rows) == 0 {
		return nil
	}
	out := make([]*types.SearchResult, 0, len(rows))
	for _, m := range rows {
		id, _ := m["chunk_id"].(string)
		kbID, _ := m["knowledge_base_id"].(string)
		if id == "" || kbID == "" {
			continue
		}
		sr := &types.SearchResult{
			ID:              id,
			KnowledgeID:     stringField(m, "knowledge_id"),
			KnowledgeBaseID: kbID,
			KnowledgeTitle:  stringField(m, "knowledge_title"),
		}
		if idx, ok := m["chunk_index"].(float64); ok {
			sr.ChunkIndex = int(idx)
		}
		if content, ok := m["content"].(string); ok {
			sr.Content = content
		}
		out = append(out, sr)
	}
	return out
}

// isKnowledgeTool reports whether the tool name is one of the
// retrieval-shaped tools we care about for chunk attribution. Used purely
// for diagnostic logging — chunk extraction itself lives in
// chunksFromToolResult.
func isKnowledgeTool(name string) bool {
	switch name {
	case agenttools.ToolKnowledgeSearch,
		agenttools.ToolListKnowledgeChunks,
		agenttools.ToolGrepChunks:
		return true
	}
	return false
}

// truncatedForLog is a tiny helper to keep the rawChunks diagnostic line
// bounded; the chunks payload can include full chunk text and run into KB.
func truncatedForLog(v interface{}, maxRows int) interface{} {
	switch x := v.(type) {
	case []interface{}:
		if len(x) <= maxRows {
			return x
		}
		return append([]interface{}{}, x[:maxRows]...)
	}
	return v
}

// toChunkRowSlice normalises whatever shape the tool Data field uses for the
// chunks array. Across tools and test fixtures it shows up as []interface{},
// []map[string]interface{}, or even typed []*types.Chunk-shaped slices, so
// flatten them all to []map[string]interface{} before the row conversion
// in chunksFromToolResult.
func toChunkRowSlice(v interface{}) []map[string]interface{} {
	switch x := v.(type) {
	case []map[string]interface{}:
		return x
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(x))
		for _, r := range x {
			if m, ok := r.(map[string]interface{}); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

// stringField returns the string value of a map entry. Used to keep the
// conversion helpers above resilient to the json.Unmarshal default types
// (string for strings, float64 for numbers).
func stringField(m map[string]interface{}, key string) string {
	s, _ := m[key].(string)
	return s
}
