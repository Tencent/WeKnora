package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestFormatWithinBudgetKeepsFittingBodiesAndExcerptsLongHit(t *testing.T) {
	tool := &SearchKnowledgeTool{}
	results := make([]*searchResultWithMeta, 3)
	for i, size := range []int{240, 140, 8000} {
		results[i] = &searchResultWithMeta{
			SearchResult: &types.SearchResult{
				ID: fmt.Sprintf("c%d", i), KnowledgeID: "doc", Content: strings.Repeat("\u6587", size),
			},
			KnowledgeBaseID: "kb",
		}
	}
	longBody := results[2].Content + " needle"
	results[2].Content = longBody
	results[2].KnowledgeFilename = "report.pdf"
	results[2].SourceLocators = types.SourceLocators{{Type: types.SourceLocatorPDF, Page: 3}}
	ctx := WithOutputBudget(context.Background(), 2500)
	budget := OutputBudget(ctx) * 4 / 5
	res, omitted := tool.formatWithinBudget(ctx, results, []string{"kb"}, "needle", SearchModeHybrid)
	rows := res.Data["results"].([]map[string]interface{})
	require.Zero(t, omitted)
	require.Len(t, rows, 3)
	require.LessOrEqual(t, utf8.RuneCountInString(res.Output), budget)
	for i, row := range rows[:2] {
		require.Equal(t, results[i].ID, row["chunk_id"])
		require.Equal(t, results[i].Content, row["content"])
		require.Equal(t, false, row["content_excerpt"])
	}
	require.Equal(t, "c2", rows[2]["chunk_id"])
	require.Equal(t, true, rows[2]["content_excerpt"])
	require.Less(t, utf8.RuneCountInString(rows[2]["content"].(string)), utf8.RuneCountInString(longBody))
	require.Contains(t, rows[2]["match_snippet"], "needle")
	require.Equal(t, "report.pdf", rows[2]["knowledge_filename"])
	require.Equal(t, results[2].SourceLocators, rows[2]["source_locators"])
	require.Equal(t, longBody, results[2].Content, "the source chunk was mutated")
}

func TestFormatWithinBudgetKeepsLargestRankedPrefixWhenMetadataOverflows(t *testing.T) {
	tool := &SearchKnowledgeTool{}
	const body = "short evidence"
	results := []*searchResultWithMeta{
		{SearchResult: &types.SearchResult{ID: "c0", Content: body}},
		{SearchResult: &types.SearchResult{ID: "c1", Content: body}},
		{SearchResult: &types.SearchResult{ID: "c2", KnowledgeTitle: strings.Repeat("x", 8000), Content: body}},
	}
	ctx := WithOutputBudget(context.Background(), 2500)
	res, omitted := tool.formatWithinBudget(ctx, results, []string{"kb"}, "evidence", SearchModeHybrid)
	rows := res.Data["results"].([]map[string]interface{})
	require.Equal(t, 1, omitted)
	require.Len(t, rows, 2)
	require.LessOrEqual(t, utf8.RuneCountInString(res.Output), 2000)
	for i, row := range rows {
		require.Equal(t, results[i].ID, row["chunk_id"])
		require.Equal(t, body, row["content"], "remaining budget should be returned to the retained hits")
	}
}
