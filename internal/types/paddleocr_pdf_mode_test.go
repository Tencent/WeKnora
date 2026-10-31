package types

import (
	"encoding/json"
	"testing"
)

func TestPaddleOCRVLPDFModeOverrides(t *testing.T) {
	for _, mode := range []string{"", "auto", "ocr"} {
		var config ParserEngineConfig
		if err := json.Unmarshal([]byte(`{"paddleocr_vl_pdf_mode":"`+mode+`"}`), &config); err != nil {
			t.Fatal(err)
		}
		overrides := config.ToOverridesMap()
		if got := overrides["paddleocr_vl_pdf_mode"]; got != mode {
			t.Fatalf("override=%q, want %q", got, mode)
		}
		if _, ok := overrides["paddleocr_vl_pdf_mode"]; mode == "" && ok {
			t.Fatal("empty mode should preserve OCR default")
		}
	}
}
