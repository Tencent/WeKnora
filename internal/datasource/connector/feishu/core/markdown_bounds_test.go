package core

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeTableBoundsUntrustedColumnCount(t *testing.T) {
	block := DocxBlock{BlockType: BlockTypeTable, Table: &BlockTable{
		Cells: []string{"cell"}, Property: &BlockTableProperty{ColumnSize: math.MaxInt},
	}}
	byID := map[string]DocxBlock{
		"cell": {BlockID: "cell", Children: []string{"text"}},
		"text": {BlockID: "text", BlockType: BlockTypeText, Text: txt("kept")},
	}
	require.NotPanics(t, func() { require.Contains(t, renderNativeTable(block, byID), "kept") })
	block.Table.Cells = nil
	require.NotPanics(t, func() { require.Empty(t, renderNativeTable(block, byID)) })
}

func TestNativeTableBoundsMergedRegions(t *testing.T) {
	rows := [][]string{{"anchor", ""}, {""}, {"", ""}}
	fillMergedCells(rows, 2, []BlockTableMergeInfo{{RowSpan: math.MaxInt, ColSpan: math.MaxInt}})
	require.Equal(t, [][]string{{"anchor", "anchor"}, {"anchor"}, {"anchor", "anchor"}}, rows)
	// A merge entry beyond the supplied cells cannot index or iterate an absent row.
	fillMergedCells(rows, 2, []BlockTableMergeInfo{
		{}, {}, {}, {}, {}, {}, {RowSpan: math.MaxInt, ColSpan: math.MaxInt},
	})
	require.Equal(t, "anchor", rows[0][0])
	require.NotPanics(t, func() { fillMergedCells(rows, 0, []BlockTableMergeInfo{{RowSpan: 2, ColSpan: 2}}) })
}
