// Package handler — chunker_debug.go exposes a read-only preview endpoint
// that runs the adaptive chunker on supplied text without touching the DB
// or generating embeddings. Used by the KB editor's debug panel so users
// can experiment with chunking parameters before committing to a re-index.
package handler

import (
	"math"
	"net/http"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/gin-gonic/gin"
)

// previewMaxChars caps the input text size so callers can't tie up the
// server with arbitrarily large payloads. Chosen to stay well below the
// 256 KB byte limit even for ASCII (256 KB / ~1 byte/rune).
const previewMaxChars = 256 * 1024

// previewMaxChunks caps the number of chunks returned in a single preview
// response so the UI doesn't choke on pathological splits.
const previewMaxChunks = 500

// PreviewChunkingRequest is the body shape accepted by /chunker/preview.
type PreviewChunkingRequest struct {
	Text           string                 `json:"text" binding:"required"`
	ChunkingConfig PreviewChunkingPayload `json:"chunking_config"`
}

// PreviewChunkingPayload mirrors the snake_case JSON the rest of the API
// uses for ChunkingConfig fields. We don't reuse types.ChunkingConfig
// directly because it carries a lot of unrelated fields (parser engine
// rules, parent-child sizes, etc.) that the preview path doesn't need.
type PreviewChunkingPayload struct {
	ChunkSize    int      `json:"chunk_size"`
	ChunkOverlap int      `json:"chunk_overlap"`
	Separators   []string `json:"separators"`
	Strategy     string   `json:"strategy"`
	TokenLimit   int      `json:"token_limit"`
	Languages    []string `json:"languages"`
}

// PreviewChunkResult describes one chunk emitted during preview.
type PreviewChunkResult struct {
	Seq              int    `json:"seq"`
	Start            int    `json:"start"`
	End              int    `json:"end"`
	SizeChars        int    `json:"size_chars"`
	SizeTokensApprox int    `json:"size_tokens_approx"`
	ContextHeader    string `json:"context_header,omitempty"`
	Content          string `json:"content"`
}

// PreviewChunkingStats summarizes chunk-size distribution.
type PreviewChunkingStats struct {
	Count        int     `json:"count"`
	AvgChars     int     `json:"avg_chars"`
	MinChars     int     `json:"min_chars"`
	MaxChars     int     `json:"max_chars"`
	StddevChars  int     `json:"stddev_chars"`
	TruncatedTo  int     `json:"truncated_to,omitempty"` // set when chunks were truncated for the response
}

// PreviewChunkingResponse is the body returned by /chunker/preview.
type PreviewChunkingResponse struct {
	SelectedTier chunker.StrategyTier    `json:"selected_tier"`
	TierChain    []chunker.StrategyTier  `json:"tier_chain"`
	Rejected     []chunker.TierRejection `json:"rejected"`
	Profile      *chunker.DocProfile     `json:"profile"`
	Chunks       []PreviewChunkResult    `json:"chunks"`
	Stats        PreviewChunkingStats    `json:"stats"`
}

// PreviewChunking handles POST /chunker/preview. It runs the supplied text
// through the adaptive chunker and returns the chunks plus diagnostic
// information about which tier won. Read-only: no DB writes, no embedding
// calls, no logging of the supplied text.
func PreviewChunking(c *gin.Context) {
	ctx := c.Request.Context()

	var req PreviewChunkingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid request body: " + err.Error()})
		return
	}

	if len([]rune(req.Text)) > previewMaxChars {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"success": false,
			"error":   "text exceeds preview limit",
			"limit":   previewMaxChars,
		})
		return
	}

	cfg := chunker.SplitterConfig{
		ChunkSize:    req.ChunkingConfig.ChunkSize,
		ChunkOverlap: req.ChunkingConfig.ChunkOverlap,
		Separators:   req.ChunkingConfig.Separators,
		Strategy:     req.ChunkingConfig.Strategy,
		TokenLimit:   req.ChunkingConfig.TokenLimit,
		Languages:    req.ChunkingConfig.Languages,
	}

	chunks, diag := chunker.SplitWithDiagnostics(req.Text, cfg)
	profile := chunker.ProfileDocument(req.Text)

	logger.Debugf(ctx, "chunker preview: tier=%s chunks=%d", diag.SelectedTier, len(chunks))

	totalCount := len(chunks)
	truncatedTo := 0
	if totalCount > previewMaxChunks {
		truncatedTo = totalCount
		chunks = chunks[:previewMaxChunks]
	}

	lang := chunker.LangMixed
	if len(profile.DetectedLangs) > 0 {
		lang = profile.DetectedLangs[0]
	}

	results := make([]PreviewChunkResult, 0, len(chunks))
	for _, ch := range chunks {
		runeLen := len([]rune(ch.Content))
		results = append(results, PreviewChunkResult{
			Seq:              ch.Seq,
			Start:            ch.Start,
			End:              ch.End,
			SizeChars:        runeLen,
			SizeTokensApprox: chunker.ApproxTokenCount(ch.Content, lang),
			ContextHeader:    ch.ContextHeader,
			Content:          ch.Content,
		})
	}

	resp := PreviewChunkingResponse{
		SelectedTier: diag.SelectedTier,
		TierChain:    diag.TierChain,
		Rejected:     diag.Rejected,
		Profile:      profile,
		Chunks:       results,
		Stats:        chunkStats(results, truncatedTo),
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": resp})
}

// chunkStats computes count / avg / min / max / stddev over the chunks'
// SizeChars values. Returns zeroes for an empty slice.
func chunkStats(chunks []PreviewChunkResult, truncatedTo int) PreviewChunkingStats {
	stats := PreviewChunkingStats{Count: len(chunks), TruncatedTo: truncatedTo}
	if len(chunks) == 0 {
		return stats
	}
	var sum, sumSq float64
	minLen, maxLen := math.MaxInt32, 0
	for _, ch := range chunks {
		l := ch.SizeChars
		sum += float64(l)
		sumSq += float64(l * l)
		if l < minLen {
			minLen = l
		}
		if l > maxLen {
			maxLen = l
		}
	}
	avg := sum / float64(len(chunks))
	variance := sumSq/float64(len(chunks)) - avg*avg
	if variance < 0 {
		variance = 0
	}
	stats.AvgChars = int(avg + 0.5)
	stats.MinChars = minLen
	stats.MaxChars = maxLen
	stats.StddevChars = int(math.Sqrt(variance) + 0.5)
	return stats
}
