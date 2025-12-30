package chatpipline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

type PluginDataAnalysis struct {
	modelService     interfaces.ModelService
	knowledgeService interfaces.KnowledgeService
	chunkRepo        interfaces.ChunkRepository
	db               *sql.DB
}

func NewPluginDataAnalysis(
	eventManager *EventManager,
	modelService interfaces.ModelService,
	knowledgeService interfaces.KnowledgeService,
	chunkRepo interfaces.ChunkRepository,
	db *sql.DB,
) *PluginDataAnalysis {
	p := &PluginDataAnalysis{
		modelService:     modelService,
		knowledgeService: knowledgeService,
		chunkRepo:        chunkRepo,
		db:               db,
	}
	eventManager.Register(p)
	return p
}

func (p *PluginDataAnalysis) ActivationEvents() []types.EventType {
	return []types.EventType{types.DATA_ANALYSIS}
}

func (p *PluginDataAnalysis) OnEvent(
	ctx context.Context,
	eventType types.EventType,
	chatManage *types.ChatManage,
	next func() *PluginError,
) *PluginError {
	// 1. Check if there are any CSV/Excel files in MergeResult
	var dataFiles []*types.SearchResult
	for _, result := range chatManage.MergeResult {
		if isDataFile(result.KnowledgeFilename) {
			dataFiles = append(dataFiles, result)
		}
	}

	// Filter out table column and table summary chunks from MergeResult
	chatManage.MergeResult = filterOutTableChunks(chatManage.MergeResult)

	if len(dataFiles) == 0 {
		return next()
	}

	// 2. Ask LLM if data analysis is needed
	// We only process the first data file for now to avoid complexity
	targetFile := dataFiles[0]

	// Get Knowledge details to get file path
	knowledge, err := p.knowledgeService.GetKnowledgeByID(ctx, targetFile.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge %s: %v", targetFile.KnowledgeID, err)
		return next()
	}

	// Initialize DataAnalysisTool
	tool := tools.NewDataAnalysisTool(p.knowledgeService, p.db, chatManage.SessionID)
	defer tool.Cleanup(ctx)

	// Load data into DuckDB
	schema, err := tool.LoadFromKnowledge(ctx, knowledge)
	if err != nil {
		logger.Errorf(ctx, "Failed to get data schema: %v", err)
		return next()
	}

	// Ask LLM to generate SQL for data analysis
	chatModel, err := p.modelService.GetChatModel(ctx, chatManage.ChatModelID)
	if err != nil {
		return ErrGetChatModel.WithError(err)
	}

	// Use utils.GenerateSchema to generate format schema for DataAnalysisInput
	formatSchema := utils.GenerateSchema[tools.DataAnalysisInput]()

	analysisPrompt := fmt.Sprintf(`
User Question: %s
Knowledge ID: %s
Table Schema: %s

Determine if the user's question requires data analysis (e.g., statistics, aggregation, filtering) on this table.
If YES, generate a DuckDB SQL query to answer the user's question and fill in the knowledge_id and sql fields.
If NO, leave the sql field empty.

Return your response in the specified JSON format.`, chatManage.Query, knowledge.ID, schema.Description())

	response, err := chatModel.Chat(ctx, []chat.Message{
		{Role: "user", Content: analysisPrompt},
	}, &chat.ChatOptions{
		Temperature: 0.1,
		Format:      formatSchema,
	})
	if err != nil {
		logger.Errorf(ctx, "Failed to generate analysis response: %v", err)
		return next()
	}
	// logger.Debugf(ctx, "Data analysis LLM response: %s", response.Content)

	// Execute SQL using the tool
	// Initialize DataAnalysisTool
	toolResult, err := tool.Execute(ctx, json.RawMessage(response.Content))
	if err != nil {
		logger.Errorf(ctx, "Failed to execute SQL: %v", err)
		return next()
	}

	// 5. Store result
	// Create a new SearchResult for the analysis output
	analysisResult := &types.SearchResult{
		ID:                "analysis_" + knowledge.ID,
		Content:           toolResult.Output,
		Score:             1.0,
		MatchType:         types.MatchTypeDataAnalysis,
		KnowledgeID:       knowledge.ID,
		KnowledgeTitle:    knowledge.Title,
		KnowledgeFilename: knowledge.FileName,
	}

	chatManage.MergeResult = append(chatManage.MergeResult, analysisResult)

	return next()
}

func isDataFile(filename string) bool {
	lower := strings.ToLower(filename)
	return strings.HasSuffix(lower, ".csv") || strings.HasSuffix(lower, ".xlsx") || strings.HasSuffix(lower, ".xls")
}

// filterOutTableChunks filters out table column and table summary chunks from search results
func filterOutTableChunks(results []*types.SearchResult) []*types.SearchResult {
	filtered := make([]*types.SearchResult, 0, len(results))
	filterList := []string{string(types.ChunkTypeTableColumn), string(types.ChunkTypeTableSummary)}
	for _, result := range results {
		if slices.Contains(filterList, result.ChunkType) {
			continue
		}
		filtered = append(filtered, result)
	}
	return filtered
}
