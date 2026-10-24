package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

var dataSchemaTool = BaseTool{
	name: ToolDataSchema,
	description: "Use this tool to get the schema information of a CSV or Excel file loaded into DuckDB, " +
		"including a spreadsheet uploaded in the chat. Pass the document's knowledge_id handle from " +
		"runtime_context (chat_uploads for a chat upload). It returns the columns and row count. " +
		"When querying the document with data_analysis, reference it as the table \"" + DataAnalysisTableName + "\". " +
		"Do not substitute a different document when the user uploaded a spreadsheet.",
	schema: utils.GenerateSchema[DataSchemaInput](),
}

type DataSchemaInput struct {
	KnowledgeID string `json:"knowledge_id" jsonschema:"doc://N document handle to query"`
}

type DataSchemaTool struct {
	BaseTool
	knowledgeService interfaces.KnowledgeService
	chunkRepo        interfaces.ChunkRepository
	targetChunkTypes []types.ChunkType
	searchTargets    types.SearchTargets
	scopeEnforced    bool
	// analysis loads chat-uploaded spreadsheets. Those files never produce
	// ChunkTypeTableSummary / ChunkTypeTableColumn chunks, so their schema is
	// the DuckDB table description. The pointer is the same DataAnalysisTool
	// registered for queries, so the table is not created twice.
	analysis *DataAnalysisTool
}

// WithSearchTargets enables Agent request-scope authorization. An Agent turn
// with no search target must reject every document rather than fall back to
// the unrestricted service-owned lookup.
func (t *DataSchemaTool) WithSearchTargets(searchTargets types.SearchTargets) *DataSchemaTool {
	t.searchTargets = searchTargets
	t.scopeEnforced = true
	return t
}

// WithDataAnalysis shares the query tool so a chat upload's DuckDB table is
// created once and reused by data_analysis.
func (t *DataSchemaTool) WithDataAnalysis(analysis *DataAnalysisTool) *DataSchemaTool {
	if t != nil {
		t.analysis = analysis
	}
	return t
}

// ChatAttachments returns the chat-upload allowlist shared with data_analysis.
func (t *DataSchemaTool) ChatAttachments() *ChatTabularAttachments {
	if t == nil || t.analysis == nil {
		return nil
	}
	return t.analysis.ChatAttachments()
}

// Cleanup drops DuckDB tables when this tool owns the analysis loader.
// When data_analysis is also registered it cleans the same object; a second
// pass is a no-op.
func (t *DataSchemaTool) Cleanup(ctx context.Context) {
	if t != nil && t.analysis != nil {
		t.analysis.Cleanup(ctx)
	}
}

func NewDataSchemaTool(knowledgeService interfaces.KnowledgeService, chunkRepo interfaces.ChunkRepository, targetChunkTypes ...types.ChunkType) *DataSchemaTool {
	if len(targetChunkTypes) == 0 {
		targetChunkTypes = []types.ChunkType{types.ChunkTypeTableSummary, types.ChunkTypeTableColumn}
	}
	return &DataSchemaTool{
		BaseTool:         dataSchemaTool,
		knowledgeService: knowledgeService,
		chunkRepo:        chunkRepo,
		targetChunkTypes: targetChunkTypes,
	}
}

// Execute executes the tool logic
func (t *DataSchemaTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var input DataSchemaInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse input args: %v", err),
		}, err
	}

	// Chat uploads are not knowledge rows and have no table-summary chunks.
	// Describe the file from DuckDB instead of failing closed on missing chunks.
	if t.analysis != nil {
		if attachment, ok := t.analysis.chatAttachment(input.KnowledgeID); ok {
			return t.executeChatUpload(ctx, attachment)
		}
	}

	// Get knowledge to get TenantID (use IDOnly to support cross-tenant shared KB)
	var knowledge *types.Knowledge
	var err error
	if t.scopeEnforced {
		knowledge, err = authorizeKnowledgeInSearchTargets(ctx, t.searchTargets, input.KnowledgeID, t.knowledgeService)
	} else {
		knowledge, err = t.knowledgeService.GetKnowledgeByIDOnly(ctx, input.KnowledgeID)
	}
	if err != nil || knowledge == nil {
		if err == nil {
			err = fmt.Errorf("knowledge service returned an empty result")
		}
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to get knowledge '%s': %v", input.KnowledgeID, err),
		}, err
	}

	// Get chunks for the knowledge ID using ChunkRepository
	// We only need table summary and column chunks
	chunkTypes := t.targetChunkTypes
	page := &types.Pagination{
		Page:     1,
		PageSize: 100, // Should be enough for schema chunks
	}
	enabled := true

	chunks, _, err := t.chunkRepo.ListPagedChunksByKnowledgeID(
		ctx,
		knowledge.TenantID,
		input.KnowledgeID,
		page,
		chunkTypes,
		nil, // tagIDs
		"",  // keyword
		"",  // searchField
		"",  // sortOrder
		"",  // knowledgeType
		&enabled,
	)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to list chunks for knowledge ID '%s': %v", input.KnowledgeID, err),
		}, err
	}

	var summaryContent, columnContent string
	for _, chunk := range chunks {
		if chunk.ChunkType == types.ChunkTypeTableSummary {
			summaryContent = chunk.Content
		} else if chunk.ChunkType == types.ChunkTypeTableColumn {
			columnContent = chunk.Content
		}
	}

	if summaryContent == "" || columnContent == "" {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("No table schema information found for knowledge ID '%s'", input.KnowledgeID),
		}, fmt.Errorf("no schema info found")
	}

	output := fmt.Sprintf(
		"%s\n\n%s\n\nQuery this document with data_analysis as the table %q.",
		summaryContent, columnContent, DataAnalysisTableName,
	)

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"summary": summaryContent,
			"columns": columnContent,
		},
	}, nil
}

func (t *DataSchemaTool) executeChatUpload(ctx context.Context, attachment ChatTabularAttachment) (*types.ToolResult, error) {
	schema, err := t.analysis.loadChatAttachment(ctx, attachment)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to read chat upload '%s': %v", attachment.FileName, err),
		}, err
	}
	// Knowledge-base spreadsheets publish ChunkTypeTableSummary and
	// ChunkTypeTableColumn during ingest. A chat upload is only a temporary
	// document, so the schema the model sees is the one DuckDB reports after
	// loading the original file.
	columns := schema.Description()
	output := fmt.Sprintf(
		"Schema for the chat-uploaded %s file %q. This file was not ingested into the knowledge base, so the schema is derived from the file itself.\n\n%s\n\nQuery this document with data_analysis as the table %q.",
		attachment.FileType, attachment.FileName, columns, DataAnalysisTableName,
	)
	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"summary":   output,
			"columns":   columns,
			"source":    "chat_upload",
			"file_type": attachment.FileType,
		},
	}, nil
}
