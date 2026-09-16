package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	invoketest "github.com/Tencent/WeKnora/internal/models/invoke/invoketest"
	"github.com/Tencent/WeKnora/internal/types"
)

// newVLMTestService wires the temporary-document VLM path to the invoketest
// fake adapter: responses replay one per call in queue order, and recorded
// calls are classified as OCR or caption by prompt (the caption prompt is the
// only one mentioning a "description of the main content" of the image).
func newVLMTestService(t *testing.T, responses ...string) (*invoketest.Fake, *temporaryDocumentService) {
	fake := invoketest.New(t)
	for _, r := range responses {
		fake.EnqueueResponse(invoke.ChatResponse{Content: r})
	}
	return fake, &temporaryDocumentService{modelService: &stubModelService{
		modelsByID: map[string]*types.Model{"fake": {ID: "fake"}},
		cfg:        fake.Config(),
	}}
}

// captionCalls counts recorded calls that used the caption prompt.
func captionCalls(fake *invoketest.Fake) int {
	n := 0
	for _, rec := range fake.Calls() {
		if len(rec.Opts.Messages) > 0 &&
			strings.Contains(rec.Opts.Messages[0].Text(), "description of the main content") {
			n++
		}
	}
	return n
}

func TestApproxTextContentRunes(t *testing.T) {
	if got := approxTextContentRunes("![page](provider://img/1.png)\n\n   "); got != 0 {
		t.Fatalf("image-only markdown should register as 0 text runes, got %d", got)
	}
	if got := approxTextContentRunes("hello world"); got != 11 {
		t.Fatalf("plain text rune count = %d, want 11", got)
	}
}

func TestCollectImageBytes(t *testing.T) {
	refs := []types.ImageRef{
		{ImageData: []byte("a")},
		{ImageData: nil},
		{ImageData: []byte("b")},
		{ImageData: []byte("c")},
	}
	got := collectImageBytes(refs, 2)
	if len(got) != 2 {
		t.Fatalf("collectImageBytes cap = %d, want 2", len(got))
	}
	if collectImageBytes(refs, 0) != nil {
		t.Fatal("zero limit must yield no images")
	}
}

func TestApplyImageUnderstandingImageFileRunsVLM(t *testing.T) {
	fake, svc := newVLMTestService(t, "a cat sitting on a mat")
	options := types.TemporaryDocumentCreateOptions{VLMModelID: "fake"}
	content := svc.applyImageUnderstanding(context.Background(), "png", options, []byte("imgbytes"), nil, "")
	if !strings.Contains(content, "a cat sitting on a mat") {
		t.Fatalf("image understanding should inject VLM text, got %q", content)
	}
	if len(fake.Calls()) == 0 {
		t.Fatal("VLM should have been invoked for an image file")
	}
}

func TestApplyImageUnderstandingImageOCRSufficientSkipsCaption(t *testing.T) {
	ocrText := strings.Repeat("发票明细行内容 ", 8) // > temporaryDocumentOCRSufficientRunes
	fake, svc := newVLMTestService(t, ocrText)
	options := types.TemporaryDocumentCreateOptions{VLMModelID: "fake"}
	svc.applyImageUnderstanding(context.Background(), "png", options, []byte("imgbytes"), nil, "")
	if got := captionCalls(fake); got != 0 {
		t.Fatalf("text-rich OCR must not trigger a caption fallback, caption calls = %d", got)
	}
	if len(fake.Calls()) != 1 {
		t.Fatalf("VLM calls = %d, want 1 (OCR only) for a single image", len(fake.Calls()))
	}
}

func TestApplyImageUnderstandingImageCaptionFallbackOnSparseOCR(t *testing.T) {
	fake, svc := newVLMTestService(t, "No text content.", "a flowchart describing the login process")
	options := types.TemporaryDocumentCreateOptions{VLMModelID: "fake"}
	content := svc.applyImageUnderstanding(context.Background(), "png", options, []byte("imgbytes"), nil, "")
	if !strings.Contains(content, "a flowchart describing the login process") {
		t.Fatalf("sparse OCR should fall back to a caption, got %q", content)
	}
	if got := captionCalls(fake); got != 1 {
		t.Fatalf("caption calls = %d, want 1 as an OCR fallback", got)
	}
	if len(fake.Calls()) != 2 {
		t.Fatalf("VLM calls = %d, want 2 (OCR then caption)", len(fake.Calls()))
	}
}

func TestApplyImageUnderstandingScannedDocumentNeverCaptions(t *testing.T) {
	fake, svc := newVLMTestService(t, "No text content.", "No text content.")
	options := types.TemporaryDocumentCreateOptions{VLMModelID: "fake", ImageUnderstanding: true}
	pages := [][]byte{[]byte("page-1"), []byte("page-2")}
	content := svc.applyImageUnderstanding(context.Background(), "pdf", options, nil, pages, "![p](x)")
	if strings.Contains(content, "a flowchart") {
		t.Fatalf("scanned documents must not use the caption fallback, got %q", content)
	}
	if got := captionCalls(fake); got != 0 {
		t.Fatalf("caption calls = %d, want 0 for scanned documents", got)
	}
	if len(fake.Calls()) != 2 {
		t.Fatalf("VLM calls = %d, want 2 (one OCR per page)", len(fake.Calls()))
	}
}

func TestApplyImageUnderstandingWithoutVLMModelIsNoop(t *testing.T) {
	fake, svc := newVLMTestService(t)
	options := types.TemporaryDocumentCreateOptions{}
	if got := svc.applyImageUnderstanding(context.Background(), "png", options, []byte("x"), nil, ""); got != "" {
		t.Fatalf("no VLM model should be a no-op, got %q", got)
	}
	if len(fake.Calls()) != 0 {
		t.Fatal("VLM must not be called without a configured model")
	}
}

func TestApplyImageUnderstandingDocumentGatedByFlag(t *testing.T) {
	fake, svc := newVLMTestService(t)
	options := types.TemporaryDocumentCreateOptions{VLMModelID: "fake", ImageUnderstanding: false}
	pages := [][]byte{[]byte("page-1")}
	if got := svc.applyImageUnderstanding(context.Background(), "pdf", options, nil, pages, "![p](x)"); got != "" {
		t.Fatalf("OCR fallback must stay off when the switch is disabled, got %q", got)
	}
	if len(fake.Calls()) != 0 {
		t.Fatal("VLM must not run for a document when understanding is disabled")
	}
}

func TestApplyImageUnderstandingScannedDocumentRunsOCR(t *testing.T) {
	_, svc := newVLMTestService(t, "第一页扫描文字内容")
	options := types.TemporaryDocumentCreateOptions{VLMModelID: "fake", ImageUnderstanding: true}
	pages := [][]byte{[]byte("page-1")}
	content := svc.applyImageUnderstanding(context.Background(), "pdf", options, nil, pages, "![p](x)")
	if !strings.Contains(content, "第一页扫描文字内容") {
		t.Fatalf("scanned document should get OCR text merged, got %q", content)
	}
}

func TestApplyImageUnderstandingHighTextDocumentSkipsOCR(t *testing.T) {
	fake, svc := newVLMTestService(t)
	options := types.TemporaryDocumentCreateOptions{VLMModelID: "fake", ImageUnderstanding: true}
	pages := [][]byte{[]byte("page-1")}
	longText := strings.Repeat("这是一段已经解析出来的正文内容。", 40)
	if got := svc.applyImageUnderstanding(context.Background(), "pdf", options, nil, pages, longText); got != "" {
		t.Fatalf("text-rich document should not trigger OCR, got a change")
	}
	if len(fake.Calls()) != 0 {
		t.Fatal("VLM must not run when the document already has enough text")
	}
}

func TestSelectTemporaryDocumentContentReturnsFullSmallDocument(t *testing.T) {
	document := &types.TemporaryDocument{Content: "complete document", TokenCount: 42}
	content, selected, total := selectTemporaryDocumentContent(document, "question")
	if content != document.Content || selected != 0 || total != 0 {
		t.Fatalf("small document selection = (%q, %d, %d)", content, selected, total)
	}
}

func TestSelectTemporaryDocumentContentRanksRelevantLargeDocumentChunks(t *testing.T) {
	chunks := make([]types.TemporaryDocumentChunk, 0, 20)
	for i := 0; i < 20; i++ {
		content := "ordinary background material"
		if i == 17 {
			content = "退款政策规定，订阅后七天内可以退款。"
		}
		chunks = append(chunks, types.TemporaryDocumentChunk{Seq: i, Content: content, TokenCount: 900})
	}
	raw, err := json.Marshal(chunks)
	if err != nil {
		t.Fatal(err)
	}
	document := &types.TemporaryDocument{Content: "large", TokenCount: 18000, Chunks: types.JSON(raw)}
	content, selected, total := selectTemporaryDocumentContent(document, "退款政策是什么？")
	if !strings.Contains(content, "七天内可以退款") {
		t.Fatalf("relevant chunk was not selected: %q", content)
	}
	if selected == 0 || selected >= total || total != 20 {
		t.Fatalf("selected=%d total=%d, want a strict subset of 20", selected, total)
	}
}

func TestSelectTemporaryDocumentContentHonorsSharedPromptBudget(t *testing.T) {
	chunks := make([]types.TemporaryDocumentChunk, 0, 10)
	for i := 0; i < 10; i++ {
		chunks = append(chunks, types.TemporaryDocumentChunk{Seq: i, Content: "section", TokenCount: 1000})
	}
	raw, _ := json.Marshal(chunks)
	document := &types.TemporaryDocument{Content: "complete", TokenCount: 10000, Chunks: types.JSON(raw)}
	_, selected, total := selectTemporaryDocumentContentWithBudget(document, "", 2500)
	if selected != 2 || total != 10 {
		t.Fatalf("selected=%d total=%d, want 2/10 within a 2500-token share", selected, total)
	}
}

func TestVisualDocumentQueryDetection(t *testing.T) {
	for _, query := range []string{"解释第三页的图", "What does this chart show?", "describe the layout"} {
		if !isVisualDocumentQuery(query) {
			t.Fatalf("query %q should request visual context", query)
		}
	}
	if isVisualDocumentQuery("总结退款政策") {
		t.Fatal("plain text query should not request visual context")
	}
}
