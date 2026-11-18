package docparser

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser/anydoc"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestAnydocReaderRejectsUnsupportedFileType(t *testing.T) {
	_, err := NewAnydocReader(nil, nil).Read(context.Background(), &types.ReadRequest{
		FileName:    "photo.png",
		FileType:    "png",
		FileContent: []byte("not really a png"),
	})
	if err == nil {
		t.Fatal("Read succeeded for an unsupported file type, want an error")
	}
	if !strings.Contains(err.Error(), "png") {
		t.Errorf("error does not name the file type: %v", err)
	}
}

func TestAnydocReaderRejectsURLs(t *testing.T) {
	_, err := NewAnydocReader(nil, nil).Read(context.Background(), &types.ReadRequest{
		URL:      "https://example.com/report.docx",
		FileType: "docx",
	})
	if err == nil {
		t.Fatal("Read succeeded for a URL request, want an error")
	}
}

func TestNewAnydocReaderHonoursImageExtractionOverride(t *testing.T) {
	if !NewAnydocReader(nil, nil).extractImages {
		t.Error("image extraction is off by default, want on")
	}
	if NewAnydocReader(map[string]string{"anydoc_extract_images": "false"}, nil).extractImages {
		t.Error("image extraction stayed on after the override turned it off")
	}
}

func TestImageRefsFromAssetsMatchInPlaceMarkdownPaths(t *testing.T) {
	refs := imageRefsFromAssets([]anydoc.Asset{
		{Name: "image-1.png", MediaType: "image/png", Data: []byte("first")},
		{Name: "image-2.jpg", MediaType: "image/jpeg", Data: []byte("second")},
	})
	if len(refs) != 2 {
		t.Fatalf("got %d image refs, want 2", len(refs))
	}
	if refs[0].OriginalRef != "images/image-1.png" {
		t.Errorf("first OriginalRef = %q, want images/image-1.png", refs[0].OriginalRef)
	}
	if refs[1].MimeType != "image/jpeg" {
		t.Errorf("second mime type = %q, want image/jpeg", refs[1].MimeType)
	}
	if len(refs[0].ImageData) == 0 {
		t.Error("image ref carries no bytes")
	}
}

func TestImageRefsFromAssetsEmpty(t *testing.T) {
	if refs := imageRefsFromAssets(nil); refs != nil {
		t.Errorf("got %d image refs, want none", len(refs))
	}
}

func TestAnydocReaderFallsBackForScannedPDF(t *testing.T) {
	fallback := &stubDocReader{result: &types.ReadResult{
		MarkdownContent: "![page](images/page_1.jpg)",
		ImageRefs: []types.ImageRef{{
			Filename:    "page_1.jpg",
			OriginalRef: "images/page_1.jpg",
			ImageData:   []byte("jpeg"),
		}},
		Metadata: map[string]string{"image_source_type": "scanned_pdf"},
	}}
	result, err := NewAnydocReader(nil, fallback).readPDFFallback(context.Background(), &types.ReadRequest{
		FileName: "exam.pdf",
		FileType: "pdf",
	}, errors.New("PDF has no extractable text (Scanned, 5 pages): OCR is required"), "scanned_pdf")
	if err != nil {
		t.Fatalf("readScannedPDF: %v", err)
	}
	if !strings.Contains(result.MarkdownContent, "images/page_1.jpg") {
		t.Errorf("fallback markdown missing page image:\n%s", result.MarkdownContent)
	}
	if result.Metadata["anydoc_fallback"] != "scanned_pdf" {
		t.Errorf("anydoc_fallback = %q, want scanned_pdf", result.Metadata["anydoc_fallback"])
	}
	if result.Metadata["image_source_type"] != "scanned_pdf" {
		t.Errorf("image_source_type = %q, want scanned_pdf", result.Metadata["image_source_type"])
	}
	if fallback.got == nil || fallback.got.ParserEngine != BuiltinEngineName {
		t.Errorf("fallback request engine = %v, want builtin", fallback.got)
	}
}

func TestAnydocReaderScannedPDFWithoutFallbackKeepsError(t *testing.T) {
	_, err := NewAnydocReader(nil, nil).readPDFFallback(context.Background(), &types.ReadRequest{
		FileName: "exam.pdf",
	}, errors.New("PDF has no extractable text: OCR is required"), "scanned_pdf")
	if err == nil {
		t.Fatal("readScannedPDF succeeded without a fallback, want an error")
	}
	if !strings.Contains(err.Error(), "OCR is required") {
		t.Errorf("error does not mention OCR: %v", err)
	}
}

func TestAnydocReaderDisconnectedFallbackKeepsError(t *testing.T) {
	fallback := &disconnectedDocReader{}
	_, err := NewAnydocReader(nil, fallback).readPDFFallback(context.Background(), &types.ReadRequest{
		FileName: "exam.pdf",
	}, errors.New("PDF has no extractable text: OCR is required"), "scanned_pdf")
	if err == nil {
		t.Fatal("readScannedPDF succeeded with a disconnected fallback, want an error")
	}
	if !strings.Contains(err.Error(), "OCR is required") {
		t.Errorf("error does not mention OCR: %v", err)
	}
}

func TestAnydocReaderStructureFallbackPreservesPageClassification(t *testing.T) {
	for _, source := range []string{"pdf_text_layer", "mixed_pdf", ""} {
		t.Run(source, func(t *testing.T) {
			fallback := &stubDocReader{result: &types.ReadResult{
				MarkdownContent: "recovered text",
				Metadata:        map[string]string{"image_source_type": source},
			}}
			req := &types.ReadRequest{
				FileName:     "report.pdf",
				FileType:     "pdf",
				FileContent:  []byte("original bytes"),
				ParserEngine: AnydocEngineName,
			}
			result, err := NewAnydocReader(nil, fallback).readPDFFallback(
				context.Background(), req, errors.New("invalid PDF structure"), "invalid_structure")
			if err != nil {
				t.Fatal(err)
			}
			if result.Metadata["image_source_type"] != source ||
				result.Metadata["anydoc_fallback"] != "invalid_structure" {
				t.Fatalf("fallback metadata = %#v", result.Metadata)
			}
			if req.ParserEngine != AnydocEngineName || fallback.got.ParserEngine != BuiltinEngineName ||
				string(fallback.got.FileContent) != string(req.FileContent) || fallback.got == req {
				t.Fatalf("fallback mutated caller request or lost original bytes: %#v", fallback.got)
			}
		})
	}
}

func TestAnydocReaderStructureFallbackFailures(t *testing.T) {
	original := errors.New("invalid PDF structure")
	for _, tc := range []struct {
		name     string
		fallback interfaces.DocReader
		want     string
	}{
		{"missing", nil, original.Error()},
		{"disconnected", &disconnectedDocReader{}, original.Error()},
		{"error", &stubDocReader{err: errors.New("builtin failed")}, "builtin failed"},
		{"nil result", &stubDocReader{}, "returned no result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewAnydocReader(nil, tc.fallback).readPDFFallback(context.Background(),
				&types.ReadRequest{FileName: "report.pdf"}, original, "invalid_structure")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("fallback error = %v, want %q", err, tc.want)
			}
			if tc.fallback == nil && !errors.Is(err, original) {
				t.Fatalf("original conversion error was lost: %v", err)
			}
		})
	}
}

func TestAnydocReaderFallbackPreservesErrorClassification(t *testing.T) {
	for _, cause := range []error{context.Canceled, anydoc.ErrResourceLimit} {
		fallback := &stubDocReader{err: cause}
		_, err := NewAnydocReader(nil, fallback).readPDFFallback(context.Background(),
			&types.ReadRequest{FileName: "report.pdf"}, errors.New("invalid PDF structure"), "invalid_structure")
		if !errors.Is(err, cause) {
			t.Fatalf("fallback error = %v, want wrapped %v", err, cause)
		}
	}
}

func TestAnydocReaderReadFallsBackForMalformedPDF(t *testing.T) {
	if !anydoc.Available() {
		t.Skip("requires linked anydoc converter")
	}
	content := []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n")
	_, convertErr := anydoc.Convert(content, anydoc.Options{Format: "pdf"})
	if !anydoc.PDFInvalidStructure(convertErr) {
		t.Fatalf("fixture conversion error = %v, want malformed PDF", convertErr)
	}
	fallback := &stubDocReader{result: &types.ReadResult{MarkdownContent: "recovered"}}
	result, err := NewAnydocReader(nil, fallback).Read(context.Background(), &types.ReadRequest{
		FileName: "malformed.pdf", FileType: "pdf", FileContent: content,
	})
	if err != nil || result == nil || result.Metadata["anydoc_fallback"] != "invalid_structure" {
		t.Fatalf("Read() = %#v, %v", result, err)
	}
	if fallback.got == nil || string(fallback.got.FileContent) != string(content) {
		t.Fatal("Read did not hand the original PDF to the fallback")
	}
}

type stubDocReader struct {
	got    *types.ReadRequest
	result *types.ReadResult
	err    error
}

func (s *stubDocReader) Read(_ context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	s.got = req
	return s.result, s.err
}

type disconnectedDocReader struct{}

func (disconnectedDocReader) Read(context.Context, *types.ReadRequest) (*types.ReadResult, error) {
	return nil, errors.New("should not be called")
}

func (disconnectedDocReader) IsConnected() bool { return false }
