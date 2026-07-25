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
	description: `Search knowledge base chunk content with a single POSIX regular expression, applied directly in the database (PostgreSQL ~* / MySQL/SQLite REGEXP, case-insensitive). Behaves like ` + "`grep -E -i`" + `.
Pack multiple concepts into ONE regex using ` + "`|`" + ` alternation — do not call this tool repeatedly for synonyms.
Returns matching chunks with a short cN chunk source ID, a parent dN document ID, and a <match> snippet around the first match.
Examples:
- Alternation (RECOMMENDED): "stardust|skyvault|psionic" (matches any of the words)
- Multiple terms in order: "psionic.*engine" (matches both words in order)
- Word boundary / anchor: "\\brag\\b" or "^chapter\\s+\\d+"
- Plain text: "engine" (matches literal substring anywhere in chunk content)
IMPORTANT — JSON escaping: every backslash in a regex MUST be written as \\ inside the JSON tool arguments (e.g. to search for literal "C++" write "C\\+\\+", NOT "C\+\+"; for "\d+" write "\\d+"). Plain "\+" / "\d" etc. are invalid JSON escapes and will fail to parse.
Use this to locate candidate chunks by exact identifiers, error codes, product names, or recurring terms.

## Deep read after grep:
- **FAQ hit** (chunk type faq): call list_knowledge_chunks with **faq_id=cN** from the grep result (NOT the parent dN document ID).
- **Document hit**: call list_knowledge_chunks with **knowledge_id=dN**, or get_document_info with **knowledge_ids=[dN]**.`,
	schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "A single POSIX regex applied directly to chunk content (case-insensitive). Combine multiple concepts with \"|\" alternation in ONE regex (e.g. \"stardust|skyvault|psionic\") — do not split into multiple calls.",
      "minLength": 1
    }
  },
  "required": ["query"]
}`),
}

// GrepChunksInput defines the input parameters for grep chunks tool.
// The canonical parameter is a single `query` string (a regex with optional
// `|` alternation), matching real `grep -E` semantics. Legacy array forms
// (`queries`, `patterns`) and the singular `pattern` alias remain accepted
// so older model outputs or external callers don't break silently — they
// are joined together into a single alternation regex before execution.
type GrepChunksInput struct {
	Query string `json:"query,omitempty"`
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

	mu         sync.Mutex
	seenChunks map[string]bool
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

	// Resolve the canonical single-string `query`, falling back to legacy
	// aliases. Legacy array inputs are joined with `|` so they degrade into
	// a single alternation regex — preserving the previous "match ANY"
	// semantics without requiring multiple DB scans.
	query := strings.TrimSpace(input.Query)

	if query == "" {
		logger.Errorf(ctx, "[Tool][GrepChunks] Missing or empty query parameter")
		return &types.ToolResult{
			Success: false,
			Error:   "query parameter is required and must be a non-empty regex string",
		}, fmt.Errorf("missing query parameter")
	}

	// Compile with (?i) prefix for case-insensitive Go-side matching.
	// Compilation also validates the regex syntax before we send it to the DB.
	re, err := regexp.Compile("(?i)" + query)
	if err != nil {
		logger.Errorf(ctx, "[Tool][GrepChunks] Invalid regex %q: %v", query, err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("invalid regex query %q: %v", query, err),
		}, err
	}
	queries := []string{query}
	compiled := []*regexp.Regexp{re}

	// Result count is controlled by the backend, not the caller — keep it
	// bounded so the LLM context stays small regardless of regex breadth.
	const limit = 30

	kbTenantMap := t.searchTargets.GetKBTenantMap()
	fullKBIDs, knowledgeIDs, tagTargets := t.resolveGrepScope()
	kbIDsForMeta := fullKBIDs
	if len(kbIDsForMeta) == 0 {
		kbIDsForMeta = t.searchTargets.GetAllKnowledgeBaseIDs()
	}

	logger.Infof(ctx, "[Tool][GrepChunks] Queries: %v, Limit: %d, fullKBs: %d, knowledgeIDs: %d, tagScopes: %d",
		queries, limit, len(fullKBIDs), len(knowledgeIDs), len(tagTargets))

	results, err := t.searchChunks(ctx, queries, fullKBIDs, knowledgeIDs, tagTargets, kbTenantMap)
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
		// Title matches rank above everything else (see chunkWithTitle.TitleMatch).
		if finalResults[i].TitleMatch != finalResults[j].TitleMatch {
			return finalResults[i].TitleMatch
		}
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

	// chunk_results: per-chunk hits for UI detail (grouped by knowledge_id on the
	// frontend). knowledge_results: pre-aggregated per document for summaries;
	// document_count is the full distinct-document total even when the knowledge
	// list is truncated for payload size.
	chunkResults := buildGrepChunkResults(finalResults, compiled)
	aggregatedResults := t.aggregateByKnowledge(finalResults, queries, compiled)
	documentCount := len(aggregatedResults)
	knowledgeResultsForUI := aggregatedResults
	const maxKnowledgeRows = 20
	if len(knowledgeResultsForUI) > maxKnowledgeRows {
		knowledgeResultsForUI = knowledgeResultsForUI[:maxKnowledgeRows]
	}

	output := t.formatOutput(ctx, finalResults, queries, compiled)

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"query":              query,
			"queries":            queries, // legacy alias for older frontends
			"patterns":           queries, // legacy alias for older frontends
			"chunk_results":      chunkResults,
			"knowledge_results":  knowledgeResultsForUI,
			"result_count":       len(chunkResults),
			"document_count":     documentCount,
			"total_matches":      len(finalResults),
			"knowledge_base_ids": kbIDsForMeta,
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
	// TitleMatch is true when the query regex matches the owning knowledge's
	// TITLE (not just chunk body). A doc literally titled "图片素材" is the most
	// on-topic hit for the query "图片素材", yet its body may mention the term far
	// less than long FAQ docs that repeat it — so title hits are floated to the
	// very top of both the per-chunk and per-knowledge ordering.
	TitleMatch      bool `json:"title_match"`
	TotalChunkCount int  `json:"total_chunk_count" gorm:"column:total_chunk_count"`
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

// resolveGrepScope splits search targets into full-KB, specific-knowledge, and
// tag-constrained KB scopes. Tag scopes stay as tag IDs so the chunk query can
// use the knowledge_tag_relations index instead of expanding a large tag into a
// huge knowledge_id IN list.
func (t *GrepChunksTool) resolveGrepScope() (fullKBIDs, knowledgeIDs []string, tagTargets []*types.SearchTarget) {
	seenKB := make(map[string]bool)
	seenKnowledge := make(map[string]bool)
	seenTagScope := make(map[string]bool)
	for _, target := range t.searchTargets {
		if target == nil || target.KnowledgeBaseID == "" {
			continue
		}
		switch {
		case len(target.KnowledgeIDs) > 0:
			for _, kid := range target.KnowledgeIDs {
				if kid != "" && !seenKnowledge[kid] {
					seenKnowledge[kid] = true
					knowledgeIDs = append(knowledgeIDs, kid)
				}
			}
		case len(target.TagIDs) > 0:
			tenantID := target.TenantID
			if tenantID == 0 {
				tenantID = t.searchTargets.GetTenantIDForKB(target.KnowledgeBaseID)
			}
			tagIDs := dedupNonEmptyStrings(target.TagIDs)
			if len(tagIDs) == 0 || tenantID == 0 {
				continue
			}
			scopeKey := fmt.Sprintf("%s:%d:%s", target.KnowledgeBaseID, tenantID, strings.Join(tagIDs, "\x00"))
			if seenTagScope[scopeKey] {
				continue
			}
			seenTagScope[scopeKey] = true
			tagTargets = append(tagTargets, &types.SearchTarget{
				Type:            types.SearchTargetTypeKnowledgeBase,
				KnowledgeBaseID: target.KnowledgeBaseID,
				TenantID:        tenantID,
				TagIDs:          tagIDs,
			})
		default:
			if !seenKB[target.KnowledgeBaseID] {
				seenKB[target.KnowledgeBaseID] = true
				fullKBIDs = append(fullKBIDs, target.KnowledgeBaseID)
			}
		}
	}
	return fullKBIDs, knowledgeIDs, tagTargets
}

func dedupNonEmptyStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// scopeClause builds the SQL predicate restricting chunks to the active scope.
// Specific knowledge IDs, tag-constrained KB scopes, and full-KB (kb, tenant)
// pairs are OR'd together so a turn that mentions @KB + @tag + @file searches
// every requested scope. Returns ("", nil) when no usable scope exists.
func scopeClause(
	kbIDs, knowledgeIDs []string,
	tagTargets []*types.SearchTarget,
	kbTenantMap map[string]uint64,
) (string, []interface{}) {
	var clauses []string
	var args []interface{}
	if len(knowledgeIDs) > 0 {
		clauses = append(clauses, "chunks.knowledge_id IN ?")
		args = append(args, knowledgeIDs)
	}
	for _, target := range tagTargets {
		if target == nil || target.KnowledgeBaseID == "" || len(target.TagIDs) == 0 {
			continue
		}
		tenantID := target.TenantID
		if tenantID == 0 {
			tenantID = kbTenantMap[target.KnowledgeBaseID]
		}
		if tenantID == 0 {
			continue
		}
		clauses = append(clauses,
			"(chunks.knowledge_base_id = ? AND chunks.tenant_id = ? AND EXISTS ("+
				"SELECT 1 FROM knowledge_tag_relations ktr "+
				"WHERE ktr.knowledge_id = chunks.knowledge_id AND ktr.tag_id IN ?"+
				"))")
		args = append(args, target.KnowledgeBaseID, tenantID, target.TagIDs)
	}
	for _, kbID := range kbIDs {
		tenantID := kbTenantMap[kbID]
		if tenantID == 0 {
			continue
		}
		clauses = append(clauses, "(chunks.knowledge_base_id = ? AND chunks.tenant_id = ?)")
		args = append(args, kbID, tenantID)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return "(" + strings.Join(clauses, " OR ") + ")", args
}

// searchChunks performs the database search using regex queries.
func (t *GrepChunksTool) searchChunks(
	ctx context.Context,
	queries []string,
	kbIDs []string,
	knowledgeIDs []string,
	tagTargets []*types.SearchTarget,
	kbTenantMap map[string]uint64,
) ([]chunkWithTitle, error) {
	if len(kbIDs) == 0 && len(knowledgeIDs) == 0 && len(tagTargets) == 0 {
		logger.Warnf(ctx, "[Tool][GrepChunks] No kbIDs, knowledgeIDs, or tag scopes specified, returning empty results")
		return nil, nil
	}

	regexOp := t.regexOperatorForDialect()

	query := t.db.WithContext(ctx).Table("chunks").
		Select("chunks.id, chunks.content, chunks.chunk_index, chunks.knowledge_id, "+
			"chunks.knowledge_base_id, chunks.chunk_type, chunks.metadata, chunks.recall_weight, chunks.created_at, "+
			"knowledges.title as knowledge_title").
		Joins("JOIN knowledges ON chunks.knowledge_id = knowledges.id").
		Where("chunks.is_enabled = ?", true).
		Where("chunks.deleted_at IS NULL").
		Where("knowledges.deleted_at IS NULL")

	// Combine specific knowledge IDs, tag scopes, and full-KB scopes with OR so
	// that mixing @KB with @tag/@file searches BOTH, mirroring knowledge_search's
	// concurrent fan-out instead of dropping one side.
	scopeSQL, scopeArgs := scopeClause(kbIDs, knowledgeIDs, tagTargets, kbTenantMap)
	if scopeSQL == "" {
		logger.Warnf(ctx, "[Tool][GrepChunks] No valid scope (kb-tenant pairs / knowledge IDs)")
		return nil, nil
	}
	logger.Infof(ctx, "[Tool][GrepChunks] Scope: %d knowledge IDs, %d tag scopes, %d KBs",
		len(knowledgeIDs), len(tagTargets), len(kbIDs))
	query = query.Where(scopeSQL, scopeArgs...)

	// For MySQL/SQLite REGEXP case-insensitivity we rely on the column's default
	// collation (utf8mb4_general_ci etc.) OR the driver's REGEXP implementation,
	// which mirrors what wiki_search already ships in this codebase.
	var regexConditions []string
	var regexArgs []interface{}
	for _, q := range queries {
		// Match the regex against either the chunk body OR the owning
		// knowledge's title, so a doc whose title matches (e.g. titled
		// "图片素材") surfaces even when its body rarely repeats the term.
		regexConditions = append(regexConditions,
			fmt.Sprintf("(chunks.content %s ? OR knowledges.title %s ?)", regexOp, regexOp))
		regexArgs = append(regexArgs, q, q)
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
		snippet := extractChunkMatchSnippet(&r.Chunk, compiled)

		extraAttr := ""
		if q := faqStandardQuestion(&r.Chunk); q != "" {
			extraAttr = fmt.Sprintf(" faq_question=\"%s\"", xmlEscape(q))
		}
		isFAQ := r.ChunkType == types.ChunkTypeFAQ

		t.mu.Lock()
		seen := t.seenChunks[r.ID]
		t.seenChunks[r.ID] = true
		t.mu.Unlock()

		if isFAQ {
			if seen {
				fmt.Fprintf(&b,
					"<faq faq_id=\"%s\" knowledge_title=\"%s\"%s index=\"%d\" score=\"%.3f\" already_seen=\"true\">\n",
					xmlEscape(r.ID), xmlEscape(r.KnowledgeTitle),
					extraAttr, r.ChunkIndex, r.MatchScore)
			} else {
				fmt.Fprintf(&b,
					"<faq faq_id=\"%s\" knowledge_title=\"%s\"%s index=\"%d\" score=\"%.3f\">\n",
					xmlEscape(r.ID), xmlEscape(r.KnowledgeTitle),
					extraAttr, r.ChunkIndex, r.MatchScore)
			}
		} else if seen {
			fmt.Fprintf(&b,
				"<chunk chunk_id=\"%s\" knowledge_id=\"%s\" knowledge_title=\"%s\"%s chunk_index=\"%d\" score=\"%.3f\" already_seen=\"true\">\n",
				xmlEscape(r.ID), xmlEscape(r.KnowledgeID), xmlEscape(r.KnowledgeTitle),
				extraAttr, r.ChunkIndex, r.MatchScore)
		} else {
			fmt.Fprintf(&b,
				"<chunk chunk_id=\"%s\" knowledge_id=\"%s\" knowledge_title=\"%s\"%s chunk_index=\"%d\" score=\"%.3f\">\n",
				xmlEscape(r.ID), xmlEscape(r.KnowledgeID), xmlEscape(r.KnowledgeTitle),
				extraAttr, r.ChunkIndex, r.MatchScore)
		}

		for _, q := range queries {
			if c := counts[q]; c > 0 {
				b.WriteString(fmt.Sprintf("<query_hit query=\"%s\" count=\"%d\" />\n",
					xmlEscape(q), c))
			}
		}
		if seen {
			b.WriteString("<note>(snippet omitted, already returned in a previous grep_chunks call this session)</note>\n")
		} else if snippet != "" {
			b.WriteString(fmt.Sprintf("<match_snippet>%s</match_snippet>\n", xmlEscape(snippet)))
		}
		if isFAQ {
			b.WriteString("</faq>\n")
		} else {
			b.WriteString("</chunk>\n")
		}
	}

	b.WriteString("</grep_results>")
	_ = ctx
	return b.String()
}

type knowledgeAggregation struct {
	KnowledgeID     string `json:"knowledge_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	KnowledgeTitle  string `json:"knowledge_title"`
	// FAQQuestion is the standard question of the first matched FAQ entry in
	// this knowledge. FAQ entries share the owning knowledge's title, so the
	// frontend uses this to give the row a distinct, human-readable label.
	FAQQuestion      string         `json:"faq_question,omitempty"`
	TitleMatch       bool           `json:"title_match"`
	ChunkHitCount    int            `json:"chunk_hit_count"`
	TotalChunkCount  int            `json:"total_chunk_count"`
	PatternCounts    map[string]int `json:"pattern_counts"`
	TotalPatternHits int            `json:"total_pattern_hits"`
	DistinctPatterns int            `json:"distinct_patterns"`
	MatchSnippet     string         `json:"match_snippet,omitempty"`
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
		if chunk.TitleMatch {
			entry.TitleMatch = true
		}
		if entry.FAQQuestion == "" {
			if q := faqStandardQuestion(&chunk.Chunk); q != "" {
				entry.FAQQuestion = q
			}
		}
		if entry.MatchSnippet == "" {
			if snippet := extractChunkMatchSnippet(&chunk.Chunk, compiled); snippet != "" {
				entry.MatchSnippet = snippet
			}
		}

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
		// A knowledge whose TITLE matches the query is the most on-topic hit
		// and always ranks first, regardless of body keyword frequency.
		if resultSlice[i].TitleMatch != resultSlice[j].TitleMatch {
			return resultSlice[i].TitleMatch
		}
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

type grepChunkResult struct {
	ChunkID         string  `json:"chunk_id,omitempty"`
	FAQID           string  `json:"faq_id,omitempty"`
	KnowledgeID     string  `json:"knowledge_id"`
	KnowledgeBaseID string  `json:"knowledge_base_id"`
	KnowledgeTitle  string  `json:"knowledge_title"`
	ChunkType       string  `json:"chunk_type"`
	Index           int     `json:"index,omitempty"`
	ChunkIndex      int     `json:"chunk_index,omitempty"`
	FAQQuestion     string  `json:"faq_question,omitempty"`
	TitleMatch      bool    `json:"title_match,omitempty"`
	MatchSnippet    string  `json:"match_snippet,omitempty"`
	Score           float64 `json:"score"`
}

func buildGrepChunkResults(results []chunkWithTitle, compiled []*regexp.Regexp) []grepChunkResult {
	if len(results) == 0 {
		return nil
	}
	out := make([]grepChunkResult, 0, len(results))
	for _, r := range results {
		item := grepChunkResult{
			KnowledgeID:     r.KnowledgeID,
			KnowledgeBaseID: r.KnowledgeBaseID,
			KnowledgeTitle:  r.KnowledgeTitle,
			ChunkType:       string(r.ChunkType),
			TitleMatch:      r.TitleMatch,
			MatchSnippet:    extractChunkMatchSnippet(&r.Chunk, compiled),
			Score:           r.MatchScore,
		}
		if r.ChunkType == types.ChunkTypeFAQ {
			item.FAQID = r.ID
			item.Index = r.ChunkIndex
			item.FAQQuestion = faqStandardQuestion(&r.Chunk)
		} else {
			item.ChunkID = r.ID
			item.ChunkIndex = r.ChunkIndex
		}
		out = append(out, item)
	}
	return out
}

// regexMatchesAny reports whether text matches at least one of the compiled
// patterns. Used to flag title hits without counting occurrences.
func regexMatchesAny(text string, compiled []*regexp.Regexp) bool {
	if text == "" || len(compiled) == 0 {
		return false
	}
	for _, re := range compiled {
		if re != nil && re.MatchString(text) {
			return true
		}
	}
	return false
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

// extractChunkMatchSnippet returns a preview for tool output. FAQ chunks only
// surface the matched question plus answers from metadata (answers are not
// stored in chunk content for question_only index mode). Other chunk types
// use regex context around the first body match.
func extractChunkMatchSnippet(chunk *types.Chunk, compiled []*regexp.Regexp) string {
	if chunk != nil && chunk.ChunkType == types.ChunkTypeFAQ {
		if s := faqMatchSnippet(chunk, compiled); s != "" {
			return s
		}
	}
	if chunk == nil {
		return ""
	}
	return extractSnippetRegex(chunk.Content, compiled)
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

	beforeRunes := []rune(before)
	if len(beforeRunes) > snippetContextRunes {
		beforeRunes = beforeRunes[len(beforeRunes)-snippetContextRunes:]
	}
	afterRunes := []rune(after)
	if len(afterRunes) > snippetContextRunes {
		afterRunes = afterRunes[:snippetContextRunes]
	}
	matchRunes := []rune(matchStr)
	if len(matchRunes) > snippetMaxMatchRunes {
		matchRunes = append(matchRunes[:snippetMaxMatchRunes], []rune("...")...)
	}

	snippet := string(beforeRunes) + string(matchRunes) + string(afterRunes)
	snippet = strings.ReplaceAll(snippet, "\n", " ")
	for strings.Contains(snippet, "  ") {
		snippet = strings.ReplaceAll(snippet, "  ", " ")
	}
	snippet = strings.TrimSpace(snippet)
	if len([]rune(snippet)) > snippetMaxTotalRunes {
		snippet = string([]rune(snippet)[:snippetMaxTotalRunes]) + "..."
	}
	return "... " + snippet + " ..."
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
		// Title-aware boost: when the owning knowledge's TITLE matches the
		// query, treat the chunk as highly relevant regardless of how often
		// the body repeats the term. The boost keeps such chunks alive through
		// MMR selection; TitleMatch is the primary sort key downstream so they
		// also land at the very top of the final ordering.
		if regexMatchesAny(results[i].KnowledgeTitle, compiled) {
			scored[i].TitleMatch = true
			score = math.Min(score+0.5, 1.0)
			if patternCount == 0 {
				// Title-only recall (body never matched the regex) still counts
				// as one matched pattern so it isn't sorted below true zeros.
				patternCount = 1
			}
		}
		scored[i].MatchScore = math.Min(score*results[i].EffectiveRecallWeight(), 1.0)
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
