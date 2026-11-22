package client

import (
	"encoding/json"
	"testing"
)

func TestImportScannedDocumentOverrides(t *testing.T) {
	off := false
	req := CreateKnowledgeFromURLRequest{
		URL: "https://example.org/scanned.pdf",
		ProcessConfig: &KnowledgeProcessOverrides{
			ParserEngineOverrides: map[string]string{"pdf_force_scanned": "true"},
			SummaryEnabled:        &off,
		},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Process struct {
			Params  map[string]string `json:"parser_engine_overrides"`
			Summary *bool             `json:"summary_enabled"`
		} `json:"process_config"`
	}
	if err = json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Process.Params["pdf_force_scanned"] != "true" || wire.Process.Summary == nil || *wire.Process.Summary {
		t.Fatalf("lost document overrides: %s", b)
	}
}
