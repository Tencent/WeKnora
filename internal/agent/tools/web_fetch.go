package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

const (
	webFetchTimeout  = 60 * time.Second // timeout for web fetch
	webFetchMaxChars = 100000           // maximum number of characters to fetch
)

var webFetchTool = BaseTool{
	name: ToolWebFetch,
	description: `Fetch detailed web content from previously discovered URLs and analyze it with an LLM.

## Usage
- Receive one or more {url, prompt} combinations
- Fetch web page content and convert to Markdown text
- Use prompt to call small model for analysis and summary (if model is available)
- Return summary result and original content fragment

## When to Use
- **MANDATORY**: After web_search returns results, if content is truncated or incomplete, use web_fetch to get full page content
- When web_search snippet is insufficient for answering the question`,
	schema: utils.GenerateSchema[WebFetchInput](),
}

// WebFetchInput defines the input parameters for web fetch tool
type WebFetchInput struct {
	Items []WebFetchItem `json:"items" jsonschema:"批量抓取任务，每项包含 url 与 prompt"`
}

// WebFetchItem represents a single web fetch task
type WebFetchItem struct {
	URL    string `json:"url" jsonschema:"待抓取的网页 URL，需来自 web_search 结果"`
	Prompt string `json:"prompt" jsonschema:"分析该网页内容时使用的提示词"`
}

// webFetchParams is the parameters for the web fetch tool
type webFetchParams struct {
	URL    string
	Prompt string
}

// webFetchItemResult is the result for a web fetch item
type webFetchItemResult struct {
	output string
	data   map[string]interface{}
	err    error
}

// WebFetchTool fetches web page content and summarizes it using an LLM
type WebFetchTool struct {
	BaseTool
	client    *http.Client
	chatModel chat.Chat
}

// NewWebFetchTool creates a new web_fetch tool instance
func NewWebFetchTool(chatModel chat.Chat) *WebFetchTool {
	// Use SSRF-safe HTTP client to prevent redirect-based SSRF attacks
	ssrfConfig := utils.DefaultSSRFSafeHTTPClientConfig()
	ssrfConfig.Timeout = webFetchTimeout

	return &WebFetchTool{
		BaseTool:  webFetchTool,
		client:    utils.NewSSRFSafeHTTPClient(ssrfConfig),
		chatModel: chatModel,
	}
}

// Execute 执行 web_fetch 工具
func (t *WebFetchTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][WebFetch] Execute started")

	// Parse args from json.RawMessage
	var input WebFetchInput
	if err := json.Unmarshal(args, &input); err != nil {
		logger.Errorf(ctx, "[Tool][WebFetch] Failed to parse args: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, err
	}

	if len(input.Items) == 0 {
		logger.Errorf(ctx, "[Tool][WebFetch] 参数缺失: items")
		return &types.ToolResult{
			Success: false,
			Error:   "missing required parameter: items",
		}, nil
	}

	results := make([]*webFetchItemResult, len(input.Items))

	var wg sync.WaitGroup
	wg.Add(len(input.Items))

	for idx := range input.Items {
		i := idx
		item := input.Items[i]

		params := webFetchParams{
			URL:    item.URL,
			Prompt: item.Prompt,
		}

		go func(index int, p webFetchParams) {
			defer wg.Done()

			if err := t.validateParams(p); err != nil {
				results[index] = &webFetchItemResult{
					err: err,
					data: map[string]interface{}{
						"url":    p.URL,
						"prompt": p.Prompt,
						"error":  err.Error(),
					},
					output: fmt.Sprintf("URL: %s\n错误: %v\n\n", p.URL, err),
				}
				return
			}

			output, data, err := t.executeFetch(ctx, p)
			results[index] = &webFetchItemResult{
				output: output,
				data:   data,
				err:    err,
			}
		}(i, params)
	}

	wg.Wait()

	var builder strings.Builder
	builder.WriteString("=== Web Fetch Results ===\n\n")

	aggregated := make([]map[string]interface{}, 0, len(results))
	success := true
	var firstErr error

	for idx, res := range results {
		if res == nil {
			success = false
			if firstErr == nil {
				firstErr = fmt.Errorf("fetch item %d returned nil", idx)
			}
			builder.WriteString(fmt.Sprintf("#%d: 无结果（内部错误）\n\n", idx+1))
			continue
		}

		builder.WriteString(fmt.Sprintf("#%d:\n%s", idx+1, res.output))
		if !strings.HasSuffix(res.output, "\n") {
			builder.WriteString("\n")
		}
		builder.WriteString("\n")

		if res.data != nil {
			aggregated = append(aggregated, res.data)
		}

		if res.err != nil {
			success = false
			if firstErr == nil {
				firstErr = res.err
			}
		}
	}

	// Add guidance for next steps
	builder.WriteString("\n=== Next Steps ===\n")
	if len(aggregated) > 0 {
		builder.WriteString("- ✅ Full page content has been fetched and analyzed.\n")
		builder.WriteString("- Evaluate if the content is sufficient to answer the question completely.\n")
		builder.WriteString("- Synthesize information from all fetched pages for comprehensive answers.\n")
		if !success {
			builder.WriteString("- ⚠️ Some URLs failed to fetch. Use available content or try alternative sources.\n")
		}
	} else {
		builder.WriteString("- ❌ No content was successfully fetched. Consider:\n")
		builder.WriteString("  - Verify URLs are accessible\n")
		builder.WriteString("  - Try alternative sources from web_search results\n")
		builder.WriteString("  - Check if information can be found in knowledge base instead\n")
	}

	data := map[string]interface{}{
		"results":      aggregated,
		"count":        len(aggregated),
		"display_type": "web_fetch_results",
	}

	logger.Infof(ctx, "[Tool][WebFetch] Completed with success=%v, items=%d", success, len(aggregated))

	return &types.ToolResult{
		Success: success,
		Output:  builder.String(),
		Data:    data,
		Error: func() string {
			if firstErr != nil {
				return firstErr.Error()
			}
			return ""
		}(),
	}, nil
}

// parseParams parses the parameters for a web fetch item
func (t *WebFetchTool) parseParams(item interface{}) webFetchParams {
	params := webFetchParams{}
	if m, ok := item.(map[string]interface{}); ok {
		if v, ok := m["url"].(string); ok {
			params.URL = strings.TrimSpace(v)
		}
		if v, ok := m["prompt"].(string); ok {
			params.Prompt = strings.TrimSpace(v)
		}
	}
	return params
}

// validateParams validates the parameters for a web fetch item
func (t *WebFetchTool) validateParams(p webFetchParams) error {
	if p.URL == "" {
		return fmt.Errorf("url is required")
	}
	if p.Prompt == "" {
		return fmt.Errorf("prompt is required")
	}
	if !strings.HasPrefix(p.URL, "http://") && !strings.HasPrefix(p.URL, "https://") {
		return fmt.Errorf("invalid URL format")
	}

	// SSRF protection: validate URL is safe to fetch
	if safe, reason := utils.IsSSRFSafeURL(p.URL); !safe {
		return fmt.Errorf("URL rejected for security reasons: %s", reason)
	}

	return nil
}

// executeFetch executes a web fetch item
func (t *WebFetchTool) executeFetch(
	ctx context.Context,
	params webFetchParams,
) (string, map[string]interface{}, error) {
	logger.Infof(ctx, "[Tool][WebFetch] Fetching URL: %s", params.URL)

	finalURL := t.normalizeGitHubURL(params.URL)

	htmlContent, method, err := t.fetchHTMLContent(ctx, finalURL)
	if err != nil {
		logger.Errorf(ctx, "[Tool][WebFetch] 获取页面失败 url=%s err=%v", finalURL, err)
		return fmt.Sprintf("URL: %s\n错误: %v\n", params.URL, err),
			map[string]interface{}{
				"url":    params.URL,
				"prompt": params.Prompt,
				"error":  err.Error(),
			}, err
	}

	textContent := t.convertHTMLToText(htmlContent)

	resultData := map[string]interface{}{
		"url":            params.URL,
		"prompt":         params.Prompt,
		"raw_content":    textContent,
		"content_length": len(textContent),
		"method":         method,
	}
	var summary string
	var summaryErr error
	summary, summaryErr = t.processWithLLM(ctx, params, textContent)
	if summaryErr != nil {
		logger.Warnf(ctx, "[Tool][WebFetch] LLM 处理失败 url=%s err=%v", params.URL, summaryErr)
	} else if summary != "" {
		resultData["summary"] = summary
	}

	output := t.buildOutputText(params, textContent, summary, summaryErr)

	return output, resultData, summaryErr
}

// normalizeGitHubURL normalizes a GitHub URL
func (t *WebFetchTool) normalizeGitHubURL(source string) string {
	if strings.Contains(source, "github.com") && strings.Contains(source, "/blob/") {
		source = strings.Replace(source, "github.com", "raw.githubusercontent.com", 1)
		source = strings.Replace(source, "/blob/", "/", 1)
	}
	return source
}

// processWithLLM processes the content with an LLM
func (t *WebFetchTool) processWithLLM(ctx context.Context, params webFetchParams, content string) (string, error) {
	if t.chatModel == nil {
		return "", fmt.Errorf("chat model not available for web_fetch")
	}

	systemMessage := "你是一名擅长阅读网页内容的智能助手，请根据提供的网页文本回答用户需求，严禁编造未在文本中出现的信息。"
	userTemplate := `用户请求:
%s

网页内容:
%s`

	messages := []chat.Message{
		{
			Role:    "system",
			Content: systemMessage,
		},
		{
			Role:    "user",
			Content: fmt.Sprintf(userTemplate, params.Prompt, content),
		},
	}

	response, err := t.chatModel.Chat(ctx, messages, &chat.ChatOptions{
		Temperature: 0.3,
		MaxTokens:   1024,
	})
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(response.Content), nil
}

// buildOutputText builds the output text for a web fetch item
func (t *WebFetchTool) buildOutputText(params webFetchParams, content string, summary string, summaryErr error) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("URL: %s\n", params.URL))
	builder.WriteString(fmt.Sprintf("Prompt: %s\n", params.Prompt))

	if summaryErr == nil && summary != "" {
		builder.WriteString("Summary:\n")
		builder.WriteString(summary)
		builder.WriteString("\n")
	} else {
		builder.WriteString("Content Preview:\n")
		builder.WriteString(content)
		builder.WriteString("\n")
	}

	return builder.String()
}

// fetchHTMLContent fetches the HTML content for a web fetch item
func (t *WebFetchTool) fetchHTMLContent(ctx context.Context, targetURL string) (string, string, error) {
	html, err := t.fetchWithChromedp(ctx, targetURL)
	if err == nil && strings.TrimSpace(html) != "" {
		return html, "chromedp", nil
	}

	if err != nil {
		logger.Debugf(ctx, "[Tool][WebFetch] Chromedp 抓取失败 url=%s err=%v，尝试直接请求", targetURL, err)
	}

	html, httpErr := t.fetchWithHTTP(ctx, targetURL)
	if httpErr != nil {
		if err != nil {
			return "", "", fmt.Errorf("chromedp error: %v; http error: %w", err, httpErr)
		}
		return "", "", httpErr
	}

	return html, "http", nil
}

// fetchWithChromedp fetches the HTML content with Chromedp
func (t *WebFetchTool) fetchWithChromedp(ctx context.Context, targetURL string) (string, error) {
	logger.Debugf(ctx, "[Tool][WebFetch] Chromedp 抓取开始 url=%s", targetURL)

	opts := append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-features", "VizDisplayCompositor"),
		chromedp.UserAgent(
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()

	ctx, cancel = chromedp.NewContext(allocCtx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, webFetchTimeout)
	defer cancel()

	var html string
	err := chromedp.Run(ctx,
		chromedp.Navigate(targetURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.OuterHTML("html", &html),
	)
	if err != nil {
		return "", fmt.Errorf("chromedp run failed: %w", err)
	}

	logger.Debugf(ctx, "[Tool][WebFetch] Chromedp 抓取成功 url=%s", targetURL)
	return html, nil
}

// fetchWithHTTP fetches the HTML content with HTTP
func (t *WebFetchTool) fetchWithHTTP(ctx context.Context, targetURL string) (string, error) {
	resp, err := t.fetchWithTimeout(ctx, targetURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("request failed with status %d %s", resp.StatusCode, resp.Status)
	}

	limitedReader := io.LimitReader(resp.Body, webFetchMaxChars*2)
	htmlBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(htmlBytes), nil
}

// fetchWithTimeout fetches the HTML content with a timeout
func (t *WebFetchTool) fetchWithTimeout(ctx context.Context, targetURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; WebFetchTool/1.0)")
	req.Header.Set(
		"Accept",
		"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
	)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Cache-Control", "no-cache")

	return t.client.Do(req)
}

// convertHTMLToText converts the HTML content to text
func (t *WebFetchTool) convertHTMLToText(html string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return t.basicTextExtraction(html)
	}

	doc.Find("script, style, nav, footer, header").Remove()

	var markdown strings.Builder
	doc.Find("body").Each(func(i int, body *goquery.Selection) {
		t.processNode(body, &markdown)
	})

	result := markdown.String()
	result = regexp.MustCompile(`\n{3,}`).ReplaceAllString(result, "\n\n")
	return strings.TrimSpace(result)
}

// processNode processes a node in the HTML content
func (t *WebFetchTool) processNode(s *goquery.Selection, markdown *strings.Builder) {
	s.Contents().Each(func(i int, node *goquery.Selection) {
		nodeName := goquery.NodeName(node)

		switch nodeName {
		case "h1", "h2", "h3", "h4", "h5", "h6":
			headerLevel := int(nodeName[1] - '0')
			markdown.WriteString("\n")
			markdown.WriteString(strings.Repeat("#", headerLevel))
			markdown.WriteString(" ")
			markdown.WriteString(strings.TrimSpace(node.Text()))
			markdown.WriteString("\n\n")
		case "p":
			t.processNode(node, markdown)
			markdown.WriteString("\n\n")
		case "a":
			href, exists := node.Attr("href")
			text := strings.TrimSpace(node.Text())
			if exists && text != "" {
				markdown.WriteString("[")
				markdown.WriteString(text)
				markdown.WriteString("](")
				markdown.WriteString(href)
				markdown.WriteString(")")
			} else if text != "" {
				markdown.WriteString(text)
			}
		case "img":
			src, _ := node.Attr("src")
			alt, _ := node.Attr("alt")
			if src != "" {
				markdown.WriteString("![")
				markdown.WriteString(alt)
				markdown.WriteString("](")
				markdown.WriteString(src)
				markdown.WriteString(")\n\n")
			}
		case "ul", "ol":
			markdown.WriteString("\n")
			isOrdered := nodeName == "ol"
			node.Find("li").Each(func(idx int, li *goquery.Selection) {
				if isOrdered {
					fmt.Fprintf(markdown, "%d. ", idx+1)
				} else {
					markdown.WriteString("- ")
				}
				markdown.WriteString(strings.TrimSpace(li.Text()))
				markdown.WriteString("\n")
			})
			markdown.WriteString("\n")
		case "br":
			markdown.WriteString("\n")
		case "code":
			parent := node.Parent()
			if goquery.NodeName(parent) == "pre" {
				markdown.WriteString("\n```\n")
				markdown.WriteString(node.Text())
				markdown.WriteString("\n```\n\n")
			} else {
				markdown.WriteString("`")
				markdown.WriteString(node.Text())
				markdown.WriteString("`")
			}
		case "blockquote":
			lines := strings.Split(strings.TrimSpace(node.Text()), "\n")
			for _, line := range lines {
				markdown.WriteString("> ")
				markdown.WriteString(strings.TrimSpace(line))
				markdown.WriteString("\n")
			}
			markdown.WriteString("\n")
		case "strong", "b":
			markdown.WriteString("**")
			markdown.WriteString(strings.TrimSpace(node.Text()))
			markdown.WriteString("**")
		case "em", "i":
			markdown.WriteString("*")
			markdown.WriteString(strings.TrimSpace(node.Text()))
			markdown.WriteString("*")
		case "hr":
			markdown.WriteString("\n---\n\n")
		case "table":
			markdown.WriteString("\n")
			node.Find("tr").Each(func(idx int, tr *goquery.Selection) {
				tr.Find("th, td").Each(func(i int, cell *goquery.Selection) {
					markdown.WriteString("| ")
					markdown.WriteString(strings.TrimSpace(cell.Text()))
					markdown.WriteString(" ")
				})
				markdown.WriteString("|\n")
				if idx == 0 {
					tr.Find("th").Each(func(i int, _ *goquery.Selection) {
						markdown.WriteString("|---")
					})
					markdown.WriteString("|\n")
				}
			})
			markdown.WriteString("\n")
		case "#text":
			text := node.Text()
			if strings.TrimSpace(text) != "" {
				markdown.WriteString(text)
			}
		default:
			t.processNode(node, markdown)
		}
	})
}

// basicTextExtraction extracts the text from the HTML content
func (t *WebFetchTool) basicTextExtraction(html string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	text := re.ReplaceAllString(html, " ")
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}
