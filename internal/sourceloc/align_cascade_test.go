package sourceloc

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// Regression test for Tencent/WeKnora#5617: a unit whose text survives only
// in a displaced spot (here a figure caption at the end of the document,
// in the real report a title dropped from its page body) used to jump the
// match cursor past every later unit, so a whole section lost its source
// locators. The displaced unit itself stays unplaced, but the units after
// it must still align.
func TestAlignLocalizesDisplacedMatch(t *testing.T) {
	md := "引言内容\n\n第一章正文\n\n第二章标题\n\n第二章正文\n\n图注第一章标题示意图\n"
	units := []Unit{
		{Text: "引言内容", Locator: types.SourceLocator{Type: "pdf", Page: 1}},
		{Text: "第一章标题", Locator: types.SourceLocator{Type: "pdf", Page: 1}},
		{Text: "第一章正文", Locator: types.SourceLocator{Type: "pdf", Page: 1}},
		{Text: "第二章标题", Locator: types.SourceLocator{Type: "pdf", Page: 2}},
		{Text: "第二章正文", Locator: types.SourceLocator{Type: "pdf", Page: 2}},
	}
	blocks := Align(md, units)
	require.Len(t, blocks, 4, "only the displaced title is dropped, the rest still aligns")
	require.Equal(t, 1, blocks[0].Locator.Page)
	require.Equal(t, runeIndex(md, "引言内容"), blocks[0].Start)
	require.Equal(t, 1, blocks[1].Locator.Page)
	require.Equal(t, runeIndex(md, "第一章正文"), blocks[1].Start)
	require.Equal(t, 2, blocks[2].Locator.Page)
	require.Equal(t, runeIndex(md, "第二章标题"), blocks[2].Start)
	require.Equal(t, 2, blocks[3].Locator.Page)
	require.Equal(t, runeIndex(md, "第二章正文"), blocks[3].Start)
	// Nothing is attributed to the caption line itself.
	require.Empty(t, NewIndex(md, blocks).Locators(runeIndex(md, "图注"), len([]rune(md))))
}

// A reordered table must not orphan the paragraph that follows it in the
// source: the table loses its locator, the paragraph keeps its own.
func TestAlignLocalizesReorderedTable(t *testing.T) {
	md := "段落文字\n\n表格内容\n"
	units := []Unit{
		{Text: "表格内容", Locator: types.SourceLocator{Type: "pdf", Page: 1}},
		{Text: "段落文字", Locator: types.SourceLocator{Type: "pdf", Page: 1}},
	}
	blocks := Align(md, units)
	require.Len(t, blocks, 1)
	require.Equal(t, runeIndex(md, "段落文字"), blocks[0].Start)
}
