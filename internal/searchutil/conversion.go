package searchutil

import (
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

// ConvertWebResultOption configures ConvertWebSearchResults behavior.
type ConvertWebResultOption func(*convertWebResultOptions)

type convertWebResultOptions struct {
	seqFunc   func(idx int) int
	baseScore float64
}

// WithSeqFunc overrides the default sequence assignment for converted results.
func WithSeqFunc(f func(idx int) int) ConvertWebResultOption {
	return func(opts *convertWebResultOptions) {
		opts.seqFunc = f
	}
}

// WithBaseScore overrides the score converted results carry into the merged
// ranking. Zero keeps the built-in default, whose historical value made web
// hits outrank mediocre knowledge-base hits; workspaces can lower it via the
// retrieval config.
func WithBaseScore(score float64) ConvertWebResultOption {
	return func(opts *convertWebResultOptions) {
		opts.baseScore = score
	}
}

// ConvertWebSearchResults converts []*types.WebSearchResult into []*types.SearchResult.
func ConvertWebSearchResults(
	webResults []*types.WebSearchResult,
	opts ...ConvertWebResultOption,
) []*types.SearchResult {
	options := convertWebResultOptions{
		seqFunc:   func(int) int { return 1 },
		baseScore: types.DefaultWebSearchBaseScore,
	}
	for _, opt := range opts {
		opt(&options)
	}
	if options.baseScore <= 0 {
		options.baseScore = types.DefaultWebSearchBaseScore
	}

	results := make([]*types.SearchResult, 0, len(webResults))

	for i, webResult := range webResults {
		if webResult == nil {
			continue
		}

		chunkID := webResult.URL
		if chunkID == "" {
			chunkID = fmt.Sprintf("web_search_%d", i)
		}

		content := webResult.Title
		appendContent := func(text string) {
			if text == "" {
				return
			}
			if content != "" {
				content += "\n\n" + text
			} else {
				content = text
			}
		}

		appendContent(webResult.Snippet)
		appendContent(webResult.Content)

		result := &types.SearchResult{
			ID:             chunkID,
			Content:        content,
			KnowledgeID:    chunkID, // Use URL as KnowledgeID so each web result stays independent during merge
			ChunkIndex:     0,
			KnowledgeTitle: webResult.Title,
			StartAt:        0,
			EndAt:          utf8.RuneCountInString(content),
			Seq:            options.seqFunc(i),
			Score:          options.baseScore,
			MatchType:      types.MatchTypeWebSearch,
			SubChunkID:     []string{},
			Metadata: map[string]string{
				"url":     webResult.URL,
				"source":  webResult.Source,
				"title":   webResult.Title,
				"snippet": webResult.Snippet,
			},
			ChunkType:         string(types.ChunkTypeWebSearch),
			ParentChunkID:     "",
			ImageInfo:         "",
			KnowledgeFilename: "",
			KnowledgeSource:   "web_search",
		}

		if webResult.PublishedAt != nil {
			result.Metadata["published_at"] = webResult.PublishedAt.Format(time.RFC3339)
		}

		results = append(results, result)
	}

	return results
}
