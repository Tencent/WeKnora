package notion

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	tableast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

func TestBlocksToMarkdownMultilineTableCells(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		t.Run(fmt.Sprintf("ending=%q", ending), func(t *testing.T) {
			row := func(left, right string) notionBlock {
				return makeBlock(t, "table_row", map[string]interface{}{
					"cells": [][]notionRichText{
						{{Type: "text", PlainText: left}},
						{{Type: "text", PlainText: right}},
					},
				})
			}
			block := notionBlock{Type: "table", Children: []notionBlock{
				row("Name"+ending+"Label", "Value"),
				row("first"+ending+"second | third", "kept"),
			}}
			md, _ := BlocksToMarkdown([]notionBlock{block})
			if !strings.Contains(md, "| Name<br>Label | Value |") ||
				!strings.Contains(md, `| first<br>second \| third | kept |`) {
				t.Fatalf("multiline cell broke table: %q", md)
			}
			root := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader([]byte(md)))
			table, ok := root.FirstChild().(*tableast.Table)
			if !ok {
				t.Fatalf("first block = %T, want table", root.FirstChild())
			}
			if table.ChildCount() != 2 || table.NextSibling() != nil {
				t.Fatalf("expected exactly one header and one body row: %q", md)
			}
			for row := table.FirstChild(); row != nil; row = row.NextSibling() {
				if row.ChildCount() != 2 {
					t.Fatalf("row has %d cells, want 2", row.ChildCount())
				}
			}
		})
	}
}
