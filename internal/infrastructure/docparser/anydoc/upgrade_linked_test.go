//go:build anydoc && cgo

package anydoc

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	upstream "github.com/firecrawl/anydoc/go"
)

func TestConvertEquations(t *testing.T) {
	for _, format := range []string{"docx", "pptx", "odt", "epub", "rtf"} {
		t.Run(format, func(t *testing.T) {
			data := readFixture(t, format+"/handmade-math."+format)
			for _, assets := range []bool{false, true} {
				result, err := Convert(data, Options{Format: format, WithAssets: assets})
				if err != nil {
					t.Fatal(err)
				}
				if result.AssetsError != nil {
					t.Fatalf("document ABI failed: %v", result.AssetsError)
				}
				if !strings.Contains(result.Markdown, `\frac`) ||
					!strings.Contains(result.Markdown, "$") {
					t.Fatalf("equation lost (assets=%v): %s", assets, result.Markdown)
				}
			}
		})
	}
	// Check the actual decoded fields, not only the independent Markdown path.
	doc, err := upstream.ToDocument(readFixture(t, "docx/handmade-math.docx"), nil)
	if err != nil {
		t.Fatal(err)
	}
	var inline, block bool
	for _, b := range doc.Blocks {
		block = block || b.Kind == "math" && b.Text != nil && strings.Contains(*b.Text, `\sum`)
		for _, in := range b.Content {
			inline = inline || in.Kind == "math" && in.Text != nil && strings.Contains(*in.Text, `\frac`)
		}
	}
	if !inline || !block {
		t.Fatalf("decoded equation nodes missing: inline=%v block=%v", inline, block)
	}
}

func TestConvertPDFReportsScannedPages(t *testing.T) {
	for _, tc := range []struct{ file, detail string }{
		{"handmade-mixed.pdf", "page 2 of 2 needs OCR"},
		{"handmade-scanned.pdf", "all 2 pages need OCR"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			result, err := Convert(readFixture(t, "pdf/"+tc.file), Options{Format: "pdf", WithAssets: true})
			var typed *upstream.ConvertError
			if result != nil ||
				!errors.As(err, &typed) ||
				typed.Kind != "needs_ocr" ||
				!strings.Contains(typed.Detail, tc.detail) {
				t.Fatalf("want scanned-page error, got result=%+v err=%v", result, err)
			}
			if !PDFNeedsOCR(fmt.Errorf("reader: %w", err)) {
				t.Fatal("wrapped typed error did not trigger OCR fallback")
			}
		})
	}
	if !PDFNeedsOCR(&upstream.ConvertError{Kind: "needs_ocr", Detail: "localized detail"}) {
		t.Fatal("OCR detection still depends on English wording")
	}
	if PDFNeedsOCR(&upstream.ConvertError{Kind: "malformed", Detail: "invalid PDF"}) {
		t.Fatal("malformed PDF incorrectly triggers OCR")
	}
}

func TestConvertSpreadsheetCheckboxes(t *testing.T) {
	// Add VML form controls to an upstream workbook. Both boolean states must
	// survive Rust encoding and Go decoding, including subsequent table cells.
	data := readFixture(t, "xlsx/sheet.xlsx")
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, f := range reader.File {
		if f.Name == "xl/worksheets/_rels/sheet1.xml.rels" {
			continue
		}
		if err := w.Copy(f); err != nil {
			t.Fatal(err)
		}
	}
	parts := map[string]string{
		"xl/worksheets/_rels/sheet1.xml.rels": `<Relationships ` +
			`xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><` +
			`Relationship Id="checkboxes" ` +
			`Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/vmlDrawing"` +
			` Target="../drawings/checkboxes.vml"/></Relationships>`,
		"xl/drawings/checkboxes.vml": `<xml xmlns:v="urn:schemas-microsoft-com:vml" ` +
			`xmlns:x="urn:schemas-microsoft-com:office:excel"><v:shape><v:textbox>Approved</v:text` +
			`box><x:ClientData ObjectType="Checkbox"><x:Anchor>0,0,0,0,1,0,1,0</x:Anchor><x:Checke` +
			`d>1</x:Checked></x:ClientData></v:shape><v:shape><v:textbox>Pending</v:textbox><x:Cli` +
			`entData ObjectType="Checkbox"><x:Anchor>1,0,0,0,2,0,1,0</x:Anchor></x:ClientData></v:` +
			`shape></xml>`,
	}
	for name, content := range parts {
		part, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := Convert(out.Bytes(), Options{Format: "xlsx", WithAssets: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.AssetsError != nil ||
		!strings.Contains(result.Markdown, "[x] Approved") ||
		!strings.Contains(result.Markdown, "[ ] Pending") {
		t.Fatalf("checkbox conversion: %+v", result)
	}
	doc, err := upstream.ToDocument(out.Bytes(), nil)
	if err != nil {
		t.Fatal(err)
	}
	states := map[bool]bool{}
	for _, block := range doc.Blocks {
		if block.Table == nil {
			continue
		}
		for _, row := range block.Table.Grid {
			for _, slot := range row {
				if slot.Cell == nil {
					continue
				}
				for _, b := range slot.Cell.Blocks {
					for _, in := range b.Content {
						if in.Kind == "checkbox" && in.Checked != nil {
							states[*in.Checked] = true
						}
					}
				}
			}
		}
	}
	if !states[false] || !states[true] {
		t.Fatalf("decoded checkbox states: %v", states)
	}
}

func TestConvertAllBenchmarkFormats(t *testing.T) {
	for _, name := range benchmarkDocuments {
		t.Run(name, func(t *testing.T) {
			format := strings.Split(name, "/")[0]
			if format == "xls" || format == "xlsb" {
				format = "xlsx"
			}
			result, err := Convert(readFixture(t, name), Options{Format: format, WithAssets: true})
			if err != nil || result.Markdown == "" || result.AssetsError != nil {
				t.Fatalf("conversion: result=%+v err=%v", result, err)
			}
		})
	}
}

func TestCombinedConversionPreservesModelAndMarkdown(t *testing.T) {
	// Exercise auto-detection as well as all normal formats, without requiring
	// an external upstream checkout for this regression in CI.
	for _, name := range benchmarkDocuments {
		if strings.HasPrefix(name, "pdf/") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			data := readFixture(t, name)
			var format *upstream.Format
			if strings.HasPrefix(name, "csv/") {
				f := upstream.FormatCsv
				format = &f
			}
			want, err := upstream.ToDocument(data, format)
			if err != nil {
				t.Fatal(err)
			}
			wantMarkdown, err := upstream.ToMarkdownWithAssetLinks(data, format)
			if err != nil {
				t.Fatal(err)
			}
			got, markdown, err := upstream.ToDocumentWithAssetLinks(data, format)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) || markdown != wantMarkdown {
				t.Fatal("single parse changed model or Markdown")
			}
		})
	}
	for _, tc := range []struct {
		name   string
		data   []byte
		format *upstream.Format
		kind   string
	}{
		{"empty", nil, nil, "unsupported"},
		{"garbage", []byte("garbage"), nil, "unsupported"},
		{"pdf", minimalPDF(), nil, "pdf_no_model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, md, err := upstream.ToDocumentWithAssetLinks(tc.data, tc.format)
			var typed *upstream.ConvertError
			if doc != nil || md != "" || !errors.As(err, &typed) || typed.Kind != tc.kind {
				t.Fatalf("error contract: doc=%+v md=%q err=%v", doc, md, err)
			}
		})
	}
}
