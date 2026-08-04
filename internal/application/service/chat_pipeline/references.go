package chatpipeline

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// prepareMessagesWithModelContext replaces positional retrieval IDs with
// request-local model handles. Persisted rendered_content remains unchanged;
// public citations are expanded only when the request setting enables them.
func prepareMessagesWithModelContext(
	ctx context.Context,
	chatManage *types.ChatManage,
) ([]chat.Message, *modelcontext.Registry) {
	citationsEnabled := chatManage == nil || chatManage.CitationsEnabled()
	registry := modelcontext.NewRegistry(citationsEnabled)
	if chatManage == nil {
		return nil, registry
	}
	messages := prepareMessagesWithHistory(chatManage)
	if len(messages) > 0 {
		messages[0].Content = strings.TrimRight(messages[0].Content, " \t\r\n") + registry.ProtocolPrompt()
	}
	if len(chatManage.MergeResult) == 0 || len(messages) == 0 {
		return messages, registry
	}

	ordered := orderedPipelineReferences(chatManage)
	knowledgeResults := make([]*types.SearchResult, 0, len(ordered))
	knowledgeRows := make([]map[string]interface{}, 0, len(ordered))
	webRows := make([]map[string]interface{}, 0)
	for _, result := range ordered {
		if result == nil {
			continue
		}
		if isPipelineWebReference(result) {
			webRows = append(webRows, map[string]interface{}{
				"url":          result.ID,
				"title":        firstPipelineTitle(result),
				"snippet":      result.Content,
				"published_at": result.Metadata["published_at"],
			})
			continue
		}
		knowledgeResults = append(knowledgeResults, result)
		knowledgeRows = append(knowledgeRows, map[string]interface{}{
			"chunk_id":          result.ID,
			"knowledge_id":      result.KnowledgeID,
			"knowledge_base_id": result.KnowledgeBaseID,
			"knowledge_title":   firstPipelineTitle(result),
			"chunk_index":       result.ChunkIndex,
			"chunk_type":        result.ChunkType,
			"content":           getEnrichedPassageForChat(ctx, result),
		})
	}
	registry.RegisterSearchResults(knowledgeResults)
	var contextParts []string
	if len(knowledgeRows) > 0 {
		contextParts = append(contextParts, registry.ModelToolResult(&types.ToolResult{
			Success: true,
			Data: map[string]interface{}{
				"display_type": "search_results",
				"results":      knowledgeRows,
			},
		}))
	}
	if len(webRows) > 0 {
		contextParts = append(contextParts, registry.ModelToolResult(&types.ToolResult{
			Success: true,
			Data: map[string]interface{}{
				"display_type": "web_search_results",
				"results":      webRows,
			},
		}))
	}
	modelContexts := strings.Join(contextParts, "\n")
	if strings.TrimSpace(modelContexts) == "" {
		return messages, registry
	}

	last := len(messages) - 1
	replaced := false
	for _, index := range []int{0, last} {
		if chatManage.RenderedContexts != "" && strings.Contains(messages[index].Content, chatManage.RenderedContexts) {
			messages[index].Content = strings.ReplaceAll(messages[index].Content, chatManage.RenderedContexts, modelContexts)
			replaced = true
		}
	}
	if !replaced {
		messages[last].Content = modelContexts + "\n\n" + messages[last].Content
	}
	return messages, registry
}

func isPipelineWebReference(result *types.SearchResult) bool {
	if result == nil {
		return false
	}
	return strings.EqualFold(result.ChunkType, string(types.ChunkTypeWebSearch)) ||
		strings.EqualFold(result.KnowledgeSource, "web_search")
}

func orderedPipelineReferences(chatManage *types.ChatManage) []*types.SearchResult {
	if chatManage == nil {
		return nil
	}
	if !chatManage.FAQPriorityEnabled {
		return chatManage.MergeResult
	}
	ordered := make([]*types.SearchResult, 0, len(chatManage.MergeResult))
	for _, result := range chatManage.MergeResult {
		if result != nil && result.ChunkType == string(types.ChunkTypeFAQ) {
			ordered = append(ordered, result)
		}
	}
	for _, result := range chatManage.MergeResult {
		if result != nil && result.ChunkType != string(types.ChunkTypeFAQ) {
			ordered = append(ordered, result)
		}
	}
	return ordered
}

func firstPipelineTitle(result *types.SearchResult) string {
	if result == nil {
		return ""
	}
	if result.KnowledgeTitle != "" {
		return result.KnowledgeTitle
	}
	return result.KnowledgeFilename
}
