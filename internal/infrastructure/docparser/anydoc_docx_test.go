package docparser

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestAnydocReaderRoutesDOCXMetafilesToDocreader(t *testing.T) {
	for _, tc := range []struct{ media, fileName, fileType string }{
		{"word/media/diagram.emf", "diagram.docx", ""},
		{"word/media/diagram.wmf", "diagram.docx", ".DOCX"},
		{"word/media/DIAGRAM.EMF", "diagram.docm", "docm"},
	} {
		t.Run(tc.media, func(t *testing.T) {
			want := &types.ReadResult{
				MarkdownContent: "before ![diagram](images/diagram.png) after",
				ImageRefs: []types.ImageRef{{
					OriginalRef: "images/diagram.png", MimeType: "image/png", ImageData: []byte("rendered PNG"),
				}},
			}
			fallback := &stubDocReader{result: want}
			req := &types.ReadRequest{
				FileName: tc.fileName, FileType: tc.fileType, FileContent: officeMediaArchive(t, tc.media),
				ParserEngine: AnydocEngineName, RequestID: "request-id",
				ParserEngineOverrides: map[string]string{"custom": "value"},
			}
			got, err := NewAnydocReader(nil, fallback).Read(context.Background(), req)
			if err != nil || got != want {
				t.Fatalf("Read() = %#v, %v; want docreader's rendered image result", got, err)
			}
			if fallback.got == nil || fallback.got == req || fallback.got.ParserEngine != "markitdown" || fallback.got.FileType != "docx" ||
				!bytes.Equal(fallback.got.FileContent, req.FileContent) || fallback.got.RequestID != req.RequestID ||
				fallback.got.ParserEngineOverrides["custom"] != "value" || req.ParserEngine != AnydocEngineName {
				t.Fatalf("fallback lost the original document/options or mutated the request: %#v", fallback.got)
			}
			if got.Metadata["parser"] != "markitdown" || got.Metadata["anydoc_fallback"] != "docx_metafile" {
				t.Fatalf("fallback metadata = %#v", got.Metadata)
			}
		})
	}
}

func TestAnydocReaderDOCXMetafileFallbackFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fallback interfaces.DocReader
		want     string
	}{
		{"missing", nil, "docreader"},
		{"disconnected", &disconnectedDocReader{}, "docreader"},
		{"error", &stubDocReader{err: context.Canceled}, "context canceled"},
		{"nil result", &stubDocReader{}, "returned no result"},
		{"result error", &stubDocReader{result: &types.ReadResult{Error: "conversion failed"}}, "conversion failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := NewAnydocReader(nil, tc.fallback).Read(context.Background(), &types.ReadRequest{
				FileName: "diagram.docx", FileContent: officeMediaArchive(t, "word/media/diagram.emf"),
			})
			if err == nil || result != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Read() = %#v, %v; want error containing %q", result, err, tc.want)
			}
			if tc.name == "error" && !errors.Is(err, context.Canceled) {
				t.Fatalf("fallback lost cancellation: %v", err)
			}
		})
	}
}

func TestAnydocReaderDoesNotRouteOtherDocuments(t *testing.T) {
	for _, tc := range []struct {
		name, fileType, media, extract string
	}{
		{"photo", "docx", "word/media/photo.png", "true"},
		{"text only", "docx", "word/media/diagram.emf", "false"},
		{"other format", "pptx", "ppt/media/diagram.emf", "true"},
		{"outside media", "docx", "custom/diagram.emf", "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fallback := &stubDocReader{result: &types.ReadResult{}}
			_, err := NewAnydocReader(map[string]string{"anydoc_extract_images": tc.extract}, fallback).Read(
				context.Background(), &types.ReadRequest{FileType: tc.fileType, FileContent: officeMediaArchive(t, tc.media)})
			// These archives omit document XML, so the converter must reject
			// them even in a build with anydoc linked.
			if err == nil || fallback.got != nil {
				t.Fatalf("unexpected fallback for %s: request=%#v, error=%v", tc.name, fallback.got, err)
			}
		})
	}
}

// Routing reads only the ZIP directory; the fallback receives the untouched bytes.
func officeMediaArchive(t *testing.T, name string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	file, err := archive.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("embedded media")); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
