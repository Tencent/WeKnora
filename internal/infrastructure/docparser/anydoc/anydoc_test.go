package anydoc

import (
	"errors"
	"slices"
	"testing"
)

func TestFormatForFileResolvesTypeAndName(t *testing.T) {
	cases := []struct {
		name       string
		fileType   string
		fileName   string
		wantFormat string
		wantOK     bool
	}{
		{name: "file type", fileType: "docx", wantFormat: "docx", wantOK: true},
		{name: "dotted file type", fileType: ".DOCX", wantFormat: "docx", wantOK: true},
		{name: "macro format maps to base format", fileType: "pptm", wantFormat: "pptx", wantOK: true},
		{name: "legacy excel maps to xlsx parser", fileType: "xls", wantFormat: "xlsx", wantOK: true},
		{name: "falls back to file name", fileName: "report.odt", wantFormat: "odt", wantOK: true},
		{name: "unsupported type", fileType: "png", wantOK: false},
		{name: "nothing to go on", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			format, ok := FormatForFile(tc.fileType, tc.fileName)
			if ok != tc.wantOK {
				t.Fatalf("FormatForFile(%q, %q) ok = %v, want %v", tc.fileType, tc.fileName, ok, tc.wantOK)
			}
			if format != tc.wantFormat {
				t.Fatalf("FormatForFile(%q, %q) = %q, want %q", tc.fileType, tc.fileName, format, tc.wantFormat)
			}
		})
	}
}

// The engine list must look the same whether or not the converter is linked
// in, so that the UI can show the file types with a "not built in" reason.
func TestSupportedFileTypesIsStableAndSorted(t *testing.T) {
	types := SupportedFileTypes()
	if !slices.IsSorted(types) {
		t.Fatalf("SupportedFileTypes is not sorted: %v", types)
	}
	for _, want := range []string{"docx", "pptx", "xlsx", "epub", "pdf", "csv"} {
		if !slices.Contains(types, want) {
			t.Errorf("SupportedFileTypes does not include %q: %v", want, types)
		}
	}
}

func TestPDFNeedsOCR(t *testing.T) {
	if PDFNeedsOCR(nil) {
		t.Fatal("nil error was treated as needing OCR")
	}
	if !PDFNeedsOCR(errors.New("PDF has no extractable text (Scanned, 5 pages): OCR is required")) {
		t.Error("scanned-PDF error was not detected")
	}
	if PDFNeedsOCR(errors.New("not a readable zip archive")) {
		t.Error("unrelated error was treated as needing OCR")
	}
}

func TestConvertRejectsEmptyInput(t *testing.T) {
	if _, err := Convert(nil, Options{Format: "docx"}); err == nil {
		t.Fatal("Convert(nil) succeeded, want an error")
	}
}

// Without the build tag there is no converter, and callers must be able to
// detect that so they can fall back to another engine.
func TestConvertReportsUnavailableBackend(t *testing.T) {
	if Available() {
		t.Skip("converter is linked into this build")
	}
	_, err := Convert([]byte("id,name\n1,a\n"), Options{Format: "csv"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Convert error = %v, want ErrUnavailable", err)
	}
	if UnavailableReason() == "" {
		t.Error("UnavailableReason is empty while the converter is unavailable")
	}
}
