package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var listKnowledgeChunksTool = BaseTool{
	name: ToolListKnowledgeChunks,
	description: `Retrieve full chunk content for a document by knowledge_id.

## Use After grep_chunks or knowledge_search:
1. grep_chunks(["keyword", "variant"]) → get knowledge_id
2. list_knowledge_chunks(knowledge_id) → read full content

## When to Use:
- Need full content of chunks from a known document
- Want to see context around specific chunks
- Check how many chunks a document has

## Parameters:
- knowledge_id (required): Document ID
- limit (optional): Chunks per page (default 20, max 100)
- offset (optional): 0-based position in the chunk list (NOT chunk_index).
  Must be < total. Use offset=0 to read from the first chunk, offset=N to
  skip the first N chunks. If offset >= total the tool returns an explicit
  out-of-range error with the valid total so you can retry.

## Output:
Full chunk content with chunk_id, chunk_index, and content text.
The response attributes include:
- total: total number of chunks in the document
- fetched: how many chunks this page returned
If fetched=0 with total>0, your offset is past the end — retry with a
smaller offset (e.g. offset = max(0, total - limit)).`,
	schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "knowledge_id": {
      "type": "string",
      "description": "Document ID to retrieve chunks from"
    },
    "limit": {
      "type": "integer",
      "description": "Chunks per page (default 20, max 100)",
      "default": 20,
      "minimum": 1,
      "maximum": 100
    },
    "offset": {
      "type": "integer",
      "description": "Start position (default 0)",
      "default": 0,
      "minimum": 0
    }
  },
  "required": ["knowledge_id"]
}`),
}

// ListKnowledgeChunksInput defines the input parameters for list knowledge chunks tool
type ListKnowledgeChunksInput struct {
	KnowledgeID string `json:"knowledge_id"`
	Limit       int    `json:"limit"`
	Offset      int    `json:"offset"`
}

// ListKnowledgeChunksTool retrieves chunk snapshots for a specific knowledge document.
type ListKnowledgeChunksTool struct {
	BaseTool
	chunkService     interfaces.ChunkService
	knowledgeService interfaces.KnowledgeService
	searchTargets    types.SearchTargets // Pre-computed unified search targets with KB-tenant mapping
}

// NewListKnowledgeChunksTool creates a new tool instance.
func NewListKnowledgeChunksTool(
	knowledgeService interfaces.KnowledgeService,
	chunkService interfaces.ChunkService,
	searchTargets types.SearchTargets,
) *ListKnowledgeChunksTool {
	return &ListKnowledgeChunksTool{
		BaseTool:         listKnowledgeChunksTool,
		chunkService:     chunkService,
		knowledgeService: knowledgeService,
		searchTargets:    searchTargets,
	}
}

// Execute performs the chunk fetch against the chunk service.
func (t *ListKnowledgeChunksTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	// Parse args from json.RawMessage
	var input ListKnowledgeChunksInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, err
	}

	knowledgeID := input.KnowledgeID
	ok := knowledgeID != ""
	if !ok || strings.TrimSpace(knowledgeID) == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "knowledge_id is required",
		}, fmt.Errorf("knowledge_id is required")
	}
	knowledgeID = strings.TrimSpace(knowledgeID)

	// Get knowledge info without tenant filter to support shared KB
	knowledge, err := t.knowledgeService.GetKnowledgeByIDOnly(ctx, knowledgeID)
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Knowledge not found: %v", err),
		}, err
	}

	// Verify the knowledge's KB is in searchTargets (permission check)
	if !t.searchTargets.ContainsKB(knowledge.KnowledgeBaseID) {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Knowledge base %s is not accessible", knowledge.KnowledgeBaseID),
		}, fmt.Errorf("knowledge base not in search targets")
	}

	// Use the knowledge's actual tenant_id for chunk query (supports cross-tenant shared KB)
	effectiveTenantID := knowledge.TenantID

	chunkLimit := 20
	if input.Limit > 0 {
		chunkLimit = input.Limit
	}
	offset := 0
	if input.Offset > 0 {
		offset = input.Offset
	}
	if offset < 0 {
		offset = 0
	}

	pagination := &types.Pagination{
		Page:     offset/chunkLimit + 1,
		PageSize: chunkLimit,
	}

	chunks, total, err := t.chunkService.GetRepository().ListPagedChunksByKnowledgeID(ctx,
		effectiveTenantID, knowledgeID, pagination, []types.ChunkType{types.ChunkTypeText, types.ChunkTypeFAQ}, "", "", "", "", "")
	if err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to list chunks: %v", err),
		}, err
	}
	if chunks == nil {
		return &types.ToolResult{
			Success: false,
			Error:   "chunk query returned no data",
		}, fmt.Errorf("chunk query returned no data")
	}

	totalChunks := total
	fetched := len(chunks)

	// Explicit out-of-range guidance: when the caller paged past the end
	// (offset >= total with total > 0), silently returning fetched=0 is
	// confusing for LLMs that just saw the document in search results. Tell
	// them exactly what happened and what offset would be valid so the next
	// call lands on a real page.
	if fetched == 0 && totalChunks > 0 && int64(offset) >= totalChunks {
		suggestedOffset := totalChunks - int64(chunkLimit)
		if suggestedOffset < 0 {
			suggestedOffset = 0
		}
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"offset %d is out of range: document has only %d chunks (valid offset range: 0..%d). Retry with offset=%d (or any value < %d).",
				offset, totalChunks, totalChunks-1, suggestedOffset, totalChunks,
			),
			Data: map[string]interface{}{
				"knowledge_id":     knowledgeID,
				"total_chunks":     totalChunks,
				"requested_offset": offset,
				"requested_limit":  chunkLimit,
				"suggested_offset": suggestedOffset,
			},
		}, nil
	}

	// Enrich image info from child image chunks (lazy loading)
	if fetched > 0 {
		chunkIDs := make([]string, 0, fetched)
		for _, c := range chunks {
			chunkIDs = append(chunkIDs, c.ID)
		}
		infoMap := searchutil.CollectImageInfoByChunkIDs(ctx, t.chunkService.GetRepository(), effectiveTenantID, chunkIDs)
		for _, c := range chunks {
			if c.ImageInfo == "" {
				if merged, ok := infoMap[c.ID]; ok {
					c.ImageInfo = merged
				}
			}
		}
	}

	knowledgeTitle := t.lookupKnowledgeTitle(ctx, knowledgeID)

	output := t.buildOutput(knowledgeID, knowledgeTitle, totalChunks, fetched, chunks)

	formattedChunks := make([]map[string]interface{}, 0, len(chunks))
	for idx, c := range chunks {
		chunkData := map[string]interface{}{
			"seq":             idx + 1,
			"chunk_id":        c.ID,
			"chunk_index":     c.ChunkIndex,
			"content":         c.Content,
			"chunk_type":      c.ChunkType,
			"knowledge_id":    c.KnowledgeID,
			"knowledge_base":  c.KnowledgeBaseID,
			"start_at":        c.StartAt,
			"end_at":          c.EndAt,
			"parent_chunk_id": c.ParentChunkID,
		}

		// 添加图片信息
		if c.ImageInfo != "" {
			var imageInfos []types.ImageInfo
			if err := json.Unmarshal([]byte(c.ImageInfo), &imageInfos); err == nil && len(imageInfos) > 0 {
				imageList := make([]map[string]string, 0, len(imageInfos))
				for _, img := range imageInfos {
					imgData := make(map[string]string)
					if img.URL != "" {
						imgData["url"] = img.URL
					}
					if img.Caption != "" {
						imgData["caption"] = img.Caption
					}
					if img.OCRText != "" {
						imgData["ocr_text"] = img.OCRText
					}
					if len(imgData) > 0 {
						imageList = append(imageList, imgData)
					}
				}
				if len(imageList) > 0 {
					chunkData["images"] = imageList
				}
			}
		}

		formattedChunks = append(formattedChunks, chunkData)
	}

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"knowledge_id":    knowledgeID,
			"knowledge_title": knowledgeTitle,
			"total_chunks":    totalChunks,
			"fetched_chunks":  fetched,
			"page":            pagination.Page,
			"page_size":       pagination.PageSize,
			"chunks":          formattedChunks,
		},
	}, nil
}

// lookupKnowledgeTitle looks up the title of a knowledge document
// Uses GetKnowledgeByIDOnly to support cross-tenant shared KB
func (t *ListKnowledgeChunksTool) lookupKnowledgeTitle(ctx context.Context, knowledgeID string) string {
	if t.knowledgeService == nil {
		return ""
	}
	knowledge, err := t.knowledgeService.GetKnowledgeByIDOnly(ctx, knowledgeID)
	if err != nil || knowledge == nil {
		return ""
	}
	return strings.TrimSpace(knowledge.Title)
}

// buildOutput builds the output as XML for the list knowledge chunks tool
func (t *ListKnowledgeChunksTool) buildOutput(
	knowledgeID string,
	knowledgeTitle string,
	total int64,
	fetched int,
	chunks []*types.Chunk,
) string {
	var b strings.Builder

	titleAttr := ""
	if knowledgeTitle != "" {
		titleAttr = fmt.Sprintf(" title=\"%s\"", knowledgeTitle)
	}
	fmt.Fprintf(&b, "<knowledge_chunks knowledge_id=\"%s\"%s total=\"%d\" fetched=\"%d\">\n",
		knowledgeID, titleAttr, total, fetched)

	if fetched == 0 {
		b.WriteString("</knowledge_chunks>")
		return b.String()
	}

	for _, c := range chunks {
		fmt.Fprintf(&b, "<chunk chunk_id=\"%s\" chunk_index=\"%d\" type=\"%s\">\n",
			c.ID, c.ChunkIndex, c.ChunkType)
		fmt.Fprintf(&b, "<content>%s</content>\n", summarizeContent(c.Content))

		if c.ImageInfo != "" {
			var imageInfos []types.ImageInfo
			if err := json.Unmarshal([]byte(c.ImageInfo), &imageInfos); err == nil && len(imageInfos) > 0 {
				for _, img := range imageInfos {
					if img.URL != "" {
						fmt.Fprintf(&b, "<image url=\"%s\">\n", img.URL)
					} else {
						b.WriteString("<image>\n")
					}
					if img.Caption != "" {
						fmt.Fprintf(&b, "<image_caption>%s</image_caption>\n", img.Caption)
					}
					if img.OCRText != "" {
						fmt.Fprintf(&b, "<image_ocr>%s</image_ocr>\n", img.OCRText)
					}
					b.WriteString("</image>\n")
				}
			}
		}

		b.WriteString("</chunk>\n")
	}

	if int64(fetched) < total {
		fmt.Fprintf(&b, "<pagination remaining=\"%d\" />\n", int64(total)-int64(fetched))
	}

	b.WriteString("</knowledge_chunks>")
	return b.String()
}

// summarizeContent summarizes the content of a chunk
func summarizeContent(content string) string {
	cleaned := strings.TrimSpace(content)
	if cleaned == "" {
		return "(empty)"
	}

	return strings.TrimSpace(string(cleaned))
}
