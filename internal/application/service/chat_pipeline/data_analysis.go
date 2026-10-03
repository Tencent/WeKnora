package chatpipeline

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
	modelService         interfaces.ModelService
	knowledgeBaseService interfaces.KnowledgeBaseService
	knowledgeService     interfaces.KnowledgeService
	fileService          interfaces.FileService
	chunkRepo            interfaces.ChunkRepository
	tenantService        interfaces.TenantService
	db                   *sql.DB
}

func NewPluginDataAnalysis(
	eventManager *EventManager,
	modelService interfaces.ModelService,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	fileService interfaces.FileService,
	chunkRepo interfaces.ChunkRepository,
	tenantService interfaces.TenantService,
	db *sql.DB,
) *PluginDataAnalysis {
	p := &PluginDataAnalysis{
		modelService:         modelService,
		knowledgeBaseService: knowledgeBaseService,
		knowledgeService:     knowledgeService,
		fileService:          fileService,
		chunkRepo:            chunkRepo,
		tenantService:        tenantService,
		db:                   db,
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
	if !chatManage.NeedsRetrieval() {
		return next()
	}
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
	tool := tools.NewDataAnalysisTool(p.knowledgeBaseService, p.knowledgeService, p.tenantService, p.fileService, p.db, chatManage.SessionID)
	defer tool.Cleanup(ctx)

	// Load data into DuckDB
	schema, err := tool.LoadFromKnowledge(ctx, knowledge)
	if err != nil {
		logger.Errorf(ctx, "Failed to get data schema: %v", err)
		return next()
	}

	chatModel, err := p.modelService.GetChatModel(ctx, chatManage.ChatModelID)
	if err != nil {
		return ErrGetChatModel.WithError(err)
	}

	toolResult, err := runDataAnalysis(ctx, chatModel, tool, knowledge.ID, chatManage.Query, schema)
	if err != nil {
		// The analysis is optional for the answer, so the pipeline continues,
		// but a failed plan is a real signal (bad SQL contract, model drift,
		// broken data file) and must not disappear into a debug log.
		pipelineWarn(ctx, "DataAnalysis", "analysis_failed", map[string]interface{}{
			"knowledge_id": knowledge.ID,
			"error":        err.Error(),
		})
		return next()
	}
	if toolResult == nil {
		pipelineInfo(ctx, "DataAnalysis", "not_needed", map[string]interface{}{
			"knowledge_id": knowledge.ID,
		})
		return next()
	}

	// 5. Store result
	// Create a new SearchResult for the analysis output
	analysisResult := &types.SearchResult{
		ID:                   "analysis_" + knowledge.ID,
		Content:              toolResult.Output,
		Score:                1.0,
		MatchType:            types.MatchTypeDataAnalysis,
		KnowledgeID:          knowledge.ID,
		KnowledgeTitle:       knowledge.Title,
		KnowledgeFilename:    knowledge.FileName,
		KnowledgeDescription: knowledge.Description,
	}

	chatManage.MergeResult = append(chatManage.MergeResult, analysisResult)

	return next()
}

// dataAnalysisPlan is the structured decision the planning model returns. The
// document is chosen by the pipeline, never by the model, so the plan carries
// only whether analysis is needed and the SQL to run against the fixed
// tools.DataAnalysisTableName table.
type dataAnalysisPlan struct {
	NeedsAnalysis bool   `json:"needs_analysis" jsonschema:"true when answering the question requires statistics, aggregation or filtering over the table"` //nolint:lll // jsonschema tag
	SQL           string `json:"sql" jsonschema:"DuckDB SELECT over the table named dataset when needs_analysis is true; empty otherwise"`                  //nolint:lll // jsonschema tag
}

// dataAnalysisExecutor is the slice of DataAnalysisTool the planner needs.
type dataAnalysisExecutor interface {
	Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error)
}

const dataAnalysisPlanPromptTemplate = `User Question: %s

The user's question may be answerable from a single CSV/Excel document that is loaded into DuckDB.
Its rows are exposed as the table %q. This is the only table; never reference any other table name.

Table Schema:
%s

Determine if the user's question requires data analysis (e.g., statistics, aggregation, filtering) on this table.
If YES, set needs_analysis to true and write one read-only DuckDB SELECT against the table %q
in the sql field. If NO, set needs_analysis to false and leave the sql field empty.

Return your response in the specified JSON format.`

const dataAnalysisRepairPromptTemplate = `The SQL you generated was rejected:

SQL: %s
Error: %s

Fix the query. The table is named %q and column names must match the schema exactly
(quote identifiers with double quotes). Return the corrected plan in the same JSON format;
set needs_analysis to false if the question cannot be answered from this table.`

// runDataAnalysis asks the model for a plan, runs it, and gives the model one
// chance to repair a rejected query. It returns (nil, nil) when the model
// decides no analysis is needed, and an error when the analysis was attempted
// and could not be completed, so callers can distinguish "not needed" from
// "failed" instead of treating both as silence.
func runDataAnalysis(
	ctx context.Context,
	chatModel chat.Chat,
	executor dataAnalysisExecutor,
	knowledgeID string,
	query string,
	schema *tools.TableSchema,
) (*types.ToolResult, error) {
	formatSchema := utils.GenerateSchema[dataAnalysisPlan]()
	modelCtx := types.WithLLMCallMetadata(ctx, "data_analysis_plan", "")
	messages := []chat.Message{{
		Role: "user",
		Content: fmt.Sprintf(dataAnalysisPlanPromptTemplate,
			query, tools.DataAnalysisTableName, schema.Description(), tools.DataAnalysisTableName),
	}}
	opts := &chat.ChatOptions{Temperature: 0.1, Format: formatSchema}

	const maxAttempts = 2
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		response, err := chatModel.Chat(modelCtx, messages, opts)
		if err != nil {
			return nil, fmt.Errorf("plan generation failed: %w", err)
		}
		plan, err := parseDataAnalysisPlan(response.Content)
		if err != nil {
			return nil, fmt.Errorf("plan is not valid JSON: %w", err)
		}
		if !plan.NeedsAnalysis || strings.TrimSpace(plan.SQL) == "" {
			return nil, nil
		}

		args, err := json.Marshal(tools.DataAnalysisInput{KnowledgeID: knowledgeID, SQL: plan.SQL})
		if err != nil {
			return nil, fmt.Errorf("failed to encode tool arguments: %w", err)
		}
		result, execErr := executor.Execute(ctx, args)
		if execErr == nil && result != nil && result.Success {
			return result, nil
		}
		errText := ""
		switch {
		case result != nil && result.Error != "":
			errText = result.Error
		case execErr != nil:
			errText = execErr.Error()
		default:
			errText = "tool returned no result"
		}
		lastErr = fmt.Errorf("attempt %d: sql=%q: %s", attempt, plan.SQL, errText)
		logger.Warnf(ctx, "[DataAnalysis] %v", lastErr)
		messages = append(messages,
			chat.Message{Role: "assistant", Content: response.Content},
			chat.Message{Role: "user", Content: fmt.Sprintf(
				dataAnalysisRepairPromptTemplate, plan.SQL, errText, tools.DataAnalysisTableName,
			)},
		)
	}
	return nil, lastErr
}

// parseDataAnalysisPlan decodes the model's structured answer, tolerating a
// Markdown code fence around the JSON object.
func parseDataAnalysisPlan(content string) (*dataAnalysisPlan, error) {
	text := strings.TrimSpace(content)
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```")
		text = strings.TrimSuffix(strings.TrimSpace(text), "```")
	}
	var plan dataAnalysisPlan
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &plan); err != nil {
		return nil, err
	}
	return &plan, nil
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
