package sourceloc

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestAlignIndexesUniqueRowsInOnePass locks the large-table path: every row is
// a distinct unit, and all of them must still align despite the former
// per-key full-document scan. The linearity itself is covered by the benchmark.
func TestAlignIndexesUniqueRowsInOnePass(t *testing.T) {
	const rows = 2000
	var md strings.Builder
	md.WriteString("| id | value |\n| --- | --- |\n")
	units := make([]Unit, 0, rows)
	for i := 0; i < rows; i++ {
		text := fmt.Sprintf("row-%04d value-%04d", i, i)
		fmt.Fprintf(&md, "| row-%04d | value-%04d |\n", i, i)
		units = append(units, Unit{Text: text, Locator: types.SourceLocator{
			Type: "sheet", Sheet: "CSV", RowStart: i + 2, RowEnd: i + 2,
		}})
	}
	blocks := Align(md.String(), units)
	if len(blocks) != len(units) {
		t.Fatalf("aligned %d of %d unique rows", len(blocks), len(units))
	}
	for i, block := range blocks {
		if block.Locator.RowStart != units[i].Locator.RowStart {
			t.Fatalf("row %d mapped to source row %d", i, block.Locator.RowStart)
		}
	}
}

// Repeated keys must still be rejected unless the document and the source units
// account for every occurrence. Prefix bucketing must not turn a TOC duplicate
// into a false exact match.
func TestAlignPrefixIndexKeepsDuplicateSafety(t *testing.T) {
	units := []Unit{
		{Text: "设备验收合格", Locator: types.SourceLocator{Type: "pdf", Page: 1}},
	}
	if blocks := Align("目录：设备验收合格。\n正文：设备验收合格。", units); len(blocks) != 0 {
		t.Fatalf("unaccounted duplicate aligned: %#v", blocks)
	}
	units = append(units, Unit{Text: "设备验收合格", Locator: types.SourceLocator{Type: "pdf", Page: 2}})
	blocks := Align("设备验收合格\n设备验收合格", units)
	if len(blocks) != 2 || blocks[1].Locator.Page != 2 {
		t.Fatalf("accounted duplicates = %#v", blocks)
	}
}
