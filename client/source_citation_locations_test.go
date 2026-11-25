package client

import (
	"encoding/json"
	"testing"
)

func TestCitationSourceLocations(t *testing.T) {
	var hit SearchResult
	if err := json.Unmarshal([]byte(`{
  "source_locators": [
    {
      "type": "pdf",
      "page": 3,
      "bbox": [
        0.1,
        0.2,
        0.4,
        0.5
      ],
      "partial": true
    }
  ]
}`), &hit); err != nil {
		t.Fatal(err)
	}
	if len(hit.SourceLocators) != 1 || hit.SourceLocators[0].Page != 3 ||
		len(hit.SourceLocators[0].BBox) != 4 || !hit.SourceLocators[0].Partial {
		t.Fatalf("lost PDF location: %+v", hit)
	}
	var chunk Chunk
	if err := json.Unmarshal([]byte(`{
  "source_locators": [
    {
      "type": "sheet",
      "sheet": "Sales",
      "row_start": 5,
      "row_end": 8
    }
  ]
}`), &chunk); err != nil {
		t.Fatal(err)
	}
	if len(chunk.SourceLocators) != 1 || chunk.SourceLocators[0].Sheet != "Sales" ||
		chunk.SourceLocators[0].RowEnd != 8 {
		t.Fatalf("lost spreadsheet location: %+v", chunk)
	}
}
