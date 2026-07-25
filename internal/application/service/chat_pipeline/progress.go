package chatpipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

const (
	retrievalProgressTool       = "knowledge_search"
	queryUnderstandProgressTool = "query_understand"

	retrievalSourceKnowledge = "knowledge"
	retrievalSourceWeb       = "web"
	retrievalSourceMixed     = "mixed"
)

// StageProgress tracks an in-flight pipeline progress tool_call.
type StageProgress struct {
	toolCallID string
	toolName   string
}

// ShouldEmitQueryUnderstandProgress reports whether the query-understand stage
// will actually run (rewrite enabled or images attached).
func ShouldEmitQueryUnderstandProgress(chatManage *types.ChatManage) bool {
	if chatManage == nil {
		return false
	}
	return chatManage.EnableRewrite || len(chatManage.Images) > 0
}

// IsConsolidatedRetrievalStage reports whether a pipeline stage belongs to the
// single user-visible "knowledge search" progress window (search → rerank → merge).
func IsConsolidatedRetrievalStage(stage types.EventType, chatManage *types.ChatManage) bool {
	if chatManage == nil {
		return false
	}
	switch stage {
	case types.CHUNK_SEARCH_PARALLEL, types.CHUNK_RERANK, types.CHUNK_MERGE, types.FILTER_TOP_K:
		return chatManage.NeedsRetrieval()
	case types.WEB_FETCH:
		return chatManage.WebSearchEnabled
	case types.DATA_ANALYSIS:
		return chatManage.DataAnalysisEnabled && chatManage.NeedsRetrieval()
	default:
		return false
	}
}

// LastConsolidatedRetrievalStage returns the last retrieval-related stage in the
// assembled pipeline, or empty when none apply.
func LastConsolidatedRetrievalStage(eventList []types.EventType, chatManage *types.ChatManage) types.EventType {
	var last types.EventType
	for _, stage := range eventList {
		if IsConsolidatedRetrievalStage(stage, chatManage) {
			last = stage
		}
	}
	return last
}

// ShouldCloseRetrievalProgress reports whether the consolidated retrieval
// progress window must be closed after a pipeline stage. It returns true when
// either the planned last retrieval stage completed, or a retrieval stage
// short-circuited the pipeline with an error — including ErrSearchNothing,
// which routes into the fallback response. Closing on the error paths prevents
// the frontend "knowledge_search" spinner from hanging forever when the
// pipeline early-returns before reaching the last retrieval stage.
func ShouldCloseRetrievalProgress(stage, lastRetrievalStage types.EventType, stageErr *PluginError) bool {
	return stage == lastRetrievalStage || stageErr != nil
}

// BeginRetrievalProgress emits a single pending knowledge_search tool_call.
func BeginRetrievalProgress(ctx context.Context, chatManage *types.ChatManage) *StageProgress {
	if chatManage == nil || chatManage.EventBus == nil {
		return nil
	}

	toolCallID := uuid.New().String()
	args := map[string]any{
		"search_source": retrievalSearchSource(chatManage),
	}
	if chatManage.RewriteQuery != "" {
		args["query"] = chatManage.RewriteQuery
	} else if chatManage.Query != "" {
		args["query"] = chatManage.Query
	}

	_ = chatManage.EventBus.Emit(ctx, types.Event{
		Type:      types.EventType(event.EventAgentToolCall),
		SessionID: chatManage.SessionID,
		Data: event.AgentToolCallData{
			ToolCallID: toolCallID,
			ToolName:   retrievalProgressTool,
			Arguments:  args,
		},
	})

	return &StageProgress{toolCallID: toolCallID, toolName: retrievalProgressTool}
}

// BeginQueryUnderstandProgress emits a pending query_understand tool_call.
func BeginQueryUnderstandProgress(ctx context.Context, chatManage *types.ChatManage) *StageProgress {
	if chatManage == nil || chatManage.EventBus == nil || !ShouldEmitQueryUnderstandProgress(chatManage) {
		return nil
	}

	toolCallID := uuid.New().String()
	args := map[string]any{}
	if chatManage.Query != "" {
		args["query"] = chatManage.Query
	}
	if len(chatManage.Images) > 0 {
		args["has_images"] = true
	}

	_ = chatManage.EventBus.Emit(ctx, types.Event{
		Type:      types.EventType(event.EventAgentToolCall),
		SessionID: chatManage.SessionID,
		Data: event.AgentToolCallData{
			ToolCallID: toolCallID,
			ToolName:   queryUnderstandProgressTool,
			Arguments:  args,
		},
	})

	return &StageProgress{toolCallID: toolCallID, toolName: queryUnderstandProgressTool}
}

// EndQueryUnderstandProgress emits the matching tool_result for query understanding.
func EndQueryUnderstandProgress(
	ctx context.Context,
	chatManage *types.ChatManage,
	progress *StageProgress,
	start time.Time,
	stageErr *PluginError,
) {
	if progress == nil || chatManage == nil || chatManage.EventBus == nil {
		return
	}

	success := stageErr == nil
	output := ""
	if success {
		output = "已完成问题理解"
	}

	var errMsg string
	if !success && stageErr != nil && stageErr.Err != nil {
		errMsg = stageErr.Err.Error()
	}

	_ = chatManage.EventBus.Emit(ctx, types.Event{
		Type:      types.EventType(event.EventAgentToolResult),
		SessionID: chatManage.SessionID,
		Data: event.AgentToolResultData{
			ToolCallID: progress.toolCallID,
			ToolName:   queryUnderstandProgressTool,
			Output:     output,
			Error:      errMsg,
			Success:    success,
			Duration:   time.Since(start).Milliseconds(),
		},
	})
}

// EndRetrievalProgress emits the matching tool_result for the consolidated retrieval window.
func EndRetrievalProgress(
	ctx context.Context,
	chatManage *types.ChatManage,
	progress *StageProgress,
	start time.Time,
	stageErr *PluginError,
) {
	if progress == nil || chatManage == nil || chatManage.EventBus == nil {
		return
	}

	count, docCount, webCount := retrievalResultBreakdown(chatManage)
	searchSource := retrievalSearchSource(chatManage)
	if count > 0 {
		switch {
		case docCount > 0 && webCount > 0:
			searchSource = retrievalSourceMixed
		case webCount > 0:
			searchSource = retrievalSourceWeb
		default:
			searchSource = retrievalSourceKnowledge
		}
	}
	success := stageErr == nil || stageErr == ErrSearchNothing
	output := ""
	if success {
		if count == 0 {
			output = "未检索到相关内容"
		} else {
			output = fmt.Sprintf("检索到 %d 条相关内容", count)
		}
	}

	var errMsg string
	if !success && stageErr != nil && stageErr.Err != nil {
		errMsg = stageErr.Err.Error()
	}

	_ = chatManage.EventBus.Emit(ctx, types.Event{
		Type:      types.EventType(event.EventAgentToolResult),
		SessionID: chatManage.SessionID,
		Data: event.AgentToolResultData{
			ToolCallID: progress.toolCallID,
			ToolName:   retrievalProgressTool,
			Output:     output,
			Error:      errMsg,
			Success:    success,
			Duration:   time.Since(start).Milliseconds(),
			Data: map[string]interface{}{
				"count":         count,
				"doc_count":     docCount,
				"web_count":     webCount,
				"search_source": searchSource,
			},
		},
	})
}

func hasKBRetrievalTargets(chatManage *types.ChatManage) bool {
	if chatManage == nil {
		return false
	}
	return types.HasKnowledgeRetrievalScope(
		chatManage.SearchTargets,
		chatManage.KnowledgeBaseIDs,
		chatManage.KnowledgeIDs,
	)
}

func retrievalSearchSource(chatManage *types.ChatManage) string {
	hasKB := hasKBRetrievalTargets(chatManage)
	hasWeb := chatManage != nil && chatManage.WebSearchEnabled
	switch {
	case hasKB && hasWeb:
		return retrievalSourceMixed
	case hasWeb:
		return retrievalSourceWeb
	default:
		return retrievalSourceKnowledge
	}
}

func retrievalResults(chatManage *types.ChatManage) []*types.SearchResult {
	switch {
	case len(chatManage.MergeResult) > 0:
		return chatManage.MergeResult
	case len(chatManage.RerankResult) > 0:
		return chatManage.RerankResult
	default:
		return chatManage.SearchResult
	}
}

func retrievalResultBreakdown(chatManage *types.ChatManage) (total, docCount, webCount int) {
	for _, result := range retrievalResults(chatManage) {
		if result == nil {
			continue
		}
		total++
		if isWebSearchResult(result) {
			webCount++
		} else {
			docCount++
		}
	}
	return total, docCount, webCount
}

func isWebSearchResult(result *types.SearchResult) bool {
	if strings.EqualFold(result.ChunkType, "web_search") {
		return true
	}
	return strings.EqualFold(result.KnowledgeSource, "web_search")
}
