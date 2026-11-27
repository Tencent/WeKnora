package searchutil

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// A repeated Markdown image link used to be enriched by strings.Replace on
// the evolving string. The inserted block quotes the link itself (in
// <image_original>, or verbatim in the caption variants), so the second
// replacement hit the copy inside the first block and nested the XML.
func TestEnrichContentWithImageInfo_RepeatedImageGetsOneBlockEach(t *testing.T) {
	info := types.ImageInfo{URL: "u.png", Caption: "cap", OCRText: "ocr text"}
	content := "before ![a](u.png) middle ![a](u.png) after"

	got := EnrichContentWithImageInfo(content, imageInfoJSON(t, info))

	if n := strings.Count(got, "<image "); n != 2 {
		t.Errorf("expected exactly 2 <image> blocks, got %d:\n%s", n, got)
	}
	if n := strings.Count(got, "<image_original>![a](u.png)</image_original>"); n != 2 {
		t.Errorf("expected 2 <image_original> copies, got %d:\n%s", n, got)
	}
	if n := strings.Count(got, "<image_caption>cap</image_caption>"); n != 2 {
		t.Errorf("expected 2 captions, got %d:\n%s", n, got)
	}
	if n := strings.Count(got, "<image_ocr>ocr text</image_ocr>"); n != 2 {
		t.Errorf("expected 2 OCR blocks, got %d:\n%s", n, got)
	}
	if strings.Contains(got, "<image_original><image") {
		t.Errorf("second block nested inside the first <image_original>:\n%s", got)
	}
	if !strings.HasPrefix(got, "before ") || !strings.HasSuffix(got, " after") {
		t.Errorf("surrounding text was not preserved:\n%s", got)
	}
}

func TestEnrichContentCaptionOnly_RepeatedImageGetsOneCaptionEach(t *testing.T) {
	info := types.ImageInfo{URL: "u.png", Caption: "cap", OCRText: "ocr text"}
	content := "![a](u.png) and ![a](u.png)"

	got := EnrichContentCaptionOnly(content, imageInfoJSON(t, info))

	if n := strings.Count(got, "![a](u.png)"); n != 2 {
		t.Errorf("expected the link itself twice, got %d:\n%s", n, got)
	}
	// Adjacency matters: on the old strings.Replace loop the second caption
	// landed after the first link, leaving the second link bare, while the
	// plain caption count still read 2.
	if n := strings.Count(got, "![a](u.png)\n<image_caption>cap</image_caption>"); n != 2 {
		t.Errorf("expected each link immediately followed by its caption (2), got %d:\n%s", n, got)
	}
	if strings.Contains(got, "ocr text") {
		t.Errorf("caption-only output must not contain OCR text:\n%s", got)
	}
}

func TestEnrichContentCaptionAndOCR_RepeatedImageGetsOneBlockEach(t *testing.T) {
	info := types.ImageInfo{URL: "u.png", Caption: "cap", OCRText: "ocr text"}
	content := "![a](u.png) and ![a](u.png)"

	got := EnrichContentCaptionAndOCR(content, imageInfoJSON(t, info))

	if n := strings.Count(got, "![a](u.png)"); n != 2 {
		t.Errorf("expected the link itself twice, got %d:\n%s", n, got)
	}
	// Adjacency matters, as in the caption-only case: the old loop stacked
	// both blocks after the first link and left the second link bare.
	want := "![a](u.png)\n<image_caption>cap</image_caption>\n<image_ocr>ocr text</image_ocr>"
	if n := strings.Count(got, want); n != 2 {
		t.Errorf("expected each link immediately followed by its caption+OCR block (2), got %d:\n%s", n, got)
	}
}
