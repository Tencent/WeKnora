package agent

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	defaultWebSearchMaxCalls     = 2
	defaultWebFetchMaxRetries    = 1
	defaultWebFetchMaxURLs       = 10
	defaultWebResearchTimeoutSec = 90
)

type webResearchBudget struct {
	mu sync.Mutex

	maxSearchCalls  int
	maxFetchRetries int
	maxFetchURLs    int
	timeout         time.Duration

	startedAt          time.Time
	searchQueries      []string
	fetchAttempts      map[string]int
	successfulURLs     map[string]bool
	nonRetryableURLs   map[string]bool
	totalFetchAttempts int
	finalize           bool
	finalizeReason     string
	directiveInjected  bool
}

func newWebResearchBudget(config *types.AgentConfig) *webResearchBudget {
	if config == nil {
		config = &types.AgentConfig{}
	}
	return &webResearchBudget{
		maxSearchCalls:   effectivePositiveInt(config.WebSearchMaxCalls, defaultWebSearchMaxCalls),
		maxFetchRetries:  effectiveNonNegativeInt(config.WebFetchMaxRetries, defaultWebFetchMaxRetries),
		maxFetchURLs:     effectivePositiveInt(config.WebFetchMaxURLs, defaultWebFetchMaxURLs),
		timeout:          time.Duration(effectivePositiveInt(config.WebResearchTimeoutSec, defaultWebResearchTimeoutSec)) * time.Second,
		fetchAttempts:    make(map[string]int),
		successfulURLs:   make(map[string]bool),
		nonRetryableURLs: make(map[string]bool),
	}
}

func effectivePositiveInt(value *int, fallback int) int {
	if value == nil || *value <= 0 {
		return fallback
	}
	return *value
}

func effectiveNonNegativeInt(value *int, fallback int) int {
	if value == nil || *value < 0 {
		return fallback
	}
	return *value
}

func (budget *webResearchBudget) beforeToolCall(name string, args map[string]interface{}) (*types.ToolResult, bool) {
	if budget == nil || (name != agenttools.ToolWebSearch && name != agenttools.ToolWebFetch) {
		return nil, true
	}

	budget.mu.Lock()
	defer budget.mu.Unlock()
	if budget.startedAt.IsZero() {
		budget.startedAt = time.Now()
	}
	if time.Since(budget.startedAt) >= budget.timeout {
		return budget.blockLocked("web_research_timeout", "The web research time budget has been exhausted."), false
	}
	if budget.finalize {
		return budget.blockLocked(budget.finalizeReason, "Web research has already stopped for this request."), false
	}

	switch name {
	case agenttools.ToolWebSearch:
		return budget.beforeSearchLocked(args)
	case agenttools.ToolWebFetch:
		return budget.beforeFetchLocked(args)
	default:
		return nil, true
	}
}

func (budget *webResearchBudget) beforeSearchLocked(args map[string]interface{}) (*types.ToolResult, bool) {
	query, _ := args["query"].(string)
	for _, previous := range budget.searchQueries {
		if equivalentSearchQueries(previous, query) {
			return budget.blockLocked("duplicate_search_query", "An equivalent web search query was already executed."), false
		}
	}
	if len(budget.searchQueries) >= budget.maxSearchCalls {
		return budget.blockLocked("web_search_budget_exhausted", fmt.Sprintf("The request reached its limit of %d web searches.", budget.maxSearchCalls)), false
	}
	budget.searchQueries = append(budget.searchQueries, query)
	return nil, true
}

func (budget *webResearchBudget) beforeFetchLocked(args map[string]interface{}) (*types.ToolResult, bool) {
	urls := webFetchURLs(args)
	if len(urls) == 0 {
		return nil, true
	}
	if budget.totalFetchAttempts+len(urls) > budget.maxFetchURLs {
		return budget.blockLocked("web_fetch_budget_exhausted", fmt.Sprintf("The request reached its limit of %d URL fetch attempts.", budget.maxFetchURLs)), false
	}

	for _, rawURL := range urls {
		canonicalURL := canonicalResearchURL(rawURL)
		switch {
		case budget.successfulURLs[canonicalURL]:
			return budget.blockLocked("duplicate_fetch_url", "A successfully fetched URL must not be fetched again."), false
		case budget.nonRetryableURLs[canonicalURL]:
			return budget.blockLocked("non_retryable_fetch_url", "A URL with a non-retryable failure must not be fetched again."), false
		case budget.fetchAttempts[canonicalURL] >= 1+budget.maxFetchRetries:
			return budget.blockLocked("web_fetch_retry_budget_exhausted", fmt.Sprintf("A URL reached its limit of %d retries.", budget.maxFetchRetries)), false
		}
	}
	for _, rawURL := range urls {
		canonicalURL := canonicalResearchURL(rawURL)
		budget.fetchAttempts[canonicalURL]++
		budget.totalFetchAttempts++
	}
	return nil, true
}

func (budget *webResearchBudget) afterToolCall(name string, result *types.ToolResult) {
	if budget == nil || (name != agenttools.ToolWebSearch && name != agenttools.ToolWebFetch) {
		return
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if !budget.startedAt.IsZero() && time.Since(budget.startedAt) >= budget.timeout {
		budget.setFinalizeLocked("web_research_timeout")
	}
	if name != agenttools.ToolWebFetch {
		return
	}
	if result == nil || !result.Success {
		budget.setFinalizeLocked("web_fetch_tool_failed")
		return
	}
	budget.observeFetchResultsLocked(result.Data["results"])
	if allFailed, _ := result.Data["all_failed"].(bool); allFailed {
		budget.setFinalizeLocked("all_web_fetches_failed")
	} else if budget.totalFetchAttempts >= budget.maxFetchURLs {
		budget.setFinalizeLocked("web_fetch_budget_exhausted")
	}
}

func (budget *webResearchBudget) observeFetchResultsLocked(rawResults interface{}) {
	visit := func(result map[string]interface{}) {
		rawURL, _ := result["url"].(string)
		canonicalURL := canonicalResearchURL(rawURL)
		status, _ := result["status"].(string)
		if status == "success" {
			budget.successfulURLs[canonicalURL] = true
			return
		}
		if status != "failed" {
			return
		}
		retryable, _ := result["retryable"].(bool)
		if !retryable {
			budget.nonRetryableURLs[canonicalURL] = true
		}
	}
	switch results := rawResults.(type) {
	case []map[string]interface{}:
		for _, result := range results {
			visit(result)
		}
	case []interface{}:
		for _, rawResult := range results {
			if result, ok := rawResult.(map[string]interface{}); ok {
				visit(result)
			}
		}
	}
}

func (budget *webResearchBudget) blockLocked(reason, message string) *types.ToolResult {
	budget.setFinalizeLocked(reason)
	return &types.ToolResult{
		Success: true,
		Output:  message + " Stop web research and answer from the web_search summaries and any successful page content already available. Disclose unverified pages and qualify dynamic facts.",
		Data: map[string]interface{}{
			"research_budget_exhausted": true,
			"reason":                    reason,
		},
	}
}

func (budget *webResearchBudget) setFinalizeLocked(reason string) {
	if budget.finalize {
		return
	}
	budget.finalize = true
	budget.finalizeReason = reason
}

func (budget *webResearchBudget) finalizationState() (bool, string, bool) {
	if budget == nil {
		return false, "", false
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if !budget.startedAt.IsZero() && time.Since(budget.startedAt) >= budget.timeout {
		budget.setFinalizeLocked("web_research_timeout")
	}
	if !budget.finalize {
		return false, "", false
	}
	shouldInject := !budget.directiveInjected
	budget.directiveInjected = true
	return true, budget.finalizeReason, shouldInject
}

func (budget *webResearchBudget) toolTimeout(name string, fallback time.Duration) time.Duration {
	if budget == nil || (name != agenttools.ToolWebSearch && name != agenttools.ToolWebFetch) {
		return fallback
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if budget.startedAt.IsZero() {
		return fallback
	}
	remaining := budget.timeout - time.Since(budget.startedAt)
	if remaining <= 0 {
		return time.Millisecond
	}
	if remaining < fallback {
		return remaining
	}
	return fallback
}

func webFetchURLs(args map[string]interface{}) []string {
	rawItems, _ := args["items"].([]interface{})
	seen := make(map[string]struct{}, len(rawItems))
	urls := make([]string, 0, len(rawItems))
	for _, rawItem := range rawItems {
		item, _ := rawItem.(map[string]interface{})
		rawURL, _ := item["url"].(string)
		canonicalURL := canonicalResearchURL(rawURL)
		if canonicalURL == "" {
			continue
		}
		if _, exists := seen[canonicalURL]; exists {
			continue
		}
		seen[canonicalURL] = struct{}{}
		urls = append(urls, rawURL)
	}
	return urls
}

func canonicalResearchURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	parsedURL, err := url.Parse(trimmed)
	if err != nil || parsedURL.Host == "" {
		return trimmed
	}
	parsedURL.Fragment = ""
	parsedURL.Host = strings.ToLower(parsedURL.Host)
	return parsedURL.String()
}

func equivalentSearchQueries(left, right string) bool {
	leftTokens := normalizeSearchTokens(left)
	rightTokens := normalizeSearchTokens(right)
	if len(leftTokens) == 0 || len(rightTokens) == 0 {
		return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
	}
	leftSorted := append([]string(nil), leftTokens...)
	rightSorted := append([]string(nil), rightTokens...)
	sort.Strings(leftSorted)
	sort.Strings(rightSorted)
	if strings.Join(leftSorted, " ") == strings.Join(rightSorted, " ") {
		return true
	}
	return ngramSimilarity(strings.Join(leftTokens, ""), strings.Join(rightTokens, "")) >= 0.88
}

func normalizeSearchTokens(query string) []string {
	return strings.FieldsFunc(strings.ToLower(strings.TrimSpace(query)), func(char rune) bool {
		return !unicode.IsLetter(char) && !unicode.IsNumber(char)
	})
}

func ngramSimilarity(left, right string) float64 {
	leftGrams := runeNgrams(left, 2)
	rightGrams := runeNgrams(right, 2)
	if len(leftGrams) == 0 || len(rightGrams) == 0 {
		return 0
	}
	intersection := 0
	for gram := range leftGrams {
		if _, exists := rightGrams[gram]; exists {
			intersection++
		}
	}
	return float64(2*intersection) / float64(len(leftGrams)+len(rightGrams))
}

func runeNgrams(value string, size int) map[string]struct{} {
	runes := []rune(value)
	grams := make(map[string]struct{})
	if len(runes) < size {
		if len(runes) > 0 {
			grams[string(runes)] = struct{}{}
		}
		return grams
	}
	for index := 0; index <= len(runes)-size; index++ {
		grams[string(runes[index:index+size])] = struct{}{}
	}
	return grams
}

func webResearchFinalizationInstruction(reason string) string {
	return fmt.Sprintf(`Web research must stop now (reason: %s). Produce the final answer in this round without calling any tools. Use existing web_search titles, URLs, snippets, and any successful fetched content. Clearly label claims supported only by search summaries as not page-verified, and qualify prices, inventory, specifications, or other dynamic facts that could not be verified. Do not invent missing facts.`, reason)
}
