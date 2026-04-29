package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

var getDocumentInfoTool = BaseTool{
	name: ToolGetDocumentInfo,
	description: `Retrieve detailed metadata information about documents.

## When to Use

Use this tool when:
- Need to understand document basic information (title, type, size, etc.)
- Check if document exists and is available
- Batch query metadata for multiple documents
- Understand document processing status

Do not use when:
- Need document content (use knowledge_search)
- Need specific text chunks (search results already contain full content)


## Returned Information

- Basic info: title, description, source type
- File info: filename, type, size
- Processing status: whether processed, chunk count
- Metadata: custom tags and properties


## Notes

- Concurrent query for multiple documents provides better performance
- Returns complete document metadata, not just title
- Can check document processing status (parse_status)`,
	schema: utils.GenerateSchema[GetDocumentInfoInput](),
}

// GetDocumentInfoInput defines the input parameters for get document info tool
type GetDocumentInfoInput struct {
	KnowledgeIDs []string `json:"knowledge_ids" jsonschema:"Array of document/knowledge IDs, obtained from knowledge_id field in search results, supports concurrent batch queries"`
}

// GetDocumentInfoTool retrieves detailed information about a document/knowledge
type GetDocumentInfoTool struct {
	BaseTool
	knowledgeService interfaces.KnowledgeService
	chunkService     interfaces.ChunkService
	searchTargets    types.SearchTargets // Pre-computed unified search targets with KB-tenant mapping
}

// NewGetDocumentInfoTool creates a new get document info tool
func NewGetDocumentInfoTool(
	knowledgeService interfaces.KnowledgeService,
	chunkService interfaces.ChunkService,
	searchTargets types.SearchTargets,
) *GetDocumentInfoTool {
	return &GetDocumentInfoTool{
		BaseTool:         getDocumentInfoTool,
		knowledgeService: knowledgeService,
		chunkService:     chunkService,
		searchTargets:    searchTargets,
	}
}

// Execute retrieves document information with concurrent processing
func (t *GetDocumentInfoTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	// Parse args from json.RawMessage
	var input GetDocumentInfoInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, err
	}

	// Extract knowledge_ids array
	knowledgeIDs := input.KnowledgeIDs
	if len(knowledgeIDs) == 0 {
		return &types.ToolResult{
			Success: false,
			Error:   "knowledge_ids is required and must be a non-empty array",
		}, fmt.Errorf("knowledge_ids is required")
	}

	// Concurrently get info for each knowledge ID
	type docInfo struct {
		knowledge  *types.Knowledge
		chunkCount int
		err        error
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make(map[string]*docInfo)

	// Concurrently get info for each knowledge ID
	for _, knowledgeID := range knowledgeIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()

			// Get knowledge metadata without tenant filter to support shared KB
			knowledge, err := t.knowledgeService.GetKnowledgeByIDOnly(ctx, id)
			if err != nil {
				mu.Lock()
				results[id] = &docInfo{
					err: fmt.Errorf("failed to get document info: %v", err),
				}
				mu.Unlock()
				return
			}

			// Verify the knowledge's KB is in searchTargets (permission check)
			if !t.searchTargets.ContainsKB(knowledge.KnowledgeBaseID) {
				mu.Lock()
				results[id] = &docInfo{
					err: fmt.Errorf("knowledge base %s is not accessible", knowledge.KnowledgeBaseID),
				}
				mu.Unlock()
				return
			}

			// Use knowledge's actual tenant_id for chunk query (supports cross-tenant shared KB).
			// Keep chunk-type filter aligned with list_knowledge_chunks so the
			// "chunk_count" reported here matches what that tool can page over.
			_, total, err := t.chunkService.GetRepository().
				ListPagedChunksByKnowledgeID(ctx, knowledge.TenantID, id, &types.Pagination{
					Page:     1,
					PageSize: 1,
				}, []types.ChunkType{types.ChunkTypeText, types.ChunkTypeFAQ}, "", "", "", "", "")
			if err != nil {
				mu.Lock()
				results[id] = &docInfo{
					err: fmt.Errorf("failed to get document info: %v", err),
				}
				mu.Unlock()
				return
			}
			chunkCount := int(total)

			mu.Lock()
			results[id] = &docInfo{
				knowledge:  knowledge,
				chunkCount: chunkCount,
			}
			mu.Unlock()
		}(knowledgeID)
	}

	wg.Wait()

	// Collect successful results and errors
	successDocs := make([]*docInfo, 0)
	var errors []string

	for _, knowledgeID := range knowledgeIDs {
		result := results[knowledgeID]
		if result.err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", knowledgeID, result.err))
		} else if result.knowledge != nil {
			successDocs = append(successDocs, result)
		}
	}

	if len(successDocs) == 0 {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to retrieve any document info. Errors: %v", errors),
		}, fmt.Errorf("all document retrievals failed")
	}

	// Format output
	output := "=== Document Info ===\n\n"
	output += fmt.Sprintf("Successfully retrieved %d / %d documents\n\n", len(successDocs), len(knowledgeIDs))

	if len(errors) > 0 {
		output += "=== Partial Failures ===\n"
		for _, errMsg := range errors {
			output += fmt.Sprintf("  - %s\n", errMsg)
		}
		output += "\n"
	}

	formattedDocs := make([]map[string]interface{}, 0, len(successDocs))
	for i, doc := range successDocs {
		k := doc.knowledge

		output += fmt.Sprintf("[Document #%d]\n", i+1)
		output += fmt.Sprintf("  ID:           %s\n", k.ID)
		output += fmt.Sprintf("  Title:        %s\n", k.Title)

		if k.Description != "" {
			output += fmt.Sprintf("  Description:  %s\n", k.Description)
		}

		output += fmt.Sprintf("  Source:       %s\n", formatSource(k.Type, k.Source))

		if k.FileName != "" {
			output += fmt.Sprintf("  File Name:    %s\n", k.FileName)
			output += fmt.Sprintf("  File Type:    %s\n", k.FileType)
			output += fmt.Sprintf("  File Size:    %s\n", formatFileSize(k.FileSize))
		}

		output += fmt.Sprintf("  Parse Status: %s\n", formatParseStatus(k.ParseStatus))
		output += fmt.Sprintf("  Chunk Count:  %d\n", doc.chunkCount)

		if k.Metadata != nil {
			if metadata, err := k.Metadata.Map(); err == nil && len(metadata) > 0 {
				output += "  Metadata:\n"
				for key, value := range metadata {
					output += fmt.Sprintf("    - %s: %v\n", key, value)
				}
			}
		}

		output += "\n"

		formattedDocs = append(formattedDocs, map[string]interface{}{
			"knowledge_id": k.ID,
			"title":        k.Title,
			"description":  k.Description,
			"type":         k.Type,
			"source":       k.Source,
			"file_name":    k.FileName,
			"file_type":    k.FileType,
			"file_size":    k.FileSize,
			"parse_status": k.ParseStatus,
			"chunk_count":  doc.chunkCount,
			"metadata":     k.GetMetadata(),
		})
	}

	// Extract first document title for summary
	var firstTitle string
	if len(successDocs) > 0 && successDocs[0].knowledge != nil {
		firstTitle = successDocs[0].knowledge.Title
	}

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"documents":    formattedDocs,
			"total_docs":   len(successDocs),
			"requested":    len(knowledgeIDs),
			"errors":       errors,
			"display_type": "document_info",
			"title":        firstTitle, // For frontend summary display
		},
	}, nil
}

func formatSource(knowledgeType, source string) string {
	switch knowledgeType {
	case "file":
		return "File Upload"
	case "url":
		return fmt.Sprintf("URL: %s", source)
	case "passage":
		return "Text Input"
	default:
		return knowledgeType
	}
}

func formatFileSize(size int64) string {
	if size == 0 {
		return "Unknown"
	}
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

func formatParseStatus(status string) string {
	switch status {
	case "pending":
		return "Pending"
	case "processing":
		return "Processing"
	case "completed", "success":
		return "Completed"
	case "failed":
		return "Failed"
	default:
		return status
	}
}
