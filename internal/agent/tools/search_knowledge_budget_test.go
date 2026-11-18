package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestFormatWithinBudgetKeepsLargestRankedPrefix(t *testing.T) {
	tool := &SearchKnowledgeTool{}
	results := make([]*searchResultWithMeta, 3)
	for i, size := range []int{240, 140, 8000} {
		results[i] = &searchResultWithMeta{
			SearchResult: &types.SearchResult{
				ID: fmt.Sprintf("c%d", i), KnowledgeID: "doc", Content: strings.Repeat("文", size),
			},
			KnowledgeBaseID: "kb",
		}
	}
	ctx := WithOutputBudget(context.Background(), 2500)
	budget := OutputBudget(ctx) * 4 / 5
	firstTwo := tool.formatOutput(ctx, results[:2], []string{"kb"}, "q", SearchModeHybrid)
	if size := utf8.RuneCountInString(firstTwo.Output); size > budget {
		t.Fatalf("test fixture: the first two rows take %d runes, over budget %d", size, budget)
	}
	res, omitted := tool.formatWithinBudget(ctx, results, []string{"kb"}, "q", SearchModeHybrid)
	rows, _ := res.Data["results"].([]map[string]interface{})
	if len(rows) != 2 || omitted != 1 {
		t.Fatalf("shown=%d omitted=%d, want 2 shown and 1 omitted", len(rows), omitted)
	}
	if rows[0]["chunk_id"] != "c0" || rows[1]["chunk_id"] != "c1" {
		t.Fatalf("unexpected ranked rows: %v", rows)
	}
}
