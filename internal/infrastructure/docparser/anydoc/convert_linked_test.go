//go:build anydoc && cgo

package anydoc

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These tests exercise the linked Rust converter, so they only build with the
// `anydoc` tag. Run them with:
//
//	scripts/build-anydoc-lib.sh && go test -tags anydoc ./internal/infrastructure/docparser/...

// SupportedFileTypes is a static list, so that the engine can advertise its
// file types in builds that link no converter. This is the check that it still
// agrees with what the converter actually accepts, in either direction.
func TestSupportedFileTypesMatchTheConverter(t *testing.T) {
	for _, fileType := range SupportedFileTypes() {
		format, ok := FormatForFile(fileType, "")
		if !ok {
			t.Errorf("%q is advertised but maps to no format", fileType)
			continue
		}
		if _, err := upstreamFormat(format); err != nil {
			t.Errorf("%q maps to format %q, which the converter rejects: %v", fileType, format, err)
		}
	}
}

func TestConvertCSV(t *testing.T) {
	result, err := Convert([]byte("quarter,widgets\nQ1,12\nQ2,15\n"), Options{Format: "csv"})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !strings.Contains(result.Markdown, "| quarter | widgets |") {
		t.Fatalf("expected a markdown table header, got:\n%s", result.Markdown)
	}
	if !strings.Contains(result.Markdown, "| Q1 | 12 |") {
		t.Fatalf("expected the first data row, got:\n%s", result.Markdown)
	}
}

func TestConvertDocxWithEmbeddedImage(t *testing.T) {
	document := buildDocx(t)

	result, err := Convert(document, Options{Format: "docx", WithAssets: true})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !strings.Contains(result.Markdown, "# Quarterly report") {
		t.Fatalf("expected the heading, got:\n%s", result.Markdown)
	}
	if !strings.Contains(result.Markdown, "Widgets shipped on time.") {
		t.Fatalf("expected the body paragraph, got:\n%s", result.Markdown)
	}
	if !strings.Contains(result.Markdown, "Closing remarks.") {
		t.Fatalf("expected the trailing paragraph, got:\n%s", result.Markdown)
	}
	if len(result.Assets) != 1 {
		t.Fatalf("got %d assets, want 1", len(result.Assets))
	}
	asset := result.Assets[0]
	if asset.Name != "image-1.png" {
		t.Errorf("asset name = %q, want image-1.png", asset.Name)
	}
	if !bytes.Equal(asset.Data, onePixelPNG()) {
		t.Errorf("asset data does not round-trip the embedded image")
	}
	if asset.Alt != "Shipping chart" {
		t.Errorf("asset alt = %q, want %q", asset.Alt, "Shipping chart")
	}
	if asset.Section != "Quarterly report" {
		t.Errorf("asset section = %q, want %q", asset.Section, "Quarterly report")
	}
	imageLink := "![Shipping chart](images/image-1.png)"
	if !strings.Contains(result.Markdown, imageLink) {
		t.Fatalf("expected the image in place, got:\n%s", result.Markdown)
	}
	before := strings.Index(result.Markdown, "Widgets shipped on time.")
	at := strings.Index(result.Markdown, imageLink)
	after := strings.Index(result.Markdown, "Closing remarks.")
	if before < 0 || at < 0 || after < 0 || !(before < at && at < after) {
		t.Fatalf("image is not between the surrounding paragraphs:\n%s", result.Markdown)
	}
}

func TestConvertTextlessPDFNeedsOCR(t *testing.T) {
	_, err := Convert(textlessPDF(), Options{Format: "pdf"})
	if err == nil {
		t.Fatal("Convert succeeded on a PDF with no text, want an error")
	}
	if !PDFNeedsOCR(err) {
		t.Fatalf("Convert error = %v, want an OCR-required error", err)
	}
}

// The ABI reports error messages through a thread-local slot that a second
// call reads, so a goroutine resumed on another OS thread would report an
// empty or someone else's message. The binding pins the goroutine for the
// pair; this is the regression test for that.
func TestErrorDetailSurvivesConcurrency(t *testing.T) {
	const conversions = 4000

	var wg sync.WaitGroup
	var empty, crossed atomic.Int64
	for i := 0; i < conversions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// docx and xlsx failures carry distinctive messages, so a detail
			// naming the other format proves it came from the wrong thread.
			format, want, other := "docx", "not a readable zip archive", "unreadable workbook"
			if i%2 == 1 {
				format, want, other = "xlsx", "unreadable workbook", "not a readable zip archive"
			}
			_, err := Convert([]byte(fmt.Sprintf("garbage %d", i)), Options{Format: format})
			if err == nil {
				t.Errorf("Convert(garbage) succeeded")
				return
			}
			detail := err.Error()
			switch {
			case strings.Contains(detail, other):
				crossed.Add(1)
			case !strings.Contains(detail, want):
				empty.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if empty.Load() > 0 || crossed.Load() > 0 {
		t.Fatalf("error details lost or crossed between goroutines: lost=%d crossed=%d (of %d)",
			empty.Load(), crossed.Load(), conversions)
	}
}

// Detection reads the container itself, so a document whose format is not
// named still converts.
func TestConvertDetectsFormatFromContent(t *testing.T) {
	result, err := Convert(buildDocx(t), Options{})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !strings.Contains(result.Markdown, "# Quarterly report") {
		t.Fatalf("expected the heading, got:\n%s", result.Markdown)
	}
}

func TestConvertRejectsGarbage(t *testing.T) {
	if _, err := Convert([]byte("not a document at all"), Options{Format: "docx"}); err == nil {
		t.Fatal("Convert succeeded on garbage input, want an error")
	}
}

// PDF has no document model, so WithAssets has to be dropped rather than turn
// a working conversion into an error.
func TestConvertPDFIgnoresAssetRequest(t *testing.T) {
	result, err := Convert(minimalPDF(), Options{Format: "pdf", WithAssets: true})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !strings.Contains(result.Markdown, "Shipping summary") {
		t.Fatalf("expected the page text, got:\n%s", result.Markdown)
	}
	if len(result.Assets) != 0 {
		t.Errorf("got %d assets for a PDF, want 0", len(result.Assets))
	}
}

// A PDF whose catalog holds a deeply nested array is the shape of
// RUSTSEC-2026-0187: lopdf 0.41 recursed per nesting level and killed the
// process with an uncatchable stack overflow, which no Go-side or Rust-side
// recover can contain. The pinned dependency in third_party/anydoc-go bounds
// the recursion, so this must come back as an ordinary error — and if a
// dependency bump ever reintroduces it, this test crashes rather than passing
// quietly.
func TestDeeplyNestedPDFFailsWithoutKillingTheProcess(t *testing.T) {
	const depth = 50000
	nested := strings.Repeat("[", depth) + strings.Repeat("]", depth)

	_, err := Convert(nestedPDF(nested), Options{Format: "pdf"})
	if err == nil {
		t.Fatal("Convert succeeded on a PDF with no text, want an error")
	}
}

// A show-text operator with no operand made the page-classification scan walk
// back over the whole content stream looking for one, so a stream of bare
// `] TJ` tokens cost time quadratic in its length: before pdf-inspector 1.14.2
// this input took 27 seconds of CPU, and a 2 MB one took nearly two minutes.
// Unbounded work is the half of the hostile-PDF problem that guarded() cannot
// catch — it never panics, it just holds the core — so the bound is pinned
// here. The budget is deliberately far above the ~6ms a bounded lookback needs:
// what it has to distinguish is linear from quadratic, not fast from slow.
func TestDetectorLookbackStaysLinear(t *testing.T) {
	const (
		operators = 200000
		budget    = 15 * time.Second
	)

	done := make(chan struct{})
	go func() {
		defer close(done)
		// The result is irrelevant: the page carries one real text run, so
		// this converts either way. Only how long it takes is under test.
		_, _ = Convert(unmatchedShowTextPDF(operators), Options{Format: "pdf"})
	}()

	select {
	case <-done:
	case <-time.After(budget):
		t.Fatalf("converting a PDF with %d unmatched TJ operators took over %s; "+
			"the detector's operand lookback is no longer bounded", operators, budget)
	}
}

// buildDocx writes the smallest OOXML package that carries a heading, a
// paragraph, and one embedded image.
func buildDocx(t *testing.T) []byte {
	t.Helper()

	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Default Extension="png" ContentType="image/png"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
  <Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`,
		"word/styles.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="paragraph" w:styleId="Heading1">
    <w:name w:val="heading 1"/>
    <w:pPr><w:outlineLvl w:val="0"/></w:pPr>
  </w:style>
</w:styles>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
  <Relationship Id="rId10" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
</Relationships>`,
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
            xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
            xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
            xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture">
  <w:body>
    <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Quarterly report</w:t></w:r></w:p>
    <w:p><w:r><w:t>Widgets shipped on time.</w:t></w:r></w:p>
    <w:p><w:r><w:drawing><wp:inline><wp:docPr id="1" name="Chart" descr="Shipping chart"/>
      <a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture">
        <pic:pic><pic:nvPicPr><pic:cNvPr id="1" name="Chart"/><pic:cNvPicPr/></pic:nvPicPr>
          <pic:blipFill><a:blip r:embed="rId10"/></pic:blipFill>
          <pic:spPr/></pic:pic>
      </a:graphicData></a:graphic>
    </wp:inline></w:drawing></w:r></w:p>
    <w:p><w:r><w:t>Closing remarks.</w:t></w:r></w:p>
  </w:body>
</w:document>`,
	}

	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	for name, content := range parts {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	image, err := archive.Create("word/media/image1.png")
	if err != nil {
		t.Fatalf("create image part: %v", err)
	}
	if _, err := image.Write(onePixelPNG()); err != nil {
		t.Fatalf("write image part: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	return buf.Bytes()
}

// onePixelPNG is a 1x1 transparent PNG: the smallest thing a container will
// accept as an image part.
func onePixelPNG() []byte {
	return []byte{
		0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
		0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
		0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01,
		0x0d, 0x0a, 0x2d, 0xb4,
		0x00, 0x00, 0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
	}
}

// minimalPDF is a one-page PDF with a single text run, written by hand so the
// test carries no binary fixture.
func minimalPDF() []byte {
	content := "BT /F1 12 Tf 20 100 Td (Shipping summary) Tj ET\n"
	return writePDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Contents 4 0 R " +
			"/Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	})
}

// nestedPDF is a one-page PDF carrying the given nested value in its catalog.
func nestedPDF(nested string) []byte {
	return writePDF([]string{
		"<< /Type /Catalog /Pages 2 0 R /Nested " + nested + " >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>",
	})
}

// unmatchedShowTextPDF is a one-page PDF whose content stream shows text once
// and then repeats a TJ operator whose array operand is never opened.
func unmatchedShowTextPDF(operators int) []byte {
	content := "BT /F1 12 Tf 20 100 Td (Shipping summary) Tj ET\n" +
		strings.Repeat("] TJ\n", operators)
	return writePDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Contents 4 0 R " +
			"/Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	})
}

// textlessPDF is a one-page PDF with no text stream: the shape of a scanned
// exam paper as far as pdf-inspector is concerned.
func textlessPDF() []byte {
	return writePDF([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>",
	})
}

func writePDF(objects []string) []byte {
	var pdf bytes.Buffer
	offsets := make([]int, len(objects))
	pdf.WriteString("%PDF-1.4\n")
	for i, object := range objects {
		offsets[i] = pdf.Len()
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}

	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return pdf.Bytes()
}
