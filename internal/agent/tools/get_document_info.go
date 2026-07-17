package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
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
- Can check document processing status (parse_status)

## IDs
- knowledge_ids: regular documents, using the short dN IDs from retrieval results
- faq_ids: individual FAQ entries, using the short cN chunk IDs. Returns the standard question and answers, not the container title.`,
	schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "knowledge_ids": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Short dN document IDs for regular documents"
    },
    "faq_ids": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Short cN FAQ chunk IDs from retrieval results. Use instead of knowledge_ids for a single FAQ Q&A."
    }
  }
}`),
}

// GetDocumentInfoInput defines the input parameters for get document info tool.
// Either knowledge_ids or faq_ids may be provided (at least one); both are optional in the schema.
type GetDocumentInfoInput struct {
	KnowledgeIDs []string `json:"knowledge_ids,omitempty"`
	FAQIDs       []string `json:"faq_ids,omitempty"`
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

	knowledgeIDs := input.KnowledgeIDs
	faqIDs := input.FAQIDs
	if len(knowledgeIDs) == 0 && len(faqIDs) == 0 {
		return &types.ToolResult{
			Success: false,
			Error:   "knowledge_ids or faq_ids is required (non-empty array)",
		}, fmt.Errorf("missing ids")
	}

	type docInfo struct {
		knowledge  *types.Knowledge
		chunk      *types.Chunk
		faqMeta    *types.FAQChunkMetadata
		chunkCount int
		err        error
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make(map[string]*docInfo)

	for _, faqID := range faqIDs {
		faqID = strings.TrimSpace(faqID)
		if faqID == "" {
			continue
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			chunk, err := t.chunkService.GetChunkByIDOnly(ctx, id)
			if err != nil || chunk == nil {
				mu.Lock()
				results["faq:"+id] = &docInfo{err: fmt.Errorf("faq entry not found: %v", err)}
				mu.Unlock()
				return
			}
			if !t.searchTargets.ContainsKB(chunk.KnowledgeBaseID) {
				mu.Lock()
				results["faq:"+id] = &docInfo{err: fmt.Errorf("knowledge base %s is not accessible", chunk.KnowledgeBaseID)}
				mu.Unlock()
				return
			}
			allowed, scopeErr := searchTargetsAllowKnowledgeID(ctx, t.searchTargets, chunk.KnowledgeID, chunk.KnowledgeBaseID, t.knowledgeService)
			if scopeErr != nil || !allowed {
				mu.Lock()
				if scopeErr != nil {
					results["faq:"+id] = &docInfo{err: fmt.Errorf("failed to validate FAQ scope: %v", scopeErr)}
				} else {
					results["faq:"+id] = &docInfo{err: fmt.Errorf("FAQ entry %s is not within the current @mention scope", id)}
				}
				mu.Unlock()
				return
			}
			var meta *types.FAQChunkMetadata
			if chunk.ChunkType == types.ChunkTypeFAQ {
				meta, _ = chunk.FAQMetadata()
			}
			mu.Lock()
			results["faq:"+id] = &docInfo{chunk: chunk, faqMeta: meta, chunkCount: 1}
			mu.Unlock()
		}(faqID)
	}

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
			allowed, scopeErr := searchTargetsAllowKnowledgeID(ctx, t.searchTargets, knowledge.ID, knowledge.KnowledgeBaseID, t.knowledgeService)
			if scopeErr != nil || !allowed {
				mu.Lock()
				if scopeErr != nil {
					results[id] = &docInfo{err: fmt.Errorf("failed to validate document scope: %v", scopeErr)}
				} else {
					results[id] = &docInfo{err: fmt.Errorf("document %s is not within the current @mention scope", knowledge.ID)}
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

	requested := len(knowledgeIDs) + len(faqIDs)
	successDocs := make([]*docInfo, 0)
	var errors []string

	for _, knowledgeID := range knowledgeIDs {
		result := results[knowledgeID]
		if result == nil {
			errors = append(errors, fmt.Sprintf("%s: not found", knowledgeID))
			continue
		}
		if result.err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", knowledgeID, result.err))
		} else if result.knowledge != nil {
			successDocs = append(successDocs, result)
		}
	}
	for _, faqID := range faqIDs {
		faqID = strings.TrimSpace(faqID)
		if faqID == "" {
			continue
		}
		result := results["faq:"+faqID]
		if result == nil {
			errors = append(errors, fmt.Sprintf("faq:%s: not found", faqID))
			continue
		}
		if result.err != nil {
			errors = append(errors, fmt.Sprintf("faq:%s: %v", faqID, result.err))
		} else if result.chunk != nil {
			successDocs = append(successDocs, result)
		}
	}

	if len(successDocs) == 0 {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to retrieve any document info. Errors: %v", errors),
		}, fmt.Errorf("all document retrievals failed")
	}

	output := "=== Document Info ===\n\n"
	output += fmt.Sprintf("Successfully retrieved %d / %d entries\n\n", len(successDocs), requested)

	if len(errors) > 0 {
		output += "=== Partial Failures ===\n"
		for _, errMsg := range errors {
			output += fmt.Sprintf("  - %s\n", errMsg)
		}
		output += "\n"
	}

	formattedDocs := make([]map[string]interface{}, 0, len(successDocs))
	for i, doc := range successDocs {
		output += fmt.Sprintf("[Entry #%d]\n", i+1)

		if doc.chunk != nil {
			formatted := formatFAQEntryInfo(&output, doc.chunk, doc.faqMeta)
			formattedDocs = append(formattedDocs, formatted)
			continue
		}

		k := doc.knowledge
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
			"is_faq":       false,
		})
	}

	var firstTitle string
	if len(formattedDocs) > 0 {
		if t, ok := formattedDocs[0]["title"].(string); ok {
			firstTitle = t
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"documents":    formattedDocs,
			"total_docs":   len(successDocs),
			"requested":    requested,
			"errors":       errors,
			"display_type": "document_info",
			"title":        firstTitle,
		},
	}, nil
}

func formatFAQEntryInfo(output *string, chunk *types.Chunk, meta *types.FAQChunkMetadata) map[string]interface{} {
	title := faqStandardQuestion(chunk)
	if title == "" && meta != nil {
		title = strings.TrimSpace(meta.StandardQuestion)
	}
	if title == "" {
		title = "FAQ Entry"
	}

	*output += fmt.Sprintf("  FAQ ID:       %s\n", chunk.ID)
	*output += fmt.Sprintf("  Question:     %s\n", title)
	if chunk.KnowledgeID != "" {
		*output += fmt.Sprintf("  Container ID: %s\n", chunk.KnowledgeID)
	}
	if meta != nil && len(meta.Answers) > 0 {
		*output += "  Answers:\n"
		for _, ans := range meta.Answers {
			*output += fmt.Sprintf("    - %s\n", ans)
		}
	}
	if meta != nil && len(meta.SimilarQuestions) > 0 {
		display, omitted := truncateSimilarQuestionsForDisplay(meta.SimilarQuestions)
		*output += "  Similar Questions:\n"
		for _, sq := range display {
			*output += fmt.Sprintf("    - %s\n", sq)
		}
		if omitted > 0 {
			*output += fmt.Sprintf("    ... and %d more omitted\n", omitted)
		}
	}
	*output += "\n"

	entry := map[string]interface{}{
		"faq_id":       chunk.ID,
		"knowledge_id": chunk.KnowledgeID,
		"title":        title,
		"faq_question": title,
		"type":         "faq",
		"is_faq":       true,
		"chunk_count":  1,
	}
	if meta != nil {
		if len(meta.Answers) > 0 {
			entry["faq_answers"] = meta.Answers
		}
		appendSimilarQuestionsToChunkData(entry, meta.SimilarQuestions)
	}
	return entry
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
