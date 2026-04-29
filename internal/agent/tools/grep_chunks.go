package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

var grepChunksTool = BaseTool{
	name: ToolGrepChunks,
	description: `Search knowledge base chunk content using PostgreSQL POSIX regular expressions (~* operator, case-insensitive; REGEXP on MySQL/SQLite).
STRONGLY PREFER using regex to search for multiple concepts at once rather than simple plain text queries.
Returns matching chunks with per-pattern hit counts and a <match_snippet> around the first match (each tagged with its knowledge_id and chunk_id).
Examples:
- Alternation (RECOMMENDED): "stardust|skyvault" (matches either word)
- Multiple terms (RECOMMENDED): "psionic.*engine" (matches both words in order)
- Word boundary / anchor: "\\brag\\b" or "^chapter\\s+\\d+"
- Plain text: "engine" (matches literal substring anywhere in chunk content)
IMPORTANT — JSON escaping: every backslash in a regex MUST be written as \\ inside the JSON tool arguments (e.g. to search for literal "C++" write "C\\+\\+", NOT "C\+\+"; for "\d+" write "\\d+"). Plain "\+" / "\d" etc. are invalid JSON escapes and will fail to parse.
Use this to locate candidate chunks by exact identifiers, error codes, product names, or recurring terms. Pair with list_knowledge_chunks afterwards to read the full context around any promising chunk_id.`,
	schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "queries": {
      "type": "array",
      "items": { "type": "string" },
      "description": "List of regex queries to run. A chunk matches when ANY query matches its content. Prefer one alternation query (\"a|b|c\") over multiple single-keyword queries.",
      "minItems": 1,
      "maxItems": 5
    },
    "knowledge_base_ids": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Optional: restrict search to specific KB IDs within the agent scope."
    },
    "limit": {
      "type": "integer",
      "description": "Max matching chunks to return (default 30, max 100).",
      "default": 30,
      "minimum": 1,
      "maximum": 100
    }
  },
  "required": ["queries"]
}`),
}

// GrepChunksInput defines the input parameters for grep chunks tool.
// The canonical parameter names are `queries` and `limit` (mirroring
// wiki_search). The legacy `patterns` and `max_results` keys remain accepted
// so older model outputs or external callers don't break silently.
type GrepChunksInput struct {
	Queries          []string `json:"queries,omitempty"`
	Patterns         []string `json:"patterns,omitempty"` // legacy alias for queries
	KnowledgeBaseIDs []string `json:"knowledge_base_ids,omitempty"`
	Limit            int      `json:"limit,omitempty"`
	MaxResults       int      `json:"max_results,omitempty"` // legacy alias for limit
}

// GrepChunksTool performs regex pattern matching across knowledge base chunks.
// PostgreSQL: uses the case-insensitive POSIX operator ~*.
// MySQL/SQLite: falls back to REGEXP.
//
// The tool tracks previously-returned chunk IDs per-instance (one instance per
// agent session) so that a subsequent search hitting the same chunk can be
// rendered compactly with an `already_seen="true"` marker instead of replaying
// the snippet, mirroring the UX of wiki_search.
type GrepChunksTool struct {
	BaseTool
	db            *gorm.DB
	searchTargets types.SearchTargets

	mu          sync.Mutex
	seenChunks  map[string]bool
}

// NewGrepChunksTool creates a new grep chunks tool
func NewGrepChunksTool(db *gorm.DB, searchTargets types.SearchTargets) *GrepChunksTool {
	return &GrepChunksTool{
		BaseTool:      grepChunksTool,
		db:            db,
		searchTargets: searchTargets,
		seenChunks:    make(map[string]bool),
	}
}

// Execute executes the grep chunks tool
func (t *GrepChunksTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][GrepChunks] Execute started")

	var input GrepChunksInput
	if err := json.Unmarshal(args, &input); err != nil {
		logger.Errorf(ctx, "[Tool][GrepChunks] Failed to parse args: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, err
	}

	// Accept both canonical (`queries`) and legacy (`patterns`) field names.
	// When both are present we concatenate, preserving whichever came first,
	// so a caller migrating between the two won't end up with nothing.
	rawQueries := append([]string{}, input.Queries...)
	rawQueries = append(rawQueries, input.Patterns...)

	queries := make([]string, 0, len(rawQueries))
	for _, q := range rawQueries {
		if strings.TrimSpace(q) != "" {
			queries = append(queries, q)
		}
	}

	if len(queries) == 0 {
		logger.Errorf(ctx, "[Tool][GrepChunks] Missing or empty queries parameter")
		return &types.ToolResult{
			Success: false,
			Error:   "queries parameter is required and must contain at least one non-empty regex query",
		}, fmt.Errorf("missing queries parameter")
	}
	if len(queries) > 5 {
		queries = queries[:5]
	}

	// Compile queries with (?i) prefix for case-insensitive Go-side matching.
	// Compilation also validates the regex syntax before we send it to the DB.
	compiled := make([]*regexp.Regexp, 0, len(queries))
	for _, q := range queries {
		re, err := regexp.Compile("(?i)" + q)
		if err != nil {
			logger.Errorf(ctx, "[Tool][GrepChunks] Invalid regex %q: %v", q, err)
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("invalid regex query %q: %v", q, err),
			}, err
		}
		compiled = append(compiled, re)
	}

	// Canonical `limit`, with `max_results` accepted as legacy alias.
	limit := input.Limit
	if limit <= 0 && input.MaxResults > 0 {
		limit = input.MaxResults
	}
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}

	allowedKBIDs := t.searchTargets.GetAllKnowledgeBaseIDs()
	kbTenantMap := t.searchTargets.GetKBTenantMap()

	var allowedKnowledgeIDs []string
	for _, target := range t.searchTargets {
		if target.Type == types.SearchTargetTypeKnowledge && len(target.KnowledgeIDs) > 0 {
			allowedKnowledgeIDs = append(allowedKnowledgeIDs, target.KnowledgeIDs...)
		}
	}

	kbIDs := input.KnowledgeBaseIDs
	if len(kbIDs) == 0 {
		kbIDs = allowedKBIDs
	} else {
		validKBs := make([]string, 0)
		for _, kbID := range kbIDs {
			if t.searchTargets.ContainsKB(kbID) {
				validKBs = append(validKBs, kbID)
			}
		}
		kbIDs = validKBs
	}

	logger.Infof(ctx, "[Tool][GrepChunks] Queries: %v, Limit: %d, KBs: %v, KnowledgeIDs: %v",
		queries, limit, kbIDs, allowedKnowledgeIDs)

	results, err := t.searchChunks(ctx, queries, kbIDs, allowedKnowledgeIDs, kbTenantMap)
	if err != nil {
		logger.Errorf(ctx, "[Tool][GrepChunks] Search failed: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Search failed: %v", err),
		}, err
	}

	logger.Infof(ctx, "[Tool][GrepChunks] Found %d matching chunks", len(results))

	deduplicatedResults := t.deduplicateChunks(ctx, results)
	logger.Infof(ctx, "[Tool][GrepChunks] After deduplication: %d chunks (from %d)",
		len(deduplicatedResults), len(results))

	// Score chunks using compiled regex (counts + earliest-position boost).
	scoredResults := t.scoreChunks(ctx, deduplicatedResults, compiled)

	finalResults := scoredResults
	if len(scoredResults) > 10 {
		mmrK := len(scoredResults)
		if limit > 0 && mmrK > limit {
			mmrK = limit
		}
		logger.Debugf(ctx, "[Tool][GrepChunks] Applying MMR: k=%d, lambda=0.7, input=%d results",
			mmrK, len(scoredResults))
		mmrResults := t.applyMMR(ctx, scoredResults, mmrK, 0.7)
		if len(mmrResults) > 0 {
			finalResults = mmrResults
			logger.Infof(ctx, "[Tool][GrepChunks] MMR completed: %d results selected", len(finalResults))
		}
	}

	sort.Slice(finalResults, func(i, j int) bool {
		if finalResults[i].MatchedPatterns != finalResults[j].MatchedPatterns {
			return finalResults[i].MatchedPatterns > finalResults[j].MatchedPatterns
		}
		if finalResults[i].MatchScore != finalResults[j].MatchScore {
			return finalResults[i].MatchScore > finalResults[j].MatchScore
		}
		return finalResults[i].ChunkIndex < finalResults[j].ChunkIndex
	})

	if len(finalResults) > limit {
		finalResults = finalResults[:limit]
	}

	// Aggregation by knowledge is still useful for the frontend summary view.
	aggregatedResults := t.aggregateByKnowledge(finalResults, queries, compiled)
	if len(aggregatedResults) > 20 {
		aggregatedResults = aggregatedResults[:20]
	}

	output := t.formatOutput(ctx, finalResults, queries, compiled)

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"queries":            queries,
			"patterns":           queries, // legacy alias; frontend currently reads `patterns`
			"knowledge_results":  aggregatedResults,
			"result_count":       len(aggregatedResults),
			"total_matches":      len(finalResults),
			"knowledge_base_ids": kbIDs,
			"limit":              limit,
			"max_results":        limit, // legacy alias
			"display_type":       "grep_results",
		},
	}, nil
}

type chunkWithTitle struct {
	types.Chunk
	KnowledgeTitle  string  `json:"knowledge_title"   gorm:"column:knowledge_title"`
	MatchScore      float64 `json:"match_score"       gorm:"column:match_score"`
	MatchedPatterns int     `json:"matched_patterns"`
	TotalChunkCount int     `json:"total_chunk_count" gorm:"column:total_chunk_count"`
}

// regexOperatorForDialect returns the SQL operator used to apply a POSIX
// regular expression to a text column for the current dialect.
// PostgreSQL ~* is case-insensitive by default; MySQL/SQLite REGEXP relies on
// collation / driver extensions.
func (t *GrepChunksTool) regexOperatorForDialect() string {
	switch t.db.Dialector.Name() {
	case "postgres":
		return "~*"
	default:
		// MySQL, SQLite (with the go-sqlite3 REGEXP extension), or anything else
		// that understands the REGEXP keyword.
		return "REGEXP"
	}
}

// searchChunks performs the database search using regex queries.
func (t *GrepChunksTool) searchChunks(
	ctx context.Context,
	queries []string,
	kbIDs []string,
	knowledgeIDs []string,
	kbTenantMap map[string]uint64,
) ([]chunkWithTitle, error) {
	if len(kbIDs) == 0 && len(knowledgeIDs) == 0 {
		logger.Warnf(ctx, "[Tool][GrepChunks] No kbIDs or knowledgeIDs specified, returning empty results")
		return nil, nil
	}

	regexOp := t.regexOperatorForDialect()

	query := t.db.WithContext(ctx).Table("chunks").
		Select("chunks.id, chunks.content, chunks.chunk_index, chunks.knowledge_id, "+
			"chunks.knowledge_base_id, chunks.chunk_type, chunks.created_at, "+
			"knowledges.title as knowledge_title").
		Joins("JOIN knowledges ON chunks.knowledge_id = knowledges.id").
		Where("chunks.is_enabled = ?", true).
		Where("chunks.deleted_at IS NULL").
		Where("knowledges.deleted_at IS NULL")

	if len(knowledgeIDs) > 0 {
		query = query.Where("chunks.knowledge_id IN ?", knowledgeIDs)
		logger.Infof(ctx, "[Tool][GrepChunks] Filtering by %d specific knowledge IDs", len(knowledgeIDs))
	} else if len(kbIDs) > 0 {
		var conditions []string
		var args []interface{}
		for _, kbID := range kbIDs {
			tenantID := kbTenantMap[kbID]
			if tenantID > 0 {
				conditions = append(conditions, "(chunks.knowledge_base_id = ? AND chunks.tenant_id = ?)")
				args = append(args, kbID, tenantID)
			}
		}
		if len(conditions) > 0 {
			query = query.Where("("+strings.Join(conditions, " OR ")+")", args...)
		} else {
			logger.Warnf(ctx, "[Tool][GrepChunks] No valid KB-tenant pairs found")
			return nil, nil
		}
	}

	// For MySQL/SQLite REGEXP case-insensitivity we rely on the column's default
	// collation (utf8mb4_general_ci etc.) OR the driver's REGEXP implementation,
	// which mirrors what wiki_search already ships in this codebase.
	var regexConditions []string
	var regexArgs []interface{}
	for _, q := range queries {
		regexConditions = append(regexConditions, fmt.Sprintf("chunks.content %s ?", regexOp))
		regexArgs = append(regexArgs, q)
	}
	query = query.Where("("+strings.Join(regexConditions, " OR ")+")", regexArgs...)

	const maxFetchLimit = 500

	var results []chunkWithTitle
	if err := query.Order("chunks.created_at DESC").Limit(maxFetchLimit).Find(&results).Error; err != nil {
		logger.Errorf(ctx, "[Tool][GrepChunks] Failed to fetch results: %v", err)
		return nil, err
	}

	if len(results) > 0 {
		knowledgeIDSet := make(map[string]struct{})
		for _, r := range results {
			if r.KnowledgeID != "" {
				knowledgeIDSet[r.KnowledgeID] = struct{}{}
			}
		}
		uniqueKnowledgeIDs := make([]string, 0, len(knowledgeIDSet))
		for kid := range knowledgeIDSet {
			uniqueKnowledgeIDs = append(uniqueKnowledgeIDs, kid)
		}

		type countRow struct {
			KnowledgeID string `gorm:"column:knowledge_id"`
			Count       int    `gorm:"column:cnt"`
		}
		var counts []countRow
		if err := t.db.WithContext(ctx).Table("chunks").
			Select("knowledge_id, COUNT(*) AS cnt").
			Where("knowledge_id IN ?", uniqueKnowledgeIDs).
			Where("is_enabled = ?", true).
			Where("deleted_at IS NULL").
			Group("knowledge_id").
			Find(&counts).Error; err != nil {
			logger.Warnf(ctx, "[Tool][GrepChunks] Failed to fetch chunk counts, skipping: %v", err)
		} else {
			countMap := make(map[string]int, len(counts))
			for _, c := range counts {
				countMap[c.KnowledgeID] = c.Count
			}
			for i := range results {
				results[i].TotalChunkCount = countMap[results[i].KnowledgeID]
			}
		}
	}

	return results, nil
}

// formatOutput emits per-chunk XML with <match_snippet> and <query_hit>
// elements, mirroring the wiki_search output shape. Chunks that were already
// surfaced by a previous call to this tool in the same session are rendered
// compactly with `already_seen="true"` so the LLM doesn't waste context
// re-reading the same snippet.
func (t *GrepChunksTool) formatOutput(
	ctx context.Context,
	results []chunkWithTitle,
	queries []string,
	compiled []*regexp.Regexp,
) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("<grep_results chunk_count=\"%d\">\n", len(results)))
	for _, q := range queries {
		b.WriteString(fmt.Sprintf("<query>%s</query>\n", xmlEscape(q)))
	}

	if len(results) == 0 {
		b.WriteString("</grep_results>")
		return b.String()
	}

	for _, r := range results {
		counts := countRegexHits(r.Content, compiled, queries)
		snippet := extractSnippetRegex(r.Content, compiled)

		t.mu.Lock()
		seen := t.seenChunks[r.ID]
		t.seenChunks[r.ID] = true
		t.mu.Unlock()

		if seen {
			b.WriteString(fmt.Sprintf(
				"<chunk chunk_id=\"%s\" knowledge_id=\"%s\" knowledge_title=\"%s\" chunk_index=\"%d\" score=\"%.3f\" already_seen=\"true\">\n",
				xmlEscape(r.ID),
				xmlEscape(r.KnowledgeID),
				xmlEscape(r.KnowledgeTitle),
				r.ChunkIndex,
				r.MatchScore,
			))
			for _, q := range queries {
				if c := counts[q]; c > 0 {
					b.WriteString(fmt.Sprintf("<query_hit query=\"%s\" count=\"%d\" />\n",
						xmlEscape(q), c))
				}
			}
			b.WriteString("<note>(snippet omitted, already returned in a previous grep_chunks call this session)</note>\n")
			b.WriteString("</chunk>\n")
			continue
		}

		b.WriteString(fmt.Sprintf(
			"<chunk chunk_id=\"%s\" knowledge_id=\"%s\" knowledge_title=\"%s\" chunk_index=\"%d\" score=\"%.3f\">\n",
			xmlEscape(r.ID),
			xmlEscape(r.KnowledgeID),
			xmlEscape(r.KnowledgeTitle),
			r.ChunkIndex,
			r.MatchScore,
		))
		for _, q := range queries {
			if c := counts[q]; c > 0 {
				b.WriteString(fmt.Sprintf("<query_hit query=\"%s\" count=\"%d\" />\n",
					xmlEscape(q), c))
			}
		}
		if snippet != "" {
			b.WriteString(fmt.Sprintf("<match_snippet>%s</match_snippet>\n", xmlEscape(snippet)))
		}
		b.WriteString("</chunk>\n")
	}

	b.WriteString("</grep_results>")
	_ = ctx
	return b.String()
}

type knowledgeAggregation struct {
	KnowledgeID      string         `json:"knowledge_id"`
	KnowledgeBaseID  string         `json:"knowledge_base_id"`
	KnowledgeTitle   string         `json:"knowledge_title"`
	ChunkHitCount    int            `json:"chunk_hit_count"`
	TotalChunkCount  int            `json:"total_chunk_count"`
	PatternCounts    map[string]int `json:"pattern_counts"`
	TotalPatternHits int            `json:"total_pattern_hits"`
	DistinctPatterns int            `json:"distinct_patterns"`
}

func (t *GrepChunksTool) aggregateByKnowledge(
	results []chunkWithTitle,
	queries []string,
	compiled []*regexp.Regexp,
) []knowledgeAggregation {
	if len(results) == 0 {
		return nil
	}

	queryKeys := make([]string, 0, len(queries))
	for _, q := range queries {
		if strings.TrimSpace(q) == "" {
			continue
		}
		queryKeys = append(queryKeys, q)
	}

	aggregated := make(map[string]*knowledgeAggregation)
	for _, chunk := range results {
		knowledgeID := chunk.KnowledgeID
		if knowledgeID == "" {
			knowledgeID = fmt.Sprintf("chunk-%s", chunk.ID)
		}

		if _, ok := aggregated[knowledgeID]; !ok {
			title := chunk.KnowledgeTitle
			if strings.TrimSpace(title) == "" {
				title = "Untitled"
			}
			aggregated[knowledgeID] = &knowledgeAggregation{
				KnowledgeID:     knowledgeID,
				KnowledgeBaseID: chunk.KnowledgeBaseID,
				KnowledgeTitle:  title,
				TotalChunkCount: chunk.TotalChunkCount,
				PatternCounts:   make(map[string]int, len(queryKeys)),
			}
			for _, qKey := range queryKeys {
				aggregated[knowledgeID].PatternCounts[qKey] = 0
			}
		}

		entry := aggregated[knowledgeID]
		entry.ChunkHitCount++

		occurrences := countRegexHits(chunk.Content, compiled, queryKeys)
		for _, q := range queryKeys {
			count := occurrences[q]
			if count == 0 {
				continue
			}
			entry.PatternCounts[q] += count
			entry.TotalPatternHits += count
		}
	}

	resultSlice := make([]knowledgeAggregation, 0, len(aggregated))
	for _, entry := range aggregated {
		distinct := 0
		for _, count := range entry.PatternCounts {
			if count > 0 {
				distinct++
			}
		}
		entry.DistinctPatterns = distinct
		resultSlice = append(resultSlice, *entry)
	}

	sort.Slice(resultSlice, func(i, j int) bool {
		if resultSlice[i].DistinctPatterns != resultSlice[j].DistinctPatterns {
			return resultSlice[i].DistinctPatterns > resultSlice[j].DistinctPatterns
		}
		if resultSlice[i].TotalPatternHits != resultSlice[j].TotalPatternHits {
			return resultSlice[i].TotalPatternHits > resultSlice[j].TotalPatternHits
		}
		if resultSlice[i].ChunkHitCount != resultSlice[j].ChunkHitCount {
			return resultSlice[i].ChunkHitCount > resultSlice[j].ChunkHitCount
		}
		return resultSlice[i].KnowledgeTitle < resultSlice[j].KnowledgeTitle
	})
	return resultSlice
}

// countRegexHits returns the total number of matches per (compiled) pattern
// within content, keyed by the original (uncompiled) pattern string.
func countRegexHits(content string, compiled []*regexp.Regexp, patterns []string) map[string]int {
	counts := make(map[string]int, len(patterns))
	if content == "" || len(compiled) == 0 {
		return counts
	}
	for i, re := range compiled {
		if re == nil {
			continue
		}
		matches := re.FindAllStringIndex(content, -1)
		counts[patterns[i]] = len(matches)
	}
	return counts
}

// extractSnippetRegex returns a short context snippet around the earliest
// regex match across any of the provided compiled patterns. Result is
// compressed to a single line and bounded in length on both sides of the
// match to keep the XML output concise.
func extractSnippetRegex(content string, compiled []*regexp.Regexp) string {
	if content == "" || len(compiled) == 0 {
		return ""
	}

	earliest := -1
	earliestEnd := -1
	for _, re := range compiled {
		if re == nil {
			continue
		}
		loc := re.FindStringIndex(content)
		if loc == nil {
			continue
		}
		if earliest < 0 || loc[0] < earliest {
			earliest = loc[0]
			earliestEnd = loc[1]
		}
	}
	if earliest < 0 {
		return ""
	}

	matchStr := content[earliest:earliestEnd]
	before := content[:earliest]
	after := content[earliestEnd:]

	const contextRunes = 60
	beforeRunes := []rune(before)
	if len(beforeRunes) > contextRunes {
		beforeRunes = beforeRunes[len(beforeRunes)-contextRunes:]
	}
	afterRunes := []rune(after)
	if len(afterRunes) > contextRunes {
		afterRunes = afterRunes[:contextRunes]
	}
	matchRunes := []rune(matchStr)
	if len(matchRunes) > 120 {
		matchRunes = append(matchRunes[:120], []rune("...")...)
	}

	snippet := string(beforeRunes) + string(matchRunes) + string(afterRunes)
	snippet = strings.ReplaceAll(snippet, "\n", " ")
	for strings.Contains(snippet, "  ") {
		snippet = strings.ReplaceAll(snippet, "  ", " ")
	}
	return "... " + strings.TrimSpace(snippet) + " ..."
}

// xmlEscape replaces characters that would break simple XML attribute /
// element values. It is intentionally minimal because the rendered output is
// consumed by the LLM (forgiving parser) rather than a strict XML processor.
func xmlEscape(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(s)
}

// deduplicateChunks removes duplicate or near-duplicate chunks using content signature
func (t *GrepChunksTool) deduplicateChunks(ctx context.Context, results []chunkWithTitle) []chunkWithTitle {
	seen := make(map[string]bool)
	contentSig := make(map[string]bool)
	uniqueResults := make([]chunkWithTitle, 0)

	for _, r := range results {
		keys := []string{r.ID}
		if r.ParentChunkID != "" {
			keys = append(keys, "parent:"+r.ParentChunkID)
		}
		if r.KnowledgeID != "" {
			keys = append(keys, fmt.Sprintf("kb:%s#%d", r.KnowledgeID, r.ChunkIndex))
		}

		dup := false
		for _, k := range keys {
			if seen[k] {
				dup = true
				break
			}
		}
		if dup {
			continue
		}

		sig := t.buildContentSignature(r.Content)
		if sig != "" {
			if contentSig[sig] {
				continue
			}
			contentSig[sig] = true
		}

		for _, k := range keys {
			seen[k] = true
		}
		uniqueResults = append(uniqueResults, r)
	}

	seenByID := make(map[string]bool)
	deduplicated := make([]chunkWithTitle, 0)
	for _, r := range uniqueResults {
		if !seenByID[r.ID] {
			seenByID[r.ID] = true
			deduplicated = append(deduplicated, r)
		}
	}
	_ = ctx
	return deduplicated
}

// buildContentSignature creates a normalized signature for content to detect near-duplicates
func (t *GrepChunksTool) buildContentSignature(content string) string {
	return searchutil.BuildContentSignature(content)
}

// scoreChunks calculates match scores for chunks based on regex matches.
func (t *GrepChunksTool) scoreChunks(
	ctx context.Context,
	results []chunkWithTitle,
	compiled []*regexp.Regexp,
) []chunkWithTitle {
	scored := make([]chunkWithTitle, len(results))
	for i := range results {
		scored[i] = results[i]
		score, patternCount := t.calculateMatchScore(results[i].Content, compiled)
		scored[i].MatchScore = score
		scored[i].MatchedPatterns = patternCount
	}
	_ = ctx
	return scored
}

// calculateMatchScore counts how many regex patterns match the content and
// applies a small boost for earlier match positions.
func (t *GrepChunksTool) calculateMatchScore(content string, compiled []*regexp.Regexp) (float64, int) {
	if content == "" || len(compiled) == 0 {
		return 0.0, 0
	}

	matchCount := 0
	earliestPos := len(content)

	for _, re := range compiled {
		if re == nil {
			continue
		}
		loc := re.FindStringIndex(content)
		if loc == nil {
			continue
		}
		matchCount++
		if loc[0] < earliestPos {
			earliestPos = loc[0]
		}
	}

	if matchCount == 0 {
		return 0.0, 0
	}

	baseScore := float64(matchCount) / float64(len(compiled))

	positionBonus := 0.0
	if earliestPos < len(content) {
		positionRatio := 1.0 - float64(earliestPos)/float64(len(content))
		positionBonus = positionRatio * 0.1
	}

	return math.Min(baseScore+positionBonus, 1.0), matchCount
}

// applyMMR applies Maximal Marginal Relevance algorithm to reduce redundancy
func (t *GrepChunksTool) applyMMR(
	ctx context.Context,
	results []chunkWithTitle,
	k int,
	lambda float64,
) []chunkWithTitle {
	if k <= 0 || len(results) == 0 {
		return nil
	}

	logger.Debugf(ctx, "[Tool][GrepChunks] Applying MMR: lambda=%.2f, k=%d, candidates=%d",
		lambda, k, len(results))

	selected := make([]chunkWithTitle, 0, k)
	selectedTokenSets := make([]map[string]struct{}, 0, k)

	candidates := make([]chunkWithTitle, len(results))
	copy(candidates, results)

	tokenSets := make([]map[string]struct{}, len(candidates))
	for i, r := range candidates {
		tokenSets[i] = t.tokenizeSimple(r.Content)
	}

	for len(selected) < k && len(candidates) > 0 {
		bestIdx := 0
		bestScore := -1.0

		for i, r := range candidates {
			relevance := r.MatchScore
			redundancy := 0.0
			for _, selectedTS := range selectedTokenSets {
				redundancy = math.Max(redundancy, t.jaccard(tokenSets[i], selectedTS))
			}
			mmr := lambda*relevance - (1.0-lambda)*redundancy
			if mmr > bestScore {
				bestScore = mmr
				bestIdx = i
			}
		}

		selected = append(selected, candidates[bestIdx])
		selectedTokenSets = append(selectedTokenSets, tokenSets[bestIdx])

		last := len(candidates) - 1
		candidates[bestIdx] = candidates[last]
		tokenSets[bestIdx] = tokenSets[last]
		candidates = candidates[:last]
		tokenSets = tokenSets[:last]
	}

	return selected
}

// tokenizeSimple tokenizes text into a set of words (simple whitespace-based)
func (t *GrepChunksTool) tokenizeSimple(text string) map[string]struct{} {
	return searchutil.TokenizeSimple(text)
}

// jaccard calculates Jaccard similarity between two token sets
func (t *GrepChunksTool) jaccard(a, b map[string]struct{}) float64 {
	return searchutil.Jaccard(a, b)
}
