package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

var grepChunksTool = BaseTool{
	name: ToolGrepChunks,
	description: `Unix-style text pattern matching tool for knowledge base chunks.

Searches for text patterns in chunk content using strict literal text matching (fixed-string search). This tool performs exact keyword lookup, not semantic search.

## Core Function
Performs exact, literal text pattern matching. Accepts multiple patterns and returns chunks matching any of them (OR logic).

## CRITICAL – Keyword Extraction Rules
This tool MUST receive **short, high-value keywords** only.  
**Do NOT use long phrases, sentences, or multi-word expressions.**

Provide only the **minimal core entities** extracted from user query, such as:
- Proper nouns
- Key concepts
- Domain terms
- Distinct entities that define the query

### Requirements
- Keywords should be **1–3 words maximum**
- Focus exclusively on **core entities**, not descriptions
- Break complex input into individual, essential keywords
- Avoid phrases, explanations, or anything that reduces match probability
- Preserve precision details embedded in the query (e.g., version numbers, build IDs) when they materially define the entity being matched.

Long phrases dramatically reduce recall because chunks rarely contain identical wording.  
Only short, atomic keywords ensure accurate matching and avoid unrelated retrieval.


## Usage
grep_chunks scans enabled chunks across the specified knowledge bases and returns those containing any provided keyword. Matching is case-insensitive, with chunk indices and local context included.

## When to Use
- Extracting core entities from user input
- Exact keyword presence checks
- Fast preliminary filtering before semantic search
- Situations requiring deterministic text search
`,
	schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "pattern": {
      "type": "array",
      "description": "REQUIRED: Text patterns to search for. Can be a single pattern or multiple patterns. Treated as literal text (fixed string matching). Results match any of the patterns (OR logic).",
      "items": {
        "type": "string"
      },
      "minItems": 1
    },
    "knowledge_base_ids": {
      "type": "array",
      "description": "Filter by knowledge base IDs. If empty, searches all allowed KBs.",
      "items": {
        "type": "string"
      }
    },
    "max_results": {
      "type": "integer",
      "description": "Maximum number of matching chunks to return (default: 50, max: 200)",
      "default": 50,
      "minimum": 1,
      "maximum": 200
    }
  },
  "required": ["pattern"]
}`),
}

// GrepChunksInput defines the input parameters for grep chunks tool
type GrepChunksInput struct {
	Pattern          []string `json:"pattern" `
	KnowledgeBaseIDs []string `json:"knowledge_base_ids,omitempty"`
	MaxResults       int      `json:"max_results,omitempty"`
}

// GrepChunksTool performs text pattern matching in knowledge base chunks
// Similar to grep command in Unix-like systems, but operates on knowledge base content
type GrepChunksTool struct {
	BaseTool
	db               *gorm.DB
	knowledgeBaseIDs []string
	knowledgeIDs     []string // Specific knowledge/document IDs to search within
}

// NewGrepChunksTool creates a new grep chunks tool
func NewGrepChunksTool(db *gorm.DB, knowledgeBaseIDs []string, knowledgeIDs []string) *GrepChunksTool {
	return &GrepChunksTool{
		BaseTool:         grepChunksTool,
		db:               db,
		knowledgeBaseIDs: knowledgeBaseIDs,
		knowledgeIDs:     knowledgeIDs,
	}
}

// Execute executes the grep chunks tool
func (t *GrepChunksTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][GrepChunks] Execute started")

	// Parse args from json.RawMessage
	var input GrepChunksInput
	if err := json.Unmarshal(args, &input); err != nil {
		logger.Errorf(ctx, "[Tool][GrepChunks] Failed to parse args: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, err
	}

	// Parse pattern parameter (required) - support multiple patterns
	patterns := input.Pattern

	// Validate patterns
	if len(patterns) == 0 {
		logger.Errorf(ctx, "[Tool][GrepChunks] Missing or invalid pattern parameter")
		return &types.ToolResult{
			Success: false,
			Error:   "pattern parameter is required and must contain at least one non-empty pattern",
		}, fmt.Errorf("missing pattern parameter")
	}

	// Use default values for all options
	countOnly := false // default: show results

	maxResults := 50
	if input.MaxResults > 0 {
		maxResults = input.MaxResults
		if maxResults < 1 {
			maxResults = 1
		} else if maxResults > 200 {
			maxResults = 200
		}
	}

	// Parse knowledge_base_ids filter
	kbIDs := input.KnowledgeBaseIDs
	if len(kbIDs) == 0 {
		kbIDs = t.knowledgeBaseIDs
	}

	// // Parse knowledge_ids filter
	// var knowledgeIDs []string
	// if knowledgeIDsRaw, ok := args["knowledge_ids"].([]interface{}); ok {
	// 	for _, id := range knowledgeIDsRaw {
	// 		if idStr, ok := id.(string); ok && idStr != "" {
	// 			knowledgeIDs = append(knowledgeIDs, idStr)
	// 		}
	// 	}
	// }

	logger.Infof(ctx, "[Tool][GrepChunks] Patterns: %v, MaxResults: %d, KnowledgeIDs: %v",
		patterns, maxResults, t.knowledgeIDs)

	// Build and execute query
	results, totalCount, err := t.searchChunks(ctx, patterns, kbIDs, t.knowledgeIDs)
	if err != nil {
		logger.Errorf(ctx, "[Tool][GrepChunks] Search failed: %v", err)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Search failed: %v", err),
		}, err
	}

	logger.Infof(ctx, "[Tool][GrepChunks] Found %d matching chunks", len(results))

	// Apply deduplication to remove duplicate or near-duplicate chunks
	deduplicatedResults := t.deduplicateChunks(ctx, results)
	logger.Infof(ctx, "[Tool][GrepChunks] After deduplication: %d chunks (from %d)",
		len(deduplicatedResults), len(results))

	// Calculate match scores for sorting (based on match count and position)
	scoredResults := t.scoreChunks(ctx, deduplicatedResults, patterns)

	// Apply MMR to reduce redundancy if we have many results
	finalResults := scoredResults
	if len(scoredResults) > 10 {
		// Use MMR when we have more than 10 results
		mmrK := len(scoredResults)
		if maxResults > 0 && mmrK > maxResults {
			mmrK = maxResults
		}
		logger.Debugf(
			ctx,
			"[Tool][GrepChunks] Applying MMR: k=%d, lambda=0.7, input=%d results",
			mmrK,
			len(scoredResults),
		)
		mmrResults := t.applyMMR(ctx, scoredResults, patterns, mmrK, 0.7)
		if len(mmrResults) > 0 {
			finalResults = mmrResults
			logger.Infof(ctx, "[Tool][GrepChunks] MMR completed: %d results selected", len(finalResults))
		}
	}

	// Sort by match score (descending), then by chunk index
	sort.Slice(finalResults, func(i, j int) bool {
		if finalResults[i].MatchedPatterns != finalResults[j].MatchedPatterns {
			return finalResults[i].MatchedPatterns > finalResults[j].MatchedPatterns
		}
		if finalResults[i].MatchScore != finalResults[j].MatchScore {
			return finalResults[i].MatchScore > finalResults[j].MatchScore
		}
		return finalResults[i].ChunkIndex < finalResults[j].ChunkIndex
	})

	aggregatedResults := t.aggregateByKnowledge(finalResults, patterns)

	totalKnowledge := len(aggregatedResults)

	if len(aggregatedResults) > 20 {
		aggregatedResults = aggregatedResults[:20]
	}

	logger.Infof(ctx, "[Tool][GrepChunks] Aggregated results: %d", len(aggregatedResults))

	// Format output
	output := t.formatOutput(ctx, aggregatedResults, totalCount, patterns, countOnly)

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"patterns":           patterns,
			"knowledge_results":  aggregatedResults,
			"result_count":       len(aggregatedResults),
			"total_matches":      totalKnowledge,
			"knowledge_base_ids": kbIDs,
			"max_results":        maxResults,
			"display_type":       "grep_results",
		},
	}, nil
}

type chunkWithTitle struct {
	types.Chunk
	KnowledgeTitle  string  `json:"knowledge_title"   gorm:"column:knowledge_title"`
	MatchScore      float64 `json:"match_score"       gorm:"column:match_score"` // Score based on match count and position
	MatchedPatterns int     `json:"matched_patterns"`                            // Number of unique patterns matched
	TotalChunkCount int     `json:"total_chunk_count" gorm:"column:total_chunk_count"`
}

// searchChunks performs the database search with pattern matching
func (t *GrepChunksTool) searchChunks(
	ctx context.Context,
	patterns []string,
	kbIDs []string,
	knowledgeIDs []string,
) ([]chunkWithTitle, int64, error) {
	tenantID := uint64(0)
	if tid, ok := ctx.Value(types.TenantIDContextKey).(uint64); ok {
		tenantID = tid
	}
	// Build base query
	query := t.db.Debug().WithContext(ctx).Table("chunks").
		Select("chunks.id, chunks.content, chunks.chunk_index, chunks.knowledge_id, chunks.knowledge_base_id, chunks.chunk_type, chunks.created_at, knowledges.title as knowledge_title, COUNT(*) OVER (PARTITION BY chunks.knowledge_id) AS total_chunk_count").
		Joins("LEFT JOIN knowledges ON chunks.knowledge_id = knowledges.id").
		Where("chunks.tenant_id = ?", tenantID).
		Where("chunks.is_enabled = ?", true).
		Where("chunks.deleted_at IS NULL").
		Where("knowledges.deleted_at IS NULL")

	// Apply knowledge IDs filter (specific documents) - takes priority over KB filter
	if len(knowledgeIDs) > 0 {
		query = query.Where("chunks.knowledge_id IN ?", knowledgeIDs)
		logger.Infof(ctx, "[Tool][GrepChunks] Filtering by %d specific knowledge IDs", len(knowledgeIDs))
	} else if len(kbIDs) > 0 {
		// Apply knowledge base filter only if no specific knowledge IDs
		query = query.Where("chunks.knowledge_base_id IN ?", kbIDs)
	}

	// Apply pattern matching (case-insensitive fixed string matching, OR logic for multiple patterns)
	if len(patterns) == 1 {
		query = query.Where("chunks.content ILIKE ?", "%"+patterns[0]+"%")
	} else {
		// Multiple patterns: use OR logic
		var conditions []string
		var args []interface{}
		for _, pattern := range patterns {
			conditions = append(conditions, "chunks.content ILIKE ?")
			args = append(args, "%"+pattern+"%")
		}
		query = query.Where("("+strings.Join(conditions, " OR ")+")", args...)
	}

	// Count total matches first (for count_only mode)
	var totalCount int64
	if err := query.Count(&totalCount).Error; err != nil {
		logger.Warnf(ctx, "[Tool][GrepChunks] Failed to count matches: %v", err)
	}

	// Fetch results
	var results []chunkWithTitle
	if err := query.Order("chunks.created_at DESC").Find(&results).Error; err != nil {
		logger.Errorf(ctx, "[Tool][GrepChunks] Failed to fetch results: %v", err)
		return nil, 0, err
	}

	return results, totalCount, nil
}

// formatOutput formats the search results for display (grep-style output)
func (t *GrepChunksTool) formatOutput(
	ctx context.Context,
	results []knowledgeAggregation,
	totalCount int64,
	patterns []string,
	countOnly bool,
) string {
	var output strings.Builder

	// If count_only mode, just return the count
	if countOnly {
		output.WriteString(fmt.Sprintf("%d\n", totalCount))
		return output.String()
	}

	// Show search info
	if len(patterns) == 1 {
		output.WriteString(fmt.Sprintf("Pattern: '%s' (case-insensitive)\n", patterns[0]))
	} else {
		output.WriteString(fmt.Sprintf("Patterns (%d): %v (case-insensitive, OR logic)\n", len(patterns), patterns))
	}
	output.WriteString(fmt.Sprintf("Matches: %d knowledge item(s)\n\n", len(results)))

	if len(results) == 0 {
		output.WriteString("No matches found.\n")
		return output.String()
	}

	for idx, result := range results {
		var patternSummaries []string
		for _, pattern := range patterns {
			count := result.PatternCounts[pattern]
			patternSummaries = append(patternSummaries, fmt.Sprintf("%s=%d", pattern, count))
		}

		output.WriteString(
			fmt.Sprintf("%d) knowledge_id=%s | title=%s | chunk_hits=%d | chunk_total=%d | pattern_hits=[%s]\n",
				idx+1,
				result.KnowledgeID,
				result.KnowledgeTitle,
				result.ChunkHitCount,
				result.TotalChunkCount,
				strings.Join(patternSummaries, ", "),
			),
		)
	}
	return output.String()
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

func (t *GrepChunksTool) aggregateByKnowledge(results []chunkWithTitle, patterns []string) []knowledgeAggregation {
	if len(results) == 0 {
		return nil
	}

	patternKeys := make([]string, 0, len(patterns))
	for _, p := range patterns {
		if strings.TrimSpace(p) == "" {
			continue
		}
		patternKeys = append(patternKeys, p)
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
				PatternCounts:   make(map[string]int, len(patternKeys)),
			}
			for _, pKey := range patternKeys {
				aggregated[knowledgeID].PatternCounts[pKey] = 0
			}
		}

		entry := aggregated[knowledgeID]
		entry.ChunkHitCount++

		patternOccurrences := t.countPatternOccurrences(chunk.Content, patternKeys)
		for _, p := range patternKeys {
			count := patternOccurrences[p]
			if count == 0 {
				continue
			}
			entry.PatternCounts[p] += count
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

func (t *GrepChunksTool) countPatternOccurrences(content string, patterns []string) map[string]int {
	counts := make(map[string]int, len(patterns))
	if content == "" || len(patterns) == 0 {
		return counts
	}

	contentLower := strings.ToLower(content)
	for _, pattern := range patterns {
		p := strings.ToLower(pattern)
		if strings.TrimSpace(p) == "" {
			continue
		}
		counts[pattern] = countOccurrences(contentLower, p)
	}
	return counts
}

func countOccurrences(text string, pattern string) int {
	if pattern == "" {
		return 0
	}
	count := 0
	index := 0
	for index < len(text) {
		pos := strings.Index(text[index:], pattern)
		if pos == -1 {
			break
		}
		count++
		index += pos + len(pattern)
	}
	return count
}

// deduplicateChunks removes duplicate or near-duplicate chunks using content signature
func (t *GrepChunksTool) deduplicateChunks(ctx context.Context, results []chunkWithTitle) []chunkWithTitle {
	seen := make(map[string]bool)
	contentSig := make(map[string]bool)
	uniqueResults := make([]chunkWithTitle, 0)

	for _, r := range results {
		// Build multiple keys for deduplication
		keys := []string{r.ID}
		if r.ParentChunkID != "" {
			keys = append(keys, "parent:"+r.ParentChunkID)
		}
		if r.KnowledgeID != "" {
			keys = append(keys, fmt.Sprintf("kb:%s#%d", r.KnowledgeID, r.ChunkIndex))
		}

		// Check if any key is already seen
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

		// Check content signature for near-duplicate content
		sig := t.buildContentSignature(r.Content)
		if sig != "" {
			if contentSig[sig] {
				continue
			}
			contentSig[sig] = true
		}

		// Mark all keys as seen
		for _, k := range keys {
			seen[k] = true
		}

		uniqueResults = append(uniqueResults, r)
	}

	// If we have duplicates by ID, keep the first one
	seenByID := make(map[string]bool)
	deduplicated := make([]chunkWithTitle, 0)
	for _, r := range uniqueResults {
		if !seenByID[r.ID] {
			seenByID[r.ID] = true
			deduplicated = append(deduplicated, r)
		}
	}

	return deduplicated
}

// buildContentSignature creates a normalized signature for content to detect near-duplicates
func (t *GrepChunksTool) buildContentSignature(content string) string {
	return searchutil.BuildContentSignature(content)
}

// scoreChunks calculates match scores for chunks based on pattern matches
func (t *GrepChunksTool) scoreChunks(
	ctx context.Context,
	results []chunkWithTitle,
	patterns []string,
) []chunkWithTitle {
	scored := make([]chunkWithTitle, len(results))
	for i := range results {
		scored[i] = results[i]
		score, patternCount := t.calculateMatchScore(results[i].Content, patterns)
		scored[i].MatchScore = score
		scored[i].MatchedPatterns = patternCount
	}
	return scored
}

// calculateMatchScore calculates a score based on how many patterns match and their positions
func (t *GrepChunksTool) calculateMatchScore(content string, patterns []string) (float64, int) {
	if content == "" || len(patterns) == 0 {
		return 0.0, 0
	}

	contentLower := strings.ToLower(content)
	matchCount := 0
	earliestPos := len(content)

	// Count how many patterns match and find earliest position
	for _, pattern := range patterns {
		patternLower := strings.ToLower(pattern)
		if strings.Contains(contentLower, patternLower) {
			matchCount++
			// Find position of first match
			pos := strings.Index(contentLower, patternLower)
			if pos >= 0 && pos < earliestPos {
				earliestPos = pos
			}
		}
	}

	// Score: higher for more matches, slightly higher for earlier positions
	// Base score: match ratio (0.0 to 1.0)
	baseScore := float64(matchCount) / float64(len(patterns))

	// Position bonus: earlier matches get slight boost (max 0.1)
	positionBonus := 0.0
	if earliestPos < len(content) {
		// Normalize position to [0, 1] and apply small bonus
		positionRatio := 1.0 - float64(earliestPos)/float64(len(content))
		positionBonus = positionRatio * 0.1
	}

	return math.Min(baseScore+positionBonus, 1.0), matchCount
}

// applyMMR applies Maximal Marginal Relevance algorithm to reduce redundancy
func (t *GrepChunksTool) applyMMR(
	ctx context.Context,
	results []chunkWithTitle,
	patterns []string,
	k int,
	lambda float64,
) []chunkWithTitle {
	if k <= 0 || len(results) == 0 {
		return nil
	}

	logger.Debugf(ctx, "[Tool][GrepChunks] Applying MMR: lambda=%.2f, k=%d, candidates=%d",
		lambda, k, len(results))

	selected := make([]chunkWithTitle, 0, k)
	candidates := make([]chunkWithTitle, len(results))
	copy(candidates, results)

	// Pre-compute token sets for all candidates
	tokenSets := make([]map[string]struct{}, len(candidates))
	for i, r := range candidates {
		tokenSets[i] = t.tokenizeSimple(r.Content)
	}

	// MMR selection loop
	for len(selected) < k && len(candidates) > 0 {
		bestIdx := 0
		bestScore := -1.0

		for i, r := range candidates {
			relevance := r.MatchScore
			redundancy := 0.0

			// Calculate maximum redundancy with already selected results
			for _, s := range selected {
				selectedTokens := t.tokenizeSimple(s.Content)
				redundancy = math.Max(redundancy, t.jaccard(tokenSets[i], selectedTokens))
			}

			// MMR score: balance relevance and diversity
			mmr := lambda*relevance - (1.0-lambda)*redundancy
			if mmr > bestScore {
				bestScore = mmr
				bestIdx = i
			}
		}

		// Add best candidate to selected and remove from candidates
		selected = append(selected, candidates[bestIdx])
		candidates = append(candidates[:bestIdx], candidates[bestIdx+1:]...)
		// Remove corresponding token set
		tokenSets = append(tokenSets[:bestIdx], tokenSets[bestIdx+1:]...)
	}

	// Compute average redundancy among selected results
	avgRed := 0.0
	if len(selected) > 1 {
		pairs := 0
		for i := 0; i < len(selected); i++ {
			for j := i + 1; j < len(selected); j++ {
				si := t.tokenizeSimple(selected[i].Content)
				sj := t.tokenizeSimple(selected[j].Content)
				avgRed += t.jaccard(si, sj)
				pairs++
			}
		}
		if pairs > 0 {
			avgRed /= float64(pairs)
		}
	}

	logger.Debugf(ctx, "[Tool][GrepChunks] MMR completed: selected=%d, avg_redundancy=%.4f",
		len(selected), avgRed)

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
