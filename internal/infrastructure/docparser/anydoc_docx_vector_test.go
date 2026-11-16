package docparser

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestAnydocReaderRoutesDOCXVectorMedia(t *testing.T) {
	for _, name := range []string{"word/media/image1.emf", "word/media/image2.WMF"} {
		t.Run(name, func(t *testing.T) {
			fallback := &stubDocReader{result: &types.ReadResult{}}
			req := &types.ReadRequest{
				FileName: "document.docx", FileType: "docx", ParserEngine: AnydocEngineName,
				FileContent: presentationArchive(t, name),
			}
			result, err := NewAnydocReader(nil, fallback).Read(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if fallback.got == nil || fallback.got.ParserEngine != "markitdown" {
				t.Fatal("DOCX vector media did not reach the fallback")
			}
			if req.ParserEngine != AnydocEngineName || result.Metadata["anydoc_fallback"] != "docx_vector_media" {
				t.Fatal("fallback modified the original request or omitted its metadata")
			}
		})
	}
}

func TestDOCXVectorMediaDetection(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"word/media/picture.emf", true},
		{"word/media/picture.WMF", true},
		{"word/media/picture.png", false},
		{"ppt/media/picture.emf", false},
		{"word/media/picture.emf/", false},
	} {
		if got := officeHasVectorMedia(presentationArchive(t, tc.name), "word/media/"); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	if officeHasVectorMedia([]byte("invalid zip"), "word/media/") {
		t.Fatal("invalid archive reported vector media")
	}
}

func TestDOCXVectorMediaFallbackFailsExplicitly(t *testing.T) {
	req := &types.ReadRequest{
		FileName: "document.docx", FileType: "docx",
		FileContent: presentationArchive(t, "word/media/image.emf"),
	}
	if _, err := NewAnydocReader(nil, nil).Read(context.Background(), req); err == nil ||
		!strings.Contains(err.Error(), "connected docreader") {
		t.Fatalf("missing fallback: got %v", err)
	}
	sentinel := errors.New("fallback failed")
	_, err := NewAnydocReader(nil, &stubDocReader{err: sentinel}).Read(context.Background(), req)
	if !errors.Is(err, sentinel) {
		t.Fatalf("fallback error was lost: %v", err)
	}
}

func TestDOCXVectorMediaDoesNotRouteWhenImagesDisabled(t *testing.T) {
	fallback := &stubDocReader{}
	req := &types.ReadRequest{
		FileName: "document.docx", FileType: "docx",
		FileContent: presentationArchive(t, "word/media/image.emf"),
	}
	_, _ = NewAnydocReader(map[string]string{"anydoc_extract_images": "false"}, fallback).
		Read(context.Background(), req)
	if fallback.got != nil {
		t.Fatal("image-disabled parsing must not require the fallback")
	}
}
