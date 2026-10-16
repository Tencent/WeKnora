package docparser

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/docreader/proto"
	"github.com/Tencent/WeKnora/internal/types"
)

const minerUContentListJSON = `[
 {"type":"text","text":"8.14.11 索夹与吊索施工应符合下列规定：","text_level":2,"page_idx":56,"bbox":[80,100,900,120]},
 {"type":"text","text":"1 在满足施工需要的前提下，应减小猫道面层开孔面积。","page_idx":56,"bbox":[80,130,920,170]},
 {"type":"table","table_body":"<table><tr><td>高度</td><td>12m</td></tr></table>",
  "table_caption":["表 1"],"page_idx":57,"bbox":[50,10,950,2000]}
]`

func TestMinerUSourceBlocks(t *testing.T) {
	md := "## 8.14.11 索夹与吊索施工应符合下列规定：\n\n1 在满足施工需要的前提下，应减小猫道面层开孔面积。\n\n表 1\n\n| 高度 | 12m |\n| --- | --- |\n"
	for name, raw := range map[string][]byte{
		"array":  []byte(minerUContentListJSON),
		"string": mustJSON(t, minerUContentListJSON),
	} {
		t.Run(name, func(t *testing.T) {
			blocks := minerUSourceBlocks(md, raw, "pdf")
			require.Len(t, blocks, 3)
			assert.Equal(t, 57, blocks[0].Locator.Page)
			assert.InDeltaSlice(t, []float64{0.08, 0.1, 0.9, 0.12}, blocks[0].Locator.BBox, 1e-9)
			assert.Equal(t, 58, blocks[2].Locator.Page)
			assert.Nil(t, blocks[2].Locator.BBox, "out-of-range boxes are dropped")
		})
	}
	assert.Nil(t, minerUSourceBlocks(md, []byte(minerUContentListJSON), "docx"))
}

func TestMinerUContentListFromZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"doc/doc.md":                     "x",
		"doc/doc_content_list.json":      "[1]",
		"doc/auto/doc_content_list.json": "[2]",
	} {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, _ = w.Write([]byte(body))
	}
	require.NoError(t, zw.Close())
	assert.Equal(t, "[1]", string(minerUContentListFromZip(buf.Bytes())))
}

func TestPaddleOCRVLPageUnits(t *testing.T) {
	var page paddleOCRVLPage
	require.NoError(t, json.Unmarshal([]byte(`{
		"markdown":{"text":"第一段\n\n第二段"},
		"prunedResult":{"width":1000,"height":2000,"parsing_res_list":[
			{"block_label":"text","block_content":"第一段","block_bbox":[100,200,900,400]}]}
	}`), &page))
	units := paddleOCRVLPageUnits(3, page)
	require.Len(t, units, 1)
	assert.Equal(t, []float64{0.1, 0.1, 0.9, 0.2}, units[0].Locator.BBox)
	assert.Equal(t, 3, units[0].Locator.Page)

	page.PrunedResult = nil
	units = paddleOCRVLPageUnits(4, page)
	require.Len(t, units, 2)
	assert.Nil(t, units[1].Locator.BBox)
	assert.True(t, strings.Contains(units[1].Text, "第二段"))
}

func TestSourceBlocksFromProto(t *testing.T) {
	blocks := sourceBlocksFromProto([]*proto.SourceBlock{
		{Start: 0, End: 5, LocatorJson: `{"type":"pdf","page":2,"bbox":[0.1,0.2,0.3,0.4]}`},
		{Start: 5, End: 5, LocatorJson: `{"type":"pdf","page":2}`},
		{Start: 5, End: 9, LocatorJson: `not json`},
	})
	require.Len(t, blocks, 1)
	assert.Equal(t, types.SourceLocator{Type: "pdf", Page: 2, BBox: []float64{0.1, 0.2, 0.3, 0.4}}, blocks[0].Locator)
}

func mustJSON(t *testing.T, s string) []byte {
	t.Helper()
	b, err := json.Marshal(s)
	require.NoError(t, err)
	return b
}
