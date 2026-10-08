package docparser

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// Contract fixture for DocVortex MiddleJson 2.0. Bboxes are normalized
// fractions, page indices are original source indices, and captions are
// child blocks. This is deliberately not a legacy content_list payload.
const middleFixture = `{
  "schema": "docvortex.middle",
  "schema_version": "2.0",
  "is_full_document": true,
  "metadata": {
    "file_suffix": "pdf",
    "producer": {
      "name": "mineru",
      "version": "4.0.0"
    }
  },
  "pages": [
    {
      "page_idx": 4,
      "blocks": [
        {
          "type": "text",
          "index": 0,
          "bbox": [
            0.1,
            0.2,
            0.9,
            0.3
          ],
          "content": [
            {
              "type": "text",
              "content": "设备"
            },
            {
              "type": "hyperlink",
              "url": "https://example.org",
              "content": [
                {
                  "type": "text",
                  "content": "验收合格"
                }
              ]
            }
          ]
        },
        {
          "type": "table",
          "index": 1,
          "bbox": [
            0.1,
            0.4,
            0.9,
            0.8
          ],
          "content": [
            {
              "type": "table_caption",
              "bbox": [
                0.1,
                0.4,
                0.9,
                0.45
              ],
              "content": [
                {
                  "type": "text",
                  "content": "设备参数表"
                }
              ]
            },
            {
              "type": "table_body",
              "bbox": [
                0.1,
                0.46,
                0.9,
                0.8
              ],
              "content": "<table><tr><td>限值</td><td>1.5毫米</td></tr></table>"
            }
          ]
        }
      ]
    }
  ]
}`

func TestMinerUMiddleNormalizedGeometryAndNestedBlocks(t *testing.T) {
	blocks := minerUSourceBlocks("设备验收合格\n\n设备参数表\n\n|限值|1.5毫米|", []byte(middleFixture), "pdf")
	require.Len(t, blocks, 3)
	require.Equal(t, 5, blocks[0].Locator.Page)
	require.Equal(t, []float64{0.1, 0.2, 0.9, 0.3}, blocks[0].Locator.BBox)
	require.Equal(t, "mineru:2:4:0", blocks[0].Locator.SourceID)
	require.Equal(t, "exact", blocks[0].Locator.Mapping)
	require.Equal(t, []float64{0.1, 0.46, 0.9, 0.8}, blocks[2].Locator.BBox)
}

func TestMinerUMiddleZIPPreferredToLegacy(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"markdown.md":           "设备验收合格",
		"middle_json.json":      middleFixture,
		"doc_content_list.json": "[]",
	} {
		w, err := z.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, z.Close())
	require.JSONEq(t, middleFixture, string(minerUContentListFromZip(buf.Bytes())))
}

func TestMinerUMiddleRejectsUnknownSchemaAndInvalidIndices(t *testing.T) {
	for _, mutate := range []func(map[string]any){
		func(d map[string]any) { d["schema"] = "docvortex.model" },
		func(d map[string]any) { d["schema_version"] = "9.0" },
		func(d map[string]any) { delete(d["pages"].([]any)[0].(map[string]any), "page_idx") },
		func(d map[string]any) { d["pages"].([]any)[0].(map[string]any)["page_idx"] = -1 },
	} {
		var d map[string]any
		require.NoError(t, json.Unmarshal([]byte(middleFixture), &d))
		mutate(d)
		raw, err := json.Marshal(d)
		require.NoError(t, err)
		require.Empty(t, minerUSourceBlocks("设备验收合格", raw, "pdf"))
	}
}

func TestMinerULegacyMissingPageIsNotPageOne(t *testing.T) {
	require.Empty(t, minerUSourceBlocks("设备验收合格", []byte(`[{"type":"text","text":"设备验收合格"}]`), "pdf"))
}

func TestMinerUBoxesRejectNonFiniteAndWrongUnits(t *testing.T) {
	for _, box := range [][]float64{{0, 0, math.NaN(), 1}, {0, 0, math.Inf(1), 1}, {0, 0, 900, 600}, {.9, 0, .1, 1}} {
		require.False(t, normalizedMinerUBox(box))
	}
	_, ok := minerUBBox([]float64{0, 0, math.NaN(), 100})
	require.False(t, ok)
}

func TestMinerULegacyZIPWithUnsupportedMiddleKeepsContentList(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	legacy := `[{"type":"text","text":"设备验收合格","page_idx":1,"bbox":[100,200,900,300]}]`
	for name, body := range map[string]string{
		"doc_middle_json.json":  `{"pdf_info":[]}`,
		"doc_content_list.json": legacy,
	} {
		w, err := z.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, z.Close())
	blocks := minerUSourceBlocks("设备验收合格", minerUContentListFromZip(buf.Bytes()), "pdf")
	require.Len(t, blocks, 1)
	require.Equal(t, 2, blocks[0].Locator.Page)
	require.Equal(t, []float64{.1, .2, .9, .3}, blocks[0].Locator.BBox)
}
