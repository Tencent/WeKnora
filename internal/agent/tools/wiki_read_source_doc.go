package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type wikiReadSourceDocTool struct {
	BaseTool
	knowledgeService interfaces.KnowledgeService
	chunkService     interfaces.ChunkService
}

func NewWikiReadSourceDocTool(knowledgeService interfaces.KnowledgeService, chunkService interfaces.ChunkService) types.Tool {
	return &wikiReadSourceDocTool{
		BaseTool: NewBaseTool(
			ToolWikiReadSourceDoc,
			`Read or search within a specific source document to drill down for details omitted from the wiki.
Provide the knowledge_id from the <sources> block.
You can EITHER search using a regex query OR fetch a specific contiguous range of chunks using start_chunk_index and end_chunk_index (useful for expanding context around a known chunk).
If neither query nor range is provided, it returns the beginning of the document.`,
			json.RawMessage(`{
  "type": "object",
  "properties": {
    "knowledge_id": {
      "type": "string",
      "description": "The ID of the source document to read"
    },
    "query": {
      "type": "string",
      "description": "Optional: A regex query to filter the document chunks. Use this to find specific quotes or details efficiently. Remember to double-escape backslashes for JSON: write \"C\\\\+\\\\+\" (NOT \"C\\+\\+\") and \"\\\\d+\" (NOT \"\\d+\")."
    },
    "start_chunk_index": {
      "type": "integer",
      "description": "Optional: The starting chunk index (1-based) to read a specific range."
    },
    "end_chunk_index": {
      "type": "integer",
      "description": "Optional: The ending chunk index (1-based) to read a specific range. Must be >= start_chunk_index."
    }
  },
  "required": ["knowledge_id"]
}`),
		),
		knowledgeService: knowledgeService,
		chunkService:     chunkService,
	}
}

// enrichChunkImageInfo populates chunk.ImageInfo for a batch of parent text
// chunks by looking up their image_ocr / image_caption children. Chunks that
// already have a non-empty ImageInfo are left untouched.
func enrichChunkImageInfo(
	ctx context.Context,
	chunkRepo interfaces.ChunkRepository,
	tenantID uint64,
	chunks []*types.Chunk,
) {
	if len(chunks) == 0 || chunkRepo == nil {
		return
	}
	ids := make([]string, 0, len(chunks))
	for _, c := range chunks {
		if c.ImageInfo == "" && c.ID != "" {
			ids = append(ids, c.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	infoMap := searchutil.CollectImageInfoByChunkIDs(ctx, chunkRepo, tenantID, ids)
	if len(infoMap) == 0 {
		return
	}
	for _, c := range chunks {
		if c.ImageInfo != "" {
			continue
		}
		if merged, ok := infoMap[c.ID]; ok && merged != "" {
			c.ImageInfo = merged
		}
	}
}

func enrichChunkContent(c *types.Chunk) string {
	content := c.Content
	if c.ImageInfo != "" {
		var imgInfos []types.ImageInfo
		if err := json.Unmarshal([]byte(c.ImageInfo), &imgInfos); err == nil && len(imgInfos) > 0 {
			var imgBuilder strings.Builder
			for _, img := range imgInfos {
				if img.URL != "" {
					imgBuilder.WriteString(fmt.Sprintf("\n<image url=\"%s\">\n", img.URL))
					if img.Caption != "" {
						imgBuilder.WriteString(fmt.Sprintf("<caption>%s</caption>\n", img.Caption))
					}
					if img.OCRText != "" {
						imgBuilder.WriteString(fmt.Sprintf("<ocr_text>%s</ocr_text>\n", img.OCRText))
					}
					imgBuilder.WriteString("</image>")
				}
			}
			content += imgBuilder.String()
		}
	}
	return content
}

func (t *wikiReadSourceDocTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var params struct {
		KnowledgeID     string `json:"knowledge_id"`
		Query           string `json:"query"`
		StartChunkIndex int    `json:"start_chunk_index"`
		EndChunkIndex   int    `json:"end_chunk_index"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Invalid parameters: " + err.Error()}, nil
	}

	knowledgeID := strings.TrimSpace(params.KnowledgeID)
	if knowledgeID == "" {
		return &types.ToolResult{Success: false, Error: "knowledge_id is required"}, nil
	}

	knowledge, err := t.knowledgeService.GetKnowledgeByIDOnly(ctx, knowledgeID)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("Document not found: %v", err)}, nil
	}

	var sb strings.Builder
	sb.WriteString("<source_document>\n<metadata>\n")
	sb.WriteString(fmt.Sprintf("<title>%s</title>\n", knowledge.Title))
	sb.WriteString(fmt.Sprintf("<knowledge_id>%s</knowledge_id>\n", knowledgeID))

	hasRange := params.StartChunkIndex > 0
	var re *regexp.Regexp

	if hasRange {
		if params.EndChunkIndex < params.StartChunkIndex {
			params.EndChunkIndex = params.StartChunkIndex + 10 // Default window
		}
		if params.EndChunkIndex-params.StartChunkIndex > 50 {
			params.EndChunkIndex = params.StartChunkIndex + 50 // Max 50 chunks to prevent bloat
		}
		sb.WriteString(fmt.Sprintf("<chunk_range start=\"%d\" end=\"%d\"/>\n", params.StartChunkIndex, params.EndChunkIndex))
	} else if params.Query != "" {
		compiled, err := regexp.Compile("(?i)" + params.Query)
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("Invalid regex query '%s': %v", params.Query, err)}, nil
		}
		re = compiled
		sb.WriteString(fmt.Sprintf("<query>%s</query>\n", params.Query))
	}

	matchCount := 0
	pageSize := 100
	page := 1
	if hasRange {
		page = (params.StartChunkIndex - 1) / pageSize + 1
	}

	var chunksOutput strings.Builder
	totalChunks := int64(0)
	reachedMax := false

	var prevChunk *types.Chunk
	var forceOutputNext bool
	outputtedIndices := make(map[int]bool)

	for {
		pagination := &types.Pagination{
			Page:     page,
			PageSize: pageSize,
		}

		chunks, total, err := t.chunkService.GetRepository().ListPagedChunksByKnowledgeID(
			ctx,
			knowledge.TenantID,
			knowledgeID,
			pagination,
			[]types.ChunkType{types.ChunkTypeText, types.ChunkTypeFAQ},
			"", "", "", "", "",
		)
		if err != nil {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("Failed to list chunks: %v", err)}, nil
		}

		if page == 1 {
			totalChunks = total
		}

		if len(chunks) == 0 {
			break
		}

		// Lazily enrich ImageInfo from image_ocr / image_caption child chunks.
		// Parent text chunks don't carry image_info themselves (see
		// image_multimodal.go); without this the source-doc reader would
		// silently drop OCR text and captions for image-heavy documents.
		enrichChunkImageInfo(ctx, t.chunkService.GetRepository(), knowledge.TenantID, chunks)

		for _, c := range chunks {
			chunkNum := c.ChunkIndex + 1
			chunkContent := enrichChunkContent(c)

			if hasRange {
				if chunkNum < params.StartChunkIndex {
					continue
				}
				if chunkNum > params.EndChunkIndex {
					reachedMax = true
					break
				}
				fmt.Fprintf(&chunksOutput, "<chunk index=\"%d\" type=\"range\">\n%s\n</chunk>\n", chunkNum, chunkContent)
				matchCount++
				continue
			}

			isMatch := false
			if re != nil {
				isMatch = re.MatchString(chunkContent)
			} else {
				isMatch = true
			}

			if isMatch {
				matchCount++

				if re != nil {
					// Output previous chunk for context
					if prevChunk != nil && !outputtedIndices[prevChunk.ChunkIndex] {
						prevContent := enrichChunkContent(prevChunk)
						fmt.Fprintf(&chunksOutput, "<chunk index=\"%d\" type=\"context_before\">\n%s\n</chunk>\n", prevChunk.ChunkIndex+1, prevContent)
						outputtedIndices[prevChunk.ChunkIndex] = true
					}
				}

				if !outputtedIndices[c.ChunkIndex] {
					matchAttr := ""
					if re != nil {
						matchAttr = ` type="match"`
					}
					fmt.Fprintf(&chunksOutput, "<chunk index=\"%d\"%s>\n%s\n</chunk>\n", c.ChunkIndex+1, matchAttr, chunkContent)
					outputtedIndices[c.ChunkIndex] = true
				}

				if re != nil {
					forceOutputNext = true
				}
			} else if forceOutputNext {
				if !outputtedIndices[c.ChunkIndex] {
					fmt.Fprintf(&chunksOutput, "<chunk index=\"%d\" type=\"context_after\">\n%s\n</chunk>\n", c.ChunkIndex+1, chunkContent)
					outputtedIndices[c.ChunkIndex] = true
				}
				forceOutputNext = false
			}

			prevChunk = c

			if re == nil && matchCount >= 10 {
				break
			}
			if re != nil && matchCount >= 20 {
				break
			}
		}

		if hasRange && reachedMax {
			break
		}

		if !hasRange {
			if re == nil && matchCount >= 10 {
				break
			}
			if re != nil && matchCount >= 20 {
				reachedMax = true
				break
			}
		}

		if int64(page*pageSize) >= total {
			break
		}
		page++
	}

	sb.WriteString(fmt.Sprintf("<total_chunks>%d</total_chunks>\n</metadata>\n", totalChunks))
	
	if matchCount > 0 {
		sb.WriteString(fmt.Sprintf("<chunks count=\"%d\">\n", matchCount))
		sb.WriteString(chunksOutput.String())
		sb.WriteString("</chunks>\n")
	} else {
		sb.WriteString("<chunks count=\"0\" />\n")
	}

	if reachedMax {
		sb.WriteString("<message>Reached maximum limit for fetching chunks in a single call. Please refine your query or range if needed.</message>\n")
	} else if matchCount == 0 {
		if hasRange {
			sb.WriteString("<message>No chunks found in the specified range.</message>\n")
		} else if re != nil {
			sb.WriteString("<message>No chunks matched your query in this document.</message>\n")
		} else {
			sb.WriteString("<message>Document has no text chunks available.</message>\n")
		}
	} else if !hasRange && re == nil {
		sb.WriteString("<message>No query or range provided. Showing the first 10 chunks as a preview.</message>\n")
	}

	sb.WriteString("</source_document>")

	return &types.ToolResult{Success: true, Output: sb.String()}, nil
}
