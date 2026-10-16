//go:build anydoc && cgo

package anydoc

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var benchmarkDocuments = []string{
	"doc/text.doc", "docx/text.docx", "ppt/pres.ppt", "pptx/pres.pptx",
	"odt/text.odt", "ods/sheet.ods", "odp/pres.odp", "rtf/text.rtf",
	"epub/book.epub", "xls/sheet.xls", "xlsx/sheet.xlsx",
	"xlsb/handmade-sheet.xlsb", "csv/sheet.csv", "pdf/text.pdf",
}

// Synthetic size sweeps cover the Go/Rust document-copy path and the bounded
// hostile-PDF detector. They are separate from the upstream fixture comparison.
func BenchmarkConvertScaling(b *testing.B) {
	for _, rows := range []int{100, 10000} {
		b.Run(fmt.Sprintf("csv_%d_rows", rows), func(b *testing.B) {
			data := []byte("name,quantity,description\n" +
				strings.Repeat("widget,42,Quarterly shipping report\n", rows))
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				result, err := Convert(data, Options{Format: "csv", WithAssets: true})
				if err != nil || result.AssetsError != nil {
					b.Fatalf("conversion: %v", err)
				}
			}
		})
	}
	for _, rows := range []int{100, 10000} {
		b.Run(fmt.Sprintf("xlsx_%d_rows", rows), func(b *testing.B) {
			data := largeWorkbook(b, rows)
			result, err := Convert(data, Options{Format: "xlsx", WithAssets: true})
			if err != nil || !strings.Contains(result.Markdown, fmt.Sprintf("row-%d", rows)) {
				b.Fatalf("preflight: last row was lost, err=%v", err)
			}
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				result, err := Convert(data, Options{Format: "xlsx", WithAssets: true})
				if err != nil || result.AssetsError != nil {
					b.Fatalf("conversion: %v", err)
				}
			}
		})
	}
	for _, operators := range []int{40000, 200000} {
		b.Run(fmt.Sprintf("pdf_%d_operators", operators), func(b *testing.B) {
			data := unmatchedShowTextPDF(operators)
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := Convert(data, Options{Format: "pdf"}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func largeWorkbook(t testing.TB, rows int) []byte {
	t.Helper()
	data := readFixture(t, "xlsx/sheet.xlsx")
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, file := range r.File {
		if file.Name == "xl/worksheets/sheet1.xml" {
			continue
		}
		if err := w.Copy(file); err != nil {
			t.Fatal(err)
		}
	}
	var sheet strings.Builder
	sheet.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for row := 1; row <= rows; row++ {
		fmt.Fprintf(&sheet, `<row r="%d"><c r="A%d" t="inlineStr"><is><t>row-%d</t></is></c>`+
			`<c r="B%d"><v>42</v></c></row>`,
			row, row, row, row)
	}
	sheet.WriteString(`</sheetData></worksheet>`)
	part, err := w.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(sheet.String())); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "upstream", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// WithAssets exercises the default reader path, including Rust parsing,
// the document ABI, Go decoding, image collection, and Markdown serialization.
// Input I/O is excluded. B/op measures Go allocations, not the Rust heap.
func BenchmarkConvert(b *testing.B) {
	for _, name := range benchmarkDocuments {
		b.Run(strings.Split(name, "/")[0], func(b *testing.B) {
			data := readFixture(b, name)
			format := strings.TrimPrefix(filepath.Ext(name), ".")
			if format == "xls" || format == "xlsb" {
				format = "xlsx"
			}
			opts := Options{Format: format, WithAssets: true}
			if result, err := Convert(data, opts); err != nil ||
				result.Markdown == "" ||
				result.AssetsError != nil {
				b.Fatalf("preflight conversion: result=%+v err=%v", result, err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					result, err := Convert(data, opts)
					if err != nil || result.Markdown == "" || result.AssetsError != nil {
						b.Errorf("conversion failed: result=%+v err=%v", result, err)
						return
					}
				}
			})
		})
	}
}
