package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

var webSearchTool = BaseTool{
	name: ToolWebSearch,
	description: `Search the web for current information and news. This tool searches the internet to find up-to-date information that may not be in the knowledge base.

## CRITICAL - KB First Rule
**ABSOLUTE RULE**: You MUST complete KB retrieval (grep_chunks AND knowledge_search) FIRST before using this tool.
- NEVER use web_search without first trying grep_chunks and knowledge_search
- ONLY use web_search if BOTH grep_chunks AND knowledge_search return insufficient/no results
- KB retrieval is MANDATORY - you CANNOT skip it

## Features
- Real-time web search: Search the internet for current information
- RAG compression: Automatically compresses and extracts relevant content from search results
- Session-scoped caching: Maintains temporary knowledge base for session to avoid re-indexing

## Usage

**Use when**:
- **ONLY after** completing grep_chunks AND knowledge_search
- KB retrieval returned insufficient or no results
- Need current or real-time information (news, events, recent updates)
- Information is not available in knowledge bases
- Need to verify or supplement information from knowledge bases
- Searching for recent developments or trends

**Parameters**:
- query (required): Search query string

**Returns**: Web search results with title, URL, snippet, and content (up to %d results)

## Examples

` + "`" + `
# Search for current information
{
  "query": "latest developments in AI"
}

# Search for recent news
{
  "query": "Python 3.12 release notes"
}
` + "`" + `

## Tips

- Results are automatically compressed using RAG to extract relevant content
- Search results are stored in a temporary knowledge base for the session
- Use this tool when knowledge bases don't have the information you need
- Results include URL, title, snippet, and content snippet (may be truncated)
- **CRITICAL**: If content is truncated or you need full details, use **web_fetch** to fetch complete page content
- Maximum %d results will be returned per search`,
	schema: utils.GenerateSchema[WebSearchInput](),
}

// WebSearchInput defines the input parameters for web search tool
type WebSearchInput struct {
	Query string `json:"query" jsonschema:"Search query string"`
}

// WebSearchTool performs web searches and returns results
type WebSearchTool struct {
	BaseTool
	webSearchService      interfaces.WebSearchService
	knowledgeBaseService  interfaces.KnowledgeBaseService
	knowledgeService      interfaces.KnowledgeService
	webSearchStateService interfaces.WebSearchStateService
	sessionID             string
	maxResults            int
}

// NewWebSearchTool creates a new web search tool
func NewWebSearchTool(
	webSearchService interfaces.WebSearchService,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	webSearchStateService interfaces.WebSearchStateService,
	sessionID string,
	maxResults int,
) *WebSearchTool {
	tool := webSearchTool
	tool.description = fmt.Sprintf(tool.description, maxResults, maxResults)

	return &WebSearchTool{
		BaseTool:              tool,
		webSearchService:      webSearchService,
		knowledgeBaseService:  knowledgeBaseService,
		knowledgeService:      knowledgeService,
		webSearchStateService: webSearchStateService,
		sessionID:             sessionID,
		maxResults:            maxResults,
	}
}

// Execute executes the web search tool
func (t *WebSearchTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][WebSearch] Execute started")

	// Parse args from json.RawMessage
	var input WebSearchInput
	if err := json.Unmarshal(args, &input); err != nil {
		logger.Errorf(ctx, "[Tool][WebSearch] Failed to parse args: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, err
	}

	// Parse query
	query := input.Query
	ok := query != ""
	if !ok || query == "" {
		logger.Errorf(ctx, "[Tool][WebSearch] Query is required")
		return &types.ToolResult{
			Success: false,
			Error:   "query parameter is required",
		}, fmt.Errorf("query parameter is required")
	}

	logger.Infof(ctx, "[Tool][WebSearch] Searching with query: %s, max_results: %d", query, t.maxResults)

	// Get tenant ID from context
	tenantID := uint64(0)
	if tid, ok := ctx.Value(types.TenantIDContextKey).(uint64); ok {
		tenantID = tid
	}

	if tenantID == 0 {
		logger.Errorf(ctx, "[Tool][WebSearch] Tenant ID not found in context")
		return &types.ToolResult{
			Success: false,
			Error:   "tenant ID not found in context",
		}, fmt.Errorf("tenant ID not found in context")
	}

	// Get tenant info from context (same approach as search.go)
	tenant := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if tenant == nil || tenant.WebSearchConfig == nil || tenant.WebSearchConfig.Provider == "" {
		logger.Errorf(ctx, "[Tool][WebSearch] Web search not configured for tenant %d", tenantID)
		return &types.ToolResult{
			Success: false,
			Error:   "web search is not configured for this tenant",
		}, fmt.Errorf("web search is not configured for tenant %d", tenantID)
	}

	// Create a copy of web search config with maxResults from agent config
	searchConfig := *tenant.WebSearchConfig
	searchConfig.MaxResults = t.maxResults

	// Perform web search
	logger.Infof(
		ctx,
		"[Tool][WebSearch] Performing web search with provider: %s, maxResults: %d",
		searchConfig.Provider,
		searchConfig.MaxResults,
	)
	webResults, err := t.webSearchService.Search(ctx, &searchConfig, query)
	if err != nil {
		logger.Errorf(ctx, "[Tool][WebSearch] Web search failed: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("web search failed: %v", err),
		}, fmt.Errorf("web search failed: %w", err)
	}

	logger.Infof(ctx, "[Tool][WebSearch] Web search returned %d results", len(webResults))

	// Apply RAG compression if configured
	if len(webResults) > 0 && tenant.WebSearchConfig.CompressionMethod != "none" &&
		tenant.WebSearchConfig.CompressionMethod != "" {
		// Load session-scoped temp KB state from Redis using WebSearchStateRepository
		tempKBID, seen, ids := t.webSearchStateService.GetWebSearchTempKBState(ctx, t.sessionID)

		// Build questions for RAG compression
		questions := []string{strings.TrimSpace(query)}

		logger.Infof(ctx, "[Tool][WebSearch] Applying RAG compression")
		compressed, kbID, newSeen, newIDs, err := t.webSearchService.CompressWithRAG(
			ctx, t.sessionID, tempKBID, questions, webResults, tenant.WebSearchConfig,
			t.knowledgeBaseService, t.knowledgeService, seen, ids,
		)
		if err != nil {
			logger.Warnf(ctx, "[Tool][WebSearch] RAG compression failed, using raw results: %v", err)
		} else {
			webResults = compressed
			// Persist temp KB state back into Redis using WebSearchStateRepository
			t.webSearchStateService.SaveWebSearchTempKBState(ctx, t.sessionID, kbID, newSeen, newIDs)
			logger.Infof(ctx, "[Tool][WebSearch] RAG compression completed, %d results", len(webResults))
		}
	}

	// Format output
	if len(webResults) == 0 {
		return &types.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("No web search results found for query: %s", query),
			Data: map[string]interface{}{
				"query":   query,
				"results": []interface{}{},
				"count":   0,
			},
		}, nil
	}

	// Build output text
	output := "=== Web Search Results ===\n"
	output += fmt.Sprintf("Query: %s\n", query)
	output += fmt.Sprintf("Found %d result(s)\n\n", len(webResults))

	// Format results
	formattedResults := make([]map[string]interface{}, 0, len(webResults))
	for i, result := range webResults {
		output += fmt.Sprintf("Result #%d:\n", i+1)
		output += fmt.Sprintf("  Title: %s\n", result.Title)
		output += fmt.Sprintf("  URL: %s\n", result.URL)
		if result.Snippet != "" {
			output += fmt.Sprintf("  Snippet: %s\n", result.Snippet)
		}
		if result.Content != "" {
			// Truncate content if too long
			content := result.Content
			if len(content) > 500 {
				content = content[:500] + "..."
			}
			output += fmt.Sprintf("  Content: %s\n", content)
		}
		if result.PublishedAt != nil {
			output += fmt.Sprintf("  Published: %s\n", result.PublishedAt.Format(time.RFC3339))
		}
		output += "\n"

		resultData := map[string]interface{}{
			"result_index": i + 1,
			"title":        result.Title,
			"url":          result.URL,
			"snippet":      result.Snippet,
			"content":      result.Content,
			"source":       result.Source,
		}
		if result.PublishedAt != nil {
			resultData["published_at"] = result.PublishedAt.Format(time.RFC3339)
		}
		formattedResults = append(formattedResults, resultData)
	}

	// Add guidance for next steps
	output += "\n=== Next Steps ===\n"
	if len(webResults) > 0 {
		output += "- ⚠️ Content may be truncated (showing first 500 chars). Use web_fetch to get full page content.\n"
		output += "- Extract URLs from results above and use web_fetch with appropriate prompts to get detailed information.\n"
		output += "- Synthesize information from multiple sources for comprehensive answers.\n"
	} else {
		output += "- No web search results found. Consider:\n"
		output += "  - Try different search queries or keywords\n"
		output += "  - Check if question can be answered from knowledge base instead\n"
		output += "  - Verify if the topic requires real-time information\n"
	}

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"query":        query,
			"results":      formattedResults,
			"count":        len(webResults),
			"display_type": "web_search_results",
		},
	}, nil
}
