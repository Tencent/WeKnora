package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"

	webfetch "github.com/Tencent/WeKnora/internal/infrastructure/web_fetch"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

var webFetchTool = BaseTool{
	name: ToolWebFetch,
	description: `Fetch detailed content from web pages returned by web_search and analyze it with an LLM.

## Usage
- Receive one or more {url: "wN", prompt} combinations; the field name stays url, but its value is the short page ID
- Fetch each page independently and return a structured status for every URL
- Successful pages remain usable when other pages fail

## When to Use
- Use when a search snippet is insufficient or a claim needs full-page verification
- web_search titles, URLs, and snippets remain usable evidence if fetching fails
- Do not repeatedly fetch a non-retryable URL or expand searches after all fetches fail
- When page verification is unavailable, answer from search summaries, disclose that limitation, and lower confidence for dynamic facts`,
	schema: utils.GenerateSchema[WebFetchInput](),
}

// WebFetchInput defines the input parameters for web fetch tool.
type WebFetchInput struct {
	Items []WebFetchItem `json:"items" jsonschema:"Batch fetch tasks, each containing a short wN web page ID and prompt"`
}

// WebFetchItem represents a single web fetch task.
type WebFetchItem struct {
	URL    string `json:"url" jsonschema:"Short wN web page ID from web_search results"`
	Prompt string `json:"prompt" jsonschema:"Prompt for analyzing the fetched web page content"`
}

type webContentFetcher interface {
	Fetch(context.Context, string) (string, error)
}

type webFetchItemResult struct {
	output string
	data   map[string]interface{}
	status string
}

// WebFetchTool fetches web page content and summarizes it using an LLM.
type WebFetchTool struct {
	BaseTool
	fetcher   webContentFetcher
	chatModel chat.Chat
}

// NewWebFetchTool creates a new web_fetch tool instance.
func NewWebFetchTool(chatModel chat.Chat) *WebFetchTool {
	return newWebFetchTool(chatModel, webfetch.NewFetcher())
}

func newWebFetchTool(chatModel chat.Chat, fetcher webContentFetcher) *WebFetchTool {
	return &WebFetchTool{
		BaseTool:  webFetchTool,
		fetcher:   fetcher,
		chatModel: chatModel,
	}
}

// Execute runs web_fetch and preserves successful items when a batch partially fails.
func (t *WebFetchTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][WebFetch] Execute started")
	var input WebFetchInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to parse args: %v", err)}, err
	}
	if len(input.Items) == 0 {
		return &types.ToolResult{Success: false, Error: "missing required parameter: items"}, nil
	}

	results := make([]*webFetchItemResult, len(input.Items))
	seenURLs := make(map[string]struct{}, len(input.Items))
	var waitGroup sync.WaitGroup
	for index, item := range input.Items {
		canonicalURL := canonicalFetchURL(item.URL)
		if _, duplicate := seenURLs[canonicalURL]; duplicate {
			results[index] = duplicateWebFetchResult(item)
			continue
		}
		seenURLs[canonicalURL] = struct{}{}
		waitGroup.Add(1)
		go func(resultIndex int, fetchItem WebFetchItem) {
			defer waitGroup.Done()
			results[resultIndex] = t.fetchItem(ctx, fetchItem)
		}(index, item)
	}
	waitGroup.Wait()

	return buildWebFetchToolResult(ctx, results), nil
}

func (t *WebFetchTool) fetchItem(ctx context.Context, item WebFetchItem) *webFetchItemResult {
	displayURL := strings.TrimSpace(item.URL)
	if strings.TrimSpace(item.Prompt) == "" {
		return failedWebFetchResult(displayURL, false, "invalid_arguments", "prompt is required")
	}

	fetchURL := normalizeGitHubURL(displayURL)
	content, err := t.fetcher.Fetch(ctx, fetchURL)
	if err != nil {
		code, retryable, message := webfetch.ErrorDetails(err)
		logger.Warnf(ctx, "[Tool][WebFetch] fetch failed url=%s code=%s retryable=%v err=%v", displayURL, code, retryable, err)
		return failedWebFetchResult(displayURL, retryable, string(code), message)
	}

	data := map[string]interface{}{
		"url":            displayURL,
		"status":         "success",
		"retryable":      false,
		"prompt":         item.Prompt,
		"raw_content":    content,
		"content_length": len(content),
		"evidence_type":  "fetched_page",
		"summary_status": "not_requested",
	}
	summary, summaryErr := t.processWithLLM(ctx, item, content)
	if summaryErr != nil {
		data["summary_status"] = "failed"
		data["summary_error_code"] = "summary_failed"
		data["summary_error_message"] = summaryErr.Error()
		logger.Warnf(ctx, "[Tool][WebFetch] summary failed url=%s err=%v", displayURL, summaryErr)
	} else if summary != "" {
		data["summary_status"] = "success"
		data["summary"] = summary
	}

	return &webFetchItemResult{
		output: buildWebFetchOutput(item, content, summary, summaryErr),
		data:   data,
		status: "success",
	}
}

func failedWebFetchResult(rawURL string, retryable bool, code, message string) *webFetchItemResult {
	data := map[string]interface{}{
		"url":           rawURL,
		"status":        "failed",
		"retryable":     retryable,
		"error_code":    code,
		"error_message": message,
	}
	return &webFetchItemResult{
		output: fmt.Sprintf("URL: %s\nStatus: failed\nRetryable: %t\nError code: %s\nError: %s\n",
			rawURL, retryable, code, message),
		data:   data,
		status: "failed",
	}
}

func duplicateWebFetchResult(item WebFetchItem) *webFetchItemResult {
	message := "duplicate URL skipped in this batch"
	return &webFetchItemResult{
		output: fmt.Sprintf("URL: %s\nStatus: skipped\nRetryable: false\nReason: %s\n", item.URL, message),
		data: map[string]interface{}{
			"url":           item.URL,
			"status":        "skipped",
			"retryable":     false,
			"error_code":    "duplicate_url",
			"error_message": message,
		},
		status: "skipped",
	}
}

func buildWebFetchToolResult(ctx context.Context, results []*webFetchItemResult) *types.ToolResult {
	var builder strings.Builder
	builder.WriteString("=== Web Fetch Results ===\n\n")
	aggregated := make([]map[string]interface{}, 0, len(results))
	successCount, failedCount, skippedCount := 0, 0, 0
	for index, result := range results {
		if result == nil {
			result = failedWebFetchResult("", false, "internal_error", "fetch item returned no result")
		}
		builder.WriteString(fmt.Sprintf("#%d:\n%s\n", index+1, result.output))
		aggregated = append(aggregated, result.data)
		switch result.status {
		case "success":
			successCount++
		case "failed":
			failedCount++
		case "skipped":
			skippedCount++
		}
	}

	allFailed := successCount == 0 && failedCount > 0
	builder.WriteString("=== Next Steps ===\n")
	switch {
	case allFailed:
		builder.WriteString("- All page fetches failed. Stop expanding web searches and answer from existing web_search titles, URLs, and snippets.\n")
		builder.WriteString("- Explicitly state that page content was not verified. Treat prices, inventory, and other dynamic facts as uncertain.\n")
	case failedCount > 0:
		builder.WriteString("- Use successful page content together with existing search snippets; failed URLs do not invalidate successful evidence.\n")
		builder.WriteString("- Do not retry non-retryable failures. If evidence is sufficient, answer now.\n")
	default:
		builder.WriteString("- Synthesize the fetched evidence and answer when it is sufficient.\n")
	}

	logger.Infof(ctx, "[Tool][WebFetch] completed success=%d failed=%d skipped=%d", successCount, failedCount, skippedCount)
	return &types.ToolResult{
		Success: true,
		Output:  builder.String(),
		Data: map[string]interface{}{
			"results":          aggregated,
			"count":            len(aggregated),
			"successful_count": successCount,
			"failed_count":     failedCount,
			"skipped_count":    skippedCount,
			"all_failed":       allFailed,
			"display_type":     "web_fetch_results",
		},
	}
}

func (t *WebFetchTool) processWithLLM(ctx context.Context, item WebFetchItem, content string) (string, error) {
	if t.chatModel == nil {
		return "", fmt.Errorf("chat model not available for web_fetch summary")
	}
	messages := []chat.Message{
		{
			Role:    "system",
			Content: "Answer the request from the supplied web page text. Never fabricate information that is absent from the page.",
		},
		{
			Role:    "user",
			Content: fmt.Sprintf("User request:\n%s\n\nWeb page content:\n%s", item.Prompt, content),
		},
	}
	modelCtx := types.WithLLMCallMetadata(ctx, "web_fetch_summary", "")
	response, err := t.chatModel.Chat(modelCtx, messages, &chat.ChatOptions{Temperature: 0.3, MaxTokens: 1024})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(response.Content), nil
}

func buildWebFetchOutput(item WebFetchItem, content, summary string, summaryErr error) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("URL: %s\nStatus: success\nPrompt: %s\n", item.URL, item.Prompt))
	if summary != "" {
		builder.WriteString("Summary:\n")
		builder.WriteString(summary)
		builder.WriteString("\n")
		return builder.String()
	}
	if summaryErr != nil {
		builder.WriteString(fmt.Sprintf("Summary status: failed (%s); fetched page content remains usable.\n", summaryErr))
	}
	builder.WriteString("Content Preview:\n")
	builder.WriteString(content)
	builder.WriteString("\n")
	return builder.String()
}

func canonicalFetchURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	parsedURL, err := url.Parse(trimmed)
	if err != nil || parsedURL.Host == "" {
		return trimmed
	}
	parsedURL.Fragment = ""
	parsedURL.Host = strings.ToLower(parsedURL.Host)
	return parsedURL.String()
}

func normalizeGitHubURL(source string) string {
	if strings.Contains(source, "github.com") && strings.Contains(source, "/blob/") {
		source = strings.Replace(source, "github.com", "raw.githubusercontent.com", 1)
		source = strings.Replace(source, "/blob/", "/", 1)
	}
	return source
}
