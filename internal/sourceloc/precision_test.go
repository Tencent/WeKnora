package sourceloc

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAlignNeverAttributesMissingBlockToPreviousPage(t *testing.T) {
	md := "第一段正确文本。\n第二段实际内容没有匹配。\n第三段正确文本。"
	units := []Unit{
		{Text: "第一段正确文本", Locator: types.SourceLocator{Type: "pdf", Page: 1}},
		{Text: "第二段OCR错误内容", Locator: types.SourceLocator{Type: "pdf", Page: 2}},
		{Text: "第三段正确文本", Locator: types.SourceLocator{Type: "pdf", Page: 3}},
	}
	index := NewIndex(md, Align(md, units))
	at := runeIndex(md, "第二段")
	require.Empty(t, index.Locators(at, at+5))
	require.Equal(t, 3, index.Locators(runeIndex(md, "第三段"), len([]rune(md)))[0].Page)
}

func TestAlignRequiresFullUnitNotCommonPrefix(t *testing.T) {
	prefix := strings.Repeat("共同前缀", 12)
	require.Empty(t,
		Align(prefix+"验收不合格",
			[]Unit{{
				Text: prefix + "验收合格",
				Locator: types.SourceLocator{
					Type: "pdf",
					Page: 1,
				},
			}}))
}

func TestAlignPreservesNumericMeaning(t *testing.T) {
	for _, pair := range [][2]string{{"数值15毫米", "数值1.5毫米"}, {"温度5度", "温度-5度"}, {"比例5", "比例5%"}, {"值≤5", "值≥5"}} {
		require.Empty(t,
			Align(pair[0],
				[]Unit{{
					Text: pair[1],
					Locator: types.SourceLocator{
						Type: "pdf",
						Page: 1,
					},
				}}),
			pair)
	}
}

func TestAlignRejectsUnaccountedDuplicate(t *testing.T) {
	require.Empty(t,
		Align("目录：设备验收合格。\n正文：设备验收合格。",
			[]Unit{{
				Text: "设备验收合格",
				Locator: types.SourceLocator{
					Type: "pdf",
					Page: 9,
				},
			}}))
	units := []Unit{
		{
			Text: "相同段落内容",
			Locator: types.SourceLocator{
				Type: "pdf",
				Page: 1,
			},
		},
		{
			Text: "相同段落内容",
			Locator: types.SourceLocator{
				Type: "pdf",
				Page: 2,
			},
		},
	}
	blocks := Align("相同段落内容\n相同段落内容", units)
	require.Len(t, blocks, 2)
	require.Equal(t, 2, blocks[1].Locator.Page)
}

func TestRemapNeverInterpolatesChangedText(t *testing.T) {
	old := "第一段\n已通过验收\n末段"
	blocks := []types.SourceBlock{{Start: 4, End: 9, Locator: types.SourceLocator{Type: "pdf", Page: 2}}}
	require.Empty(t, RemapBlocks(blocks, old, "第一段\n未通过检查\n末段"))
}

func TestMergeKeepsDistinctExcerptsOfSameRegion(t *testing.T) {
	a := types.SourceLocator{Type: "pdf", Page: 2, BBox: []float64{0, 0, 1, 1}, Quote: "第一句"}
	b := a
	b.Quote = "另一句"
	require.Len(t, MergeLocators(types.SourceLocators{a}, types.SourceLocators{a, b}), 2)
}

func TestLocatorsPreserveLongEvidenceAndAllRegions(t *testing.T) {
	text := strings.Repeat("完整原文", 100) + "末尾引用的关键结论"
	index := NewIndex(text,
		[]types.SourceBlock{{
			Start: 0,
			End:   len([]rune(text)),
			Locator: types.SourceLocator{
				Type: "pdf",
				Page: 1,
			},
		}})
	require.Equal(t, text, index.Locators(0, len([]rune(text)))[0].Quote)
	var blocks []types.SourceBlock
	for i := 0; i < 80; i++ {
		blocks = append(blocks,
			types.SourceBlock{
				Start: i,
				End:   i + 1,
				Locator: types.SourceLocator{
					Type: "pdf",
					Page: i + 1,
				},
			})
	}
	locs := NewIndex(strings.Repeat("文", 80), blocks).Locators(0, 80)
	require.Len(t, locs, 80)
	require.Len(t, MergeLocators(locs[:40], locs[40:]), 80)
}

func TestFoldingCannotMixRevisionsOrMappingQuality(t *testing.T) {
	first := types.SourceLocator{Type: "pdf", Page: 1, Mapping: "exact", SourceHash: "original", Quote: "第一段"}
	second := first
	second.Mapping = ""
	require.Len(t, AppendLocator(types.SourceLocators{first}, second), 2)
	second = first
	second.SourceHash = "replacement"
	require.Len(t, AppendLocator(types.SourceLocators{first}, second), 2)
}

func TestAlignRejectsPartialNumericValues(t *testing.T) {
	for _, pair := range [][2]string{{"增长为5%", "增长为5"}, {"115", "15"}, {"15.5", "15"}, {"-15", "15"}} {
		require.Empty(t,
			Align(pair[0], []Unit{{Text: pair[1], Locator: types.SourceLocator{Type: "pdf", Page: 1}}}),
			pair,
		)
	}
}

func TestChunkWithUnmappedEvidenceIsMarkedPartial(t *testing.T) {
	md := "匹配的原文。\n未匹配的关键结论。"
	index := NewIndex(md, Align(md, []Unit{{Text: "匹配的原文", Locator: types.SourceLocator{Type: "pdf", Page: 1}}}))
	locs := index.Locators(0, len([]rune(md)))
	require.Len(t, locs, 1)
	require.True(t, locs[0].Partial)
	require.False(t, index.Locators(0, 5)[0].Partial)
}

func TestAlignmentDecodesTableEntitiesWithoutLosingOffsets(t *testing.T) {
	md := "<table><tr><td>限值</td><td>&lt;1.5</td></tr></table>"
	loc := types.SourceLocator{Type: "pdf", Page: 2}
	blocks := Align(md, []Unit{{Text: "限值 <1.5", Locator: loc}})
	require.Len(t, blocks, 1)
	quotes := NewIndex(md, blocks).Locators(0, len([]rune(md)))
	require.False(t, quotes[0].Partial)
	require.Equal(t, "限值 <1.5", quotes[0].Quote)
	remapped := RemapBlocks(blocks, md, "|限值|<1.5|")
	require.Len(t, remapped, 1)
	require.Equal(t, 2, remapped[0].Locator.Page)
}

func TestImagePlaceholderDoesNotInvalidateVerifiedTextLocators(t *testing.T) {
	md := "第一段完整的原文。\n\n![descript](resource://image)\n\n第二段完整的原文。"
	blocks := Align(md, []Unit{
		{Text: "第一段完整的原文。", Locator: types.SourceLocator{Type: "docx", Block: 1}},
		{Text: "第二段完整的原文。", Locator: types.SourceLocator{Type: "docx", Block: 3}},
	})
	locs := NewIndex(md, blocks).Locators(0, len([]rune(md)))
	require.Len(t, locs, 2)
	for _, loc := range locs {
		require.False(t, loc.Partial)
	}
}
