//go:build anydoc && cgo

package anydoc

import (
	"fmt"
	"testing"

	upstream "github.com/firecrawl/anydoc/go"
)

func TestPDFInvalidStructureTypedErrors(t *testing.T) {
	var absent *upstream.ConvertError
	if PDFInvalidStructure(absent) || PDFInvalidStructure(fmt.Errorf("convert: %w", absent)) {
		t.Fatal("typed nil must not trigger a fallback")
	}
	for _, kind := range []string{"malformed", "encrypted", "resource_limit", "io", "needs_ocr", "unsupported"} {
		t.Run(kind, func(t *testing.T) {
			// A typed rejection must take precedence over legacy error text.
			err := fmt.Errorf("convert: %w", &upstream.ConvertError{Kind: kind, Detail: "invalid PDF structure"})
			if got, want := PDFInvalidStructure(err), kind == "malformed"; got != want {
				t.Fatalf("PDFInvalidStructure(%v) = %v, want %v", err, got, want)
			}
		})
	}
}
