package docparser

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func presentationArchive(t *testing.T, names ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, name := range names {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, "/") {
			continue
		}
		if _, err := f.Write([]byte("synthetic media")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAnydocReaderRoutesPPTXVectorMedia(t *testing.T) {
	for _, media := range []string{"ppt/media/chart.emf", "ppt/media/equation.wmf", "ppt/media/CHART.EMF"} {
		t.Run(media, func(t *testing.T) {
			req := &types.ReadRequest{
				FileName: "slides.pptx", FileType: "pptx", ParserEngine: AnydocEngineName,
				FileContent: presentationArchive(t, media),
				Title:       "Process diagram", RequestID: "vector-routing-regression",
				ParserEngineOverrides: map[string]string{"parser_max_workers": "2"},
			}
			original := *req
			want := &types.ReadResult{
				MarkdownContent: "![chart](images/chart.png)",
				ImageRefs: []types.ImageRef{{
					Filename: "chart.png", OriginalRef: "images/chart.png", ImageData: []byte("png"),
				}},
			}
			fallback := &stubDocReader{result: want}
			got, err := NewAnydocReader(nil, fallback).Read(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if got != want || !reflect.DeepEqual(got.ImageRefs, want.ImageRefs) {
				t.Fatalf("fallback result was not preserved: %+v", got)
			}
			if fallback.got == nil {
				t.Fatal("vector presentation did not reach docreader")
			}
			expected := original
			expected.ParserEngine = "markitdown"
			if !reflect.DeepEqual(*fallback.got, expected) || !reflect.DeepEqual(*req, original) {
				t.Fatal("fallback must change only its copied request's engine")
			}
			if got.Metadata["anydoc_fallback"] != "pptx_vector_media" || got.Metadata["parser"] != "markitdown" {
				t.Fatalf("missing fallback metadata: %+v", got.Metadata)
			}
		})
	}
}

func TestPPTXVectorMediaDetection(t *testing.T) {
	if pptxHasVectorMedia([]byte("not a ZIP")) {
		t.Fatal("invalid ZIP reported vector media")
	}
	if pptxHasVectorMedia(presentationArchive(t, "ppt/media/chart.emf/")) {
		t.Fatal("directory reported as a vector image")
	}
}

func TestAnydocReaderDoesNotRoutePPTMToMarkitdown(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fileType string
		fileName string
	}{
		{"explicit type", "pptm", "slides.pptm"},
		{"explicit type takes precedence", "pptm", "slides.pptx"},
		{"normalized explicit type", ".PPTM", "slides.pptm"},
		{"inferred type", "", "slides.pptm"},
		{"inferred uppercase type", "", "slides.PPTM"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fallback := &stubDocReader{}
			// The synthetic archive may fail native conversion, but PPTM must
			// stay on anydoc: docreader does not support this file type.
			_, _ = NewAnydocReader(nil, fallback).Read(context.Background(), &types.ReadRequest{
				FileName: tc.fileName, FileType: tc.fileType,
				FileContent: presentationArchive(t, "ppt/media/chart.emf"),
			})
			if fallback.got != nil {
				t.Fatal("PPTM input incorrectly routed to MarkItDown")
			}
		})
	}
}

func TestAnydocReaderPPTXVectorFallbackFailures(t *testing.T) {
	sentinel := errors.New("docreader failed")
	for _, tc := range []struct {
		name   string
		reader *AnydocReader
		want   string
	}{
		{"missing", NewAnydocReader(nil, nil), "requires a connected docreader"},
		{"disconnected", NewAnydocReader(nil, &disconnectedDocReader{}), "requires a connected docreader"},
		{"error", NewAnydocReader(nil, &stubDocReader{err: sentinel}), "docreader failed"},
		{"nil result", NewAnydocReader(nil, &stubDocReader{}), "returned no result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.reader.Read(context.Background(), &types.ReadRequest{
				FileName: "slides.pptx", FileContent: presentationArchive(t, "ppt/media/chart.emf"),
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			if tc.name == "error" && !errors.Is(err, sentinel) {
				t.Fatal("fallback error lost its cause")
			}
		})
	}
}

func TestAnydocReaderDoesNotRouteOtherInputsToMarkitdown(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fileType  string
		media     string
		overrides map[string]string
	}{
		{"raster", "pptx", "ppt/media/chart.png", nil},
		{"svg", "pptx", "ppt/media/chart.svg", nil},
		{"unrelated directory", "pptx", "custom/chart.emf", nil},
		{"word", "docx", "word/media/chart.emf", nil},
		{"text only", "pptx", "ppt/media/chart.emf", map[string]string{"anydoc_extract_images": "false"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fallback := &stubDocReader{}
			// Conversion may reject this directory-only synthetic archive. It
			// must nevertheless never send these controls to MarkItDown.
			_, _ = NewAnydocReader(tc.overrides, fallback).Read(context.Background(), &types.ReadRequest{
				FileType: tc.fileType, FileContent: presentationArchive(t, tc.media),
			})
			if fallback.got != nil {
				t.Fatal("non-vector or text-only input unexpectedly used fallback")
			}
		})
	}
}
