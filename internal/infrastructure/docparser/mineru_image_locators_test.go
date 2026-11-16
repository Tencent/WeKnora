package docparser

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// imageMiddleFixture is the layout MinerU writes for a drawing sheet: page 21
// carries one image block whose only text is its caption, page 23 carries a
// chart block and an image block whose body has no box of its own, and page 24
// carries two image blocks so a single page resolves to several image files.
const imageMiddleFixture = `{
  "schema": "docvortex.middle",
  "schema_version": "2.0",
  "pages": [
    {
      "page_idx": 21,
      "blocks": [
        {
          "type": "image",
          "index": 8,
          "bbox": [0.158, 0.545, 0.397, 0.784],
          "content": [
            {
              "type": "image_body",
              "index": 8,
              "bbox": [0.158, 0.545, 0.397, 0.784],
              "image_path": "images/page_21_image_8.jpg",
              "content": ""
            },
            {
              "type": "image_caption",
              "index": 9,
              "bbox": [0.224, 0.792, 0.33, 0.809],
              "content": [{"type": "text", "content": "S3SK-16 剖面涌水"}]
            }
          ]
        },
        {
          "type": "text",
          "index": 10,
          "bbox": [0.1, 0.1, 0.9, 0.2],
          "content": [{"type": "text", "content": "图签"}]
        }
      ]
    },
    {
      "page_idx": 23,
      "blocks": [
        {
          "type": "chart",
          "index": 11,
          "bbox": [0.576, 0.241, 0.893, 0.493],
          "content": [
            {
              "type": "chart_body",
              "index": 11,
              "bbox": [0.576, 0.241, 0.893, 0.493],
              "image_path": "images/page_23_chart_11.jpg",
              "content": ""
            }
          ]
        },
        {
          "type": "image",
          "index": 14,
          "bbox": [0.05, 0.55, 0.95, 0.9],
          "content": [
            {
              "type": "image_body",
              "index": 14,
              "image_path": "images/page_23_image_14.jpg",
              "content": ""
            }
          ]
        }
      ]
    },
    {
      "page_idx": 24,
      "blocks": [
        {
          "type": "image",
          "index": 2,
          "bbox": [0.02, 0.03, 0.48, 0.47],
          "content": [
            {
              "type": "image_body",
              "index": 2,
              "bbox": [0.02, 0.03, 0.48, 0.47],
              "image_path": "images/page_24_image_2.jpg",
              "content": ""
            }
          ]
        },
        {
          "type": "image",
          "index": 5,
          "bbox": [0.52, 0.53, 0.98, 0.97],
          "content": [
            {
              "type": "image_body",
              "index": 5,
              "bbox": [0.52, 0.53, 0.98, 0.97],
              "image_path": "images/page_24_image_5.jpg",
              "content": ""
            }
          ]
        }
      ]
    }
  ]
}`

// An image block has no text of its own, so the page of the figure has to reach
// ingestion from the image path the layout recorded next to it.
func TestMinerUImageLocatorsMapImageFilesToTheirPage(t *testing.T) {
	locators := minerUImageLocators([]byte(imageMiddleFixture), "pdf")
	require.Len(t, locators, 5)

	image := locators["images/page_21_image_8.jpg"]
	require.Equal(t, types.SourceLocatorPDF, image.Type)
	require.Equal(t, 22, image.Page)
	require.Equal(t, "mineru:2:21:8/0", image.SourceID)
	// The page and the region are the parser's own, which is what a viewer
	// needs before it will open the original at that page.
	require.Equal(t, "exact", image.Mapping)
	require.False(t, image.Partial)
	// The image's own region, not the block's caption-inclusive box.
	require.Equal(t, []float64{0.158, 0.545, 0.397, 0.784}, image.BBox)

	chart := locators["images/page_23_chart_11.jpg"]
	require.Equal(t, 24, chart.Page)
	require.Equal(t, "mineru:2:23:11/0", chart.SourceID)
	require.Equal(t, "exact", chart.Mapping)

	// A child without a box of its own falls back to the block's.
	bodyless := locators["images/page_23_image_14.jpg"]
	require.Equal(t, 24, bodyless.Page)
	require.Equal(t, []float64{0.05, 0.55, 0.95, 0.9}, bodyless.BBox)
}

// A page can carry several image blocks: each keeps its own file, its own page
// and its own block reference instead of sharing one.
func TestMinerUImageLocatorsSplitSeveralBlocksOnOnePage(t *testing.T) {
	locators := minerUImageLocators([]byte(imageMiddleFixture), "pdf")

	first := locators["images/page_24_image_2.jpg"]
	require.Equal(t, 25, first.Page)
	require.Equal(t, "mineru:2:24:2/0", first.SourceID)
	require.Equal(t, []float64{0.02, 0.03, 0.48, 0.47}, first.BBox)

	second := locators["images/page_24_image_5.jpg"]
	require.Equal(t, 25, second.Page)
	require.Equal(t, "mineru:2:24:5/0", second.SourceID)
	require.Equal(t, []float64{0.52, 0.53, 0.98, 0.97}, second.BBox)
}

// An image with no box at all keeps its page and drops the region rather than
// claiming a place the layout never reported.
func TestMinerUImageLocatorsOmitMissingBBox(t *testing.T) {
	payload := `{"schema":"docvortex.middle","schema_version":"2.0","pages":[
		{"page_idx":6,"blocks":[{"type":"image","index":0,"content":[
			{"type":"image_body","image_path":"images/page_6_image_0.jpg"}]}]}]}`
	locators := minerUImageLocators([]byte(payload), "pdf")
	require.Len(t, locators, 1)
	loc := locators["images/page_6_image_0.jpg"]
	require.Equal(t, 7, loc.Page)
	require.Equal(t, "mineru:2:6:0/0", loc.SourceID)
	require.Empty(t, loc.BBox)
}

// A caption child names no image file; only image_body and chart_body do.
func TestMinerUImageLocatorsIgnoreNonImageChildren(t *testing.T) {
	locators := minerUImageLocators([]byte(imageMiddleFixture), "pdf")
	require.NotContains(t, locators, "images/page_21_image_9.jpg")
	require.Len(t, locators, 5, "one entry per rendered image file")
}

// Office files have no pages a viewer can show: MinerU's pages belong to the
// intermediate PDF it made from the upload, so they stay out of the map the
// same way their source blocks do.
func TestMinerUImageLocatorsSkipOfficeFiles(t *testing.T) {
	for _, fileType := range []string{"doc", "docx", ".DOCX", "ppt", "pptx", "xlsx"} {
		require.Nil(t, minerUImageLocators([]byte(imageMiddleFixture), fileType), fileType)
	}
	// An image upload is a page a viewer can show.
	require.NotNil(t, minerUImageLocators([]byte(imageMiddleFixture), "png"))
}

// Payloads that carry no image path leave ingestion on its old path.
func TestMinerUImageLocatorsSkipLayoutlessPayloads(t *testing.T) {
	require.Nil(t, minerUImageLocators(nil, "pdf"))
	require.Nil(t, minerUImageLocators([]byte(`[{"type":"image","img_path":"images/a.jpg","page_idx":0}]`), "pdf"))
	require.Nil(t, minerUImageLocators([]byte(`{"schema":"docvortex.model","schema_version":"2.0","pages":[]}`), "pdf"))
	require.Nil(t, minerUImageLocators([]byte(`{"schema":"docvortex.middle","schema_version":"9.0","pages":[]}`), "pdf"))
	require.Nil(t, minerUImageLocators([]byte(`{"schema":"docvortex.middle","schema_version":"2.0","pages":[
		{"blocks":[{"type":"image","index":0,"content":[{"type":"image_body","image_path":"images/a.jpg"}]}]}]}`), "pdf"))
	require.Nil(t, minerUImageLocators([]byte(`{"schema":"docvortex.middle","schema_version":"2.0","pages":[]}`), "pdf"))
}

// Two blocks naming the same file keep the first, so a repeated reference
// cannot move an image already placed.
func TestMinerUImageLocatorsKeepFirstOccurrence(t *testing.T) {
	payload := `{"schema":"docvortex.middle","schema_version":"2.0","pages":[
		{"page_idx":0,"blocks":[{"type":"image","index":0,"bbox":[0,0,1,1],
			"content":[{"type":"image_body","image_path":"images/a.jpg"}]}]},
		{"page_idx":4,"blocks":[{"type":"image","index":0,"bbox":[0,0,1,1],
			"content":[{"type":"image_body","image_path":"images/a.jpg"}]}]}]}`
	locators := minerUImageLocators([]byte(payload), "pdf")
	require.Len(t, locators, 1)
	require.Equal(t, 1, locators["images/a.jpg"].Page)
}
