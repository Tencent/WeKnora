package chunker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/sourceloc"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestParentChildTableSourceOffsets(t *testing.T) {
	splitters := []struct {
		name  string
		split func(string, SplitterConfig, SplitterConfig) ParentChildResult
	}{
		{"legacy", SplitTextParentChild},
		{"adaptive", SplitParentChild},
		{"diagnostics", func(text string, parent, child SplitterConfig) ParentChildResult {
			result, _ := SplitParentChildWithDiagnostics(text, parent, child)
			return result
		}},
	}
	for _, fixture := range []struct {
		name, newline, row string
	}{
		{"ascii", "\n", "| apple | red |"},
		{"unicode", "\n", "| 苹果🍎 | 北京 |"},
		{"crlf", "\r\n", "| 苹果🍎 | 北京 |"},
	} {
		header := "| Item | City |" + fixture.newline + "| --- | --- |" + fixture.newline
		// Repeated rows also catch attempts to recover positions with a text search.
		text := "Intro" + fixture.newline + header +
			strings.Repeat(fixture.row+fixture.newline, 12) + "| last | row |" + fixture.newline
		parentCfg := SplitterConfig{
			ChunkSize: 80, ChunkOverlap: 5, Separators: []string{"\n\n", "\n"}, Strategy: StrategyLegacy,
		}
		// Ensure this exercises a parent with a synthetic header, not merely
		// children with headers inside a single source-backed parent.
		parents := SplitText(text, parentCfg)
		sawSyntheticParent := false
		for _, parent := range parents {
			if len([]rune(parent.Content)) > parent.End-parent.Start {
				sawSyntheticParent = true
			}
		}
		if !sawSyntheticParent {
			t.Fatalf("%s fixture did not produce a parent with a synthetic header", fixture.name)
		}
		for _, splitter := range splitters {
			// 12 isolates the virtual header as a child; 50 re-splits table
			// rows; 160 keeps the parent intact (ParentIndex == -1).
			for _, childSize := range []int{12, 50, 160} {
				t.Run(fmt.Sprintf("%s/%s/child=%d", fixture.name, splitter.name, childSize), func(t *testing.T) {
					childCfg := parentCfg
					childCfg.ChunkSize = childSize
					result := splitter.split(text, parentCfg, childCfg)
					if len(result.Children) == 0 {
						t.Fatal("expected children")
					}
					textRunes := []rune(text)
					covered := make([]bool, len(textRunes))
					var chunks []Chunk
					for i, child := range result.Children {
						if child.Seq != i {
							t.Errorf("child %d has sequence %d", i, child.Seq)
						}
						if child.Start < 0 || child.End > len(textRunes) || child.Start >= child.End {
							t.Fatalf("child %d has invalid source span [%d, %d), document length %d",
								i, child.Start, child.End, len(textRunes))
						}
						content := []rune(child.Content)
						span := child.End - child.Start
						if span > len(content) {
							t.Fatalf("child %d source span exceeds content length", i)
						}
						want := string(textRunes[child.Start:child.End])
						if got := string(content[len(content)-span:]); got != want {
							t.Fatalf("child %d source span [%d, %d): got suffix %q, want %q",
								i, child.Start, child.End, got, want)
						}
						if child.ParentIndex >= 0 {
							if child.ParentIndex >= len(result.Parents) {
								t.Fatalf("child %d has invalid parent index %d", i, child.ParentIndex)
							}
							parent := result.Parents[child.ParentIndex]
							if child.Start < parent.Start || child.End > parent.End {
								t.Errorf("child %d source span escapes its parent", i)
							}
						} else if childSize == 160 && strings.Contains(child.Content, fixture.row) {
							if !strings.Contains(child.Content, "| Item | City |") {
								t.Errorf("child %d lost table header context", i)
							}
						}
						for pos := child.Start; pos < child.End; pos++ {
							covered[pos] = true
						}
						chunks = append(chunks, child.Chunk)
					}
					for pos, ok := range covered {
						if !ok {
							t.Fatalf("source rune %d is missing from children", pos)
						}
					}
					if got := restoreTextFromChunks(chunks); got != text {
						t.Fatalf("source reconstruction mismatch: got %q, want %q", got, text)
					}
				})
			}
		}
	}
}

func TestParentChildTableCitationUsesSourceRow(t *testing.T) {
	prefix := "| Item | City |\n| --- | --- |\n" + strings.Repeat("| 苹果🍎 | 北京 |\n", 12)
	lastRow := "| last | row |\n"
	text := prefix + lastRow
	index := sourceloc.NewIndex(text, []types.SourceBlock{
		{Start: 0, End: len([]rune(prefix)), Locator: types.SourceLocator{
			Type: types.SourceLocatorSheet, Sheet: "Sheet1", RowStart: 1, RowEnd: 13,
		}},
		{Start: len([]rune(prefix)), End: len([]rune(text)), Locator: types.SourceLocator{
			Type: types.SourceLocatorSheet, Sheet: "Sheet1", RowStart: 14, RowEnd: 14,
		}},
	})
	parentCfg := SplitterConfig{
		ChunkSize: 80, ChunkOverlap: 5, Separators: []string{"\n\n", "\n"}, Strategy: StrategyLegacy,
	}
	childCfg := parentCfg
	childCfg.ChunkSize = 12
	result := SplitParentChild(text, parentCfg, childCfg)
	for _, child := range result.Children {
		if child.Content != lastRow {
			continue
		}
		// ProcessDocument assigns source evidence with these exact offsets.
		locators := index.Locators(child.Start, child.End)
		if len(locators) != 1 {
			t.Fatalf("last row has %d source locators, want one", len(locators))
		}
		loc := locators[0]
		if loc.Sheet != "Sheet1" || loc.RowStart != 14 || loc.RowEnd != 14 || loc.Quote != "| last | row |" {
			t.Fatalf("last row points to the wrong source: %+v", loc)
		}
		return
	}
	t.Fatal("last table row was not retained as a child")
}
