package chatpipline

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginRerank implements reranking functionality for chat pipeline
type PluginRerank struct {
	modelService interfaces.ModelService // Service to access rerank models
}

// NewPluginRerank creates a new rerank plugin instance
func NewPluginRerank(eventManager *EventManager, modelService interfaces.ModelService) *PluginRerank {
	res := &PluginRerank{
		modelService: modelService,
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the event types this plugin handles
func (p *PluginRerank) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHUNK_RERANK}
}

// OnEvent handles reranking events in the chat pipeline
func (p *PluginRerank) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	logger.Info(ctx, "Starting reranking process")
	logger.Infof(ctx, "Getting rerank model, model ID: %s", chatManage.RerankModelID)
	if len(chatManage.SearchResult) == 0 {
		logger.Infof(ctx, "No search result, skip reranking")
		return next()
	}
	if chatManage.RerankModelID == "" {
		logger.Warn(ctx, "Rerank model ID is empty, skipping reranking")
		return next()
	}

	// Get rerank model from service
	rerankModel, err := p.modelService.GetRerankModel(ctx, chatManage.RerankModelID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get rerank model: %v, rerank model ID: %s", err, chatManage.RerankModelID)
		return ErrGetRerankModel.WithError(err)
	}

	// Prepare passages for reranking
	logger.Infof(ctx, "Preparing passages for reranking, search result count: %d", len(chatManage.SearchResult))
	var passages []string
	for _, result := range chatManage.SearchResult {
		// 合并Content和ImageInfo的文本内容
		passage := getEnrichedPassage(ctx, result)
		passages = append(passages, passage)
	}

	// Try reranking with different query variants in priority order
	rerankResp := p.rerank(ctx, chatManage, rerankModel, chatManage.RewriteQuery, passages)
	if len(rerankResp) == 0 {
		rerankResp = p.rerank(ctx, chatManage, rerankModel, chatManage.ProcessedQuery, passages)
		if len(rerankResp) == 0 {
			rerankResp = p.rerank(ctx, chatManage, rerankModel, chatManage.Query, passages)
		}
	}

	// Update search results with reranked scores
	logger.Infof(ctx, "Filtered rerank results, original: %d, filtered: %d", len(rerankResp), len(rerankResp))
	result := []*types.SearchResult{}
	for _, rr := range rerankResp {
		chatManage.SearchResult[rr.Index].Score = rr.RelevanceScore
		result = append(result, chatManage.SearchResult[rr.Index])
	}
	chatManage.RerankResult = result

	if len(chatManage.RerankResult) == 0 {
		logger.Warn(ctx, "Reranking produced no results above threshold")
		return ErrSearchNothing
	}

	logger.Infof(ctx, "Reranking process completed successfully, result count: %d", len(chatManage.RerankResult))
	return next()
}

// rerank performs the actual reranking operation with given query and passages
func (p *PluginRerank) rerank(ctx context.Context,
	chatManage *types.ChatManage, rerankModel rerank.Reranker, query string, passages []string,
) []rerank.RankResult {
	logger.Infof(ctx, "Executing reranking with query: %s, passage count: %d", query, len(passages))
	rerankResp, err := rerankModel.Rerank(ctx, query, passages)
	if err != nil {
		logger.Errorf(ctx, "Reranking failed: %v", err)
		return nil
	}

	// Log top scores for debugging
	logger.Infof(ctx, "Reranking completed, filtering results with threshold: %f", chatManage.RerankThreshold)
	for i := range min(3, len(rerankResp)) {
		logger.Infof(ctx, "Top %d score of rerankResp: %f, passages: %s, index: %d",
			i+1, rerankResp[i].RelevanceScore, rerankResp[i].Document.Text, rerankResp[i].Index,
		)
	}

	// Filter results based on threshold with special handling for history matches
	rankFilter := []rerank.RankResult{}
	for _, result := range rerankResp {
		th := chatManage.RerankThreshold
		matchType := chatManage.SearchResult[result.Index].MatchType
		if matchType == types.MatchTypeHistory {
			th = math.Max(th-0.1, 0.5) // Lower threshold for history matches
		}
		if result.RelevanceScore > th {
			rankFilter = append(rankFilter, result)
		}
	}
	// 兜底逻辑：如果阈值过滤后没有任何结果，则取原始排序的前3名 
	if len(rankFilter) == 0 && len(rerankResp) > 0 {
		logger.Warnf(ctx, "No documents passed the rerank threshold of %.2f. Falling back to Top-3 results.", chatManage.RerankThreshold)
		
		// 确定要取的数量：最多3个，但不超过实际拥有的数量
		topK := 3
		if len(rerankResp) < topK {
			topK = len(rerankResp)
		}
		
		// 从 rerankResp (已排序的完整列表) 中截取前 topK 个
		return rerankResp[:topK]
	}
	return rankFilter
}

// getEnrichedPassage 合并Content和ImageInfo的文本内容
func getEnrichedPassage(ctx context.Context, result *types.SearchResult) string {
	if result.ImageInfo == "" {
		return result.Content
	}

	// 解析ImageInfo
	var imageInfos []types.ImageInfo
	err := json.Unmarshal([]byte(result.ImageInfo), &imageInfos)
	if err != nil {
		logger.Warnf(ctx, "Failed to parse ImageInfo: %v, using content only", err)
		return result.Content
	}

	if len(imageInfos) == 0 {
		return result.Content
	}

	// 提取所有图片的描述和OCR文本
	var imageTexts []string
	for _, img := range imageInfos {
		if img.Caption != "" {
			imageTexts = append(imageTexts, fmt.Sprintf("图片描述: %s", img.Caption))
		}
		if img.OCRText != "" {
			imageTexts = append(imageTexts, fmt.Sprintf("图片文本: %s", img.OCRText))
		}
	}

	if len(imageTexts) == 0 {
		return result.Content
	}

	// 组合内容和图片信息
	combinedText := result.Content
	if combinedText != "" {
		combinedText += "\n\n"
	}
	combinedText += strings.Join(imageTexts, "\n")

	logger.Debugf(ctx, "Enhanced passage with image info: content length %d, image texts length %d",
		len(result.Content), len(strings.Join(imageTexts, "\n")))

	return combinedText
}
