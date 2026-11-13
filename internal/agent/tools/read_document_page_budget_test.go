package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/modelcontext"
)

func TestReadDocumentPageBudgetsEscapedContentAndPreservesCursor(t *testing.T) {
	tool, repo := newReadDocumentFixture(3)
	repo.ordered[0].Content = "first " + strings.Repeat("&", 2800)
	repo.ordered[1].Content = "second " + strings.Repeat("&", 2800)
	repo.ordered[2].Content = "last"
	var got []string
	for offset := 0; offset < len(repo.ordered); {
		input := json.RawMessage(fmt.Sprintf(`{"id":"doc-1","offset":%d}`, offset))
		result, err := tool.Execute(t.Context(), input)
		if err != nil || !result.Success {
			t.Fatalf("read at offset %d: result=%+v err=%v", offset, result, err)
		}
		output := modelcontext.NewRegistry(true).ModelToolResultForTool(ToolReadDocument, result)
		if size := utf8.RuneCountInString(output); size > DefaultMaxToolOutput {
			t.Fatalf("model received %d runes at offset %d, budget is %d", size, offset, DefaultMaxToolOutput)
		}
		ids := chunkIDsFromData(t, result.Data)
		got = append(got, ids...)
		offset += len(ids)
		if len(ids) == 0 {
			t.Fatal("page did not advance")
		}
		if offset < len(repo.ordered) && result.Data["next_offset"] != offset {
			t.Fatalf("next offset = %v, want %d", result.Data["next_offset"], offset)
		}
	}
	if strings.Join(got, ",") != "chunk-0,chunk-1,chunk-2" {
		t.Fatalf("unexpected pages: %v", got)
	}
}
