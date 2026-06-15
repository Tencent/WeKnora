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
	description: `Retrieve full chunk content for a document or a single FAQ entry.

## Use After grep_chunks or knowledge_search:
- **FAQ hit** (type faq): list_knowledge_chunks(faq_id="<chunk_id from search>") — reads that one FAQ entry with answers from metadata.
- **Document hit**: list_knowledge_chunks(knowledge_id="<document id>") — pages through all chunks.

## Parameters (provide exactly one id target):
- faq_id (optional): FAQ entry ID from grep_chunks / knowledge_search.
- chunk_id (optional): Single non-FAQ chunk ID (do not use for FAQ — use faq_id).
- knowledge_id (optional): Document/knowledge ID to page through all chunks.
- limit / offset: Only for knowledge_id paging (default limit 20, max 100).

## Output:
Full chunk content. FAQ entries include <faq> with <answer> from metadata.`,
	schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "faq_id": {
      "type": "string",
      "description": "FAQ entry ID (same as chunk_id). Use for FAQ hits instead of knowledge_id."
    },
    "chunk_id": {
      "type": "string",
      "description": "Single chunk ID (alias of faq_id)"
    },
    "knowledge_id": {
      "type": "string",
      "description": "Document/knowledge ID to list all chunks"
    },
    "limit": {
      "type": "integer",
      "description": "Chunks per page when using knowledge_id (default 20, max 100)",
      "default": 20,
      "minimum": 1,
      "maximum": 100
    },
    "offset": {
      "type": "integer",
      "description": "Start position when using knowledge_id (default 0)",
      "default": 0,
      "minimum": 0
    }
  }
}`),
}

// ListKnowledgeChunksInput defines the input parameters for list knowledge chunks tool
type ListKnowledgeChunksInput struct {
	KnowledgeID string `json:"knowledge_id,omitempty"`
	FAQID       string `json:"faq_id,omitempty"`
	ChunkID     string `json:"chunk_id,omitempty"`
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

	chunkID := strings.TrimSpace(input.FAQID)
	if chunkID == "" {
		chunkID = strings.TrimSpace(input.ChunkID)
	}
	if chunkID != "" {
		return t.executeByChunkID(ctx, chunkID)
	}

	knowledgeID := strings.TrimSpace(input.KnowledgeID)
	if knowledgeID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "one of faq_id, chunk_id, or knowledge_id is required",
		}, fmt.Errorf("missing id parameter")
	}

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

		appendFAQChunkData(chunkData, c)
		normalizeFAQChunkDataMap(chunkData, c)

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
			"display_type":    "knowledge_chunks_list",
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

// executeByChunkID loads one chunk by faq_id / chunk_id (FAQ entry or any chunk).
func (t *ListKnowledgeChunksTool) executeByChunkID(ctx context.Context, chunkID string) (*types.ToolResult, error) {
	chunk, err := t.chunkService.GetChunkByIDOnly(ctx, chunkID)
	if err != nil || chunk == nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("chunk not found: %v", err),
		}, err
	}
	if !t.searchTargets.ContainsKB(chunk.KnowledgeBaseID) {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("knowledge base %s is not accessible", chunk.KnowledgeBaseID),
		}, fmt.Errorf("knowledge base not in search targets")
	}

	chunks := []*types.Chunk{chunk}
	if chunk.ImageInfo == "" {
		effectiveTenantID := t.searchTargets.GetTenantIDForKB(chunk.KnowledgeBaseID)
		if effectiveTenantID > 0 {
			infoMap := searchutil.CollectImageInfoByChunkIDs(ctx, t.chunkService.GetRepository(), effectiveTenantID, []string{chunk.ID})
			if merged, ok := infoMap[chunk.ID]; ok {
				chunk.ImageInfo = merged
			}
		}
	}

	knowledgeTitle := t.lookupKnowledgeTitle(ctx, chunk.KnowledgeID)
	output := t.buildOutput(chunk.KnowledgeID, knowledgeTitle, 1, 1, chunks)

	formattedChunks := []map[string]interface{}{
		{
			"seq":            1,
			"chunk_id":       chunk.ID,
			"chunk_index":    chunk.ChunkIndex,
			"content":        chunk.Content,
			"chunk_type":     chunk.ChunkType,
			"knowledge_id":   chunk.KnowledgeID,
			"knowledge_base": chunk.KnowledgeBaseID,
		},
	}
	appendFAQChunkData(formattedChunks[0], chunk)
	normalizeFAQChunkDataMap(formattedChunks[0], chunk)

	data := map[string]interface{}{
		"display_type":    "knowledge_chunks_list",
		"knowledge_id":    chunk.KnowledgeID,
		"knowledge_title": knowledgeTitle,
		"total_chunks":    int64(1),
		"fetched_chunks":  1,
		"page":            1,
		"page_size":       1,
		"chunks":          formattedChunks,
		"faq_id":          chunk.ID,
		"single_chunk":    true,
	}
	if q := faqStandardQuestion(chunk); q != "" {
		data["faq_question"] = q
	}

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data:    data,
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
		if c.ChunkType == types.ChunkTypeFAQ {
			writeFAQEntryXML(&b, c)
			writeChunkImagesXML(&b, c)
			continue
		}

		if q := faqStandardQuestion(c); q != "" {
			fmt.Fprintf(&b, "<chunk chunk_id=\"%s\" chunk_index=\"%d\" type=\"%s\" question=\"%s\">\n",
				c.ID, c.ChunkIndex, c.ChunkType, xmlEscape(q))
		} else {
			fmt.Fprintf(&b, "<chunk chunk_id=\"%s\" chunk_index=\"%d\" type=\"%s\">\n",
				c.ID, c.ChunkIndex, c.ChunkType)
		}
		fmt.Fprintf(&b, "<content>%s</content>\n", summarizeContent(c.Content))
		writeChunkImagesXML(&b, c)
		b.WriteString("</chunk>\n")
	}

	if int64(fetched) < total {
		fmt.Fprintf(&b, "<pagination remaining=\"%d\" />\n", int64(total)-int64(fetched))
	}

	b.WriteString("</knowledge_chunks>")
	return b.String()
}

func writeChunkImagesXML(b *strings.Builder, c *types.Chunk) {
	if c == nil || c.ImageInfo == "" {
		return
	}
	var imageInfos []types.ImageInfo
	if err := json.Unmarshal([]byte(c.ImageInfo), &imageInfos); err != nil || len(imageInfos) == 0 {
		return
	}
	for _, img := range imageInfos {
		if img.URL != "" {
			fmt.Fprintf(b, "<image url=\"%s\">\n", img.URL)
		} else {
			b.WriteString("<image>\n")
		}
		if img.Caption != "" {
			fmt.Fprintf(b, "<image_caption>%s</image_caption>\n", img.Caption)
		}
		if img.OCRText != "" {
			fmt.Fprintf(b, "<image_ocr>%s</image_ocr>\n", img.OCRText)
		}
		b.WriteString("</image>\n")
	}
}

// faqStandardQuestion returns the FAQ standard question for an FAQ-type chunk,
// or "" for non-FAQ chunks (or when metadata is missing/unparseable). All FAQ
// entries inside one knowledge share the same knowledge title, so surfacing the
// standard question gives each entry a distinct, human-readable identity in
// tool output that would otherwise look like duplicate same-titled chunks.
func faqStandardQuestion(c *types.Chunk) string {
	if c == nil || c.ChunkType != types.ChunkTypeFAQ {
		return ""
	}
	meta, err := c.FAQMetadata()
	if err != nil || meta == nil {
		return ""
	}
	return strings.TrimSpace(meta.StandardQuestion)
}

// summarizeContent summarizes the content of a chunk
func summarizeContent(content string) string {
	cleaned := strings.TrimSpace(content)
	if cleaned == "" {
		return "(empty)"
	}

	return strings.TrimSpace(string(cleaned))
}
