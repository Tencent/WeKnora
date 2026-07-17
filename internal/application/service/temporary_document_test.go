package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
)

// fakeVLM is a minimal VLM stub that records calls and returns a fixed response.
// The mutex guards calls because multi-page OCR requests run concurrently, so
// the stub must be safe under the race detector.
type fakeVLM struct {
	response string

	mu    sync.Mutex
	calls int
}

func (f *fakeVLM) Predict(context.Context, [][]byte, string) (string, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.response == "" {
		return "extracted document text from image", nil
	}
	return f.response, nil
}

func (f *fakeVLM) GetModelName() string { return "fake-vlm" }
func (f *fakeVLM) GetModelID() string   { return "fake" }

// promptAwareVLM distinguishes OCR calls from caption calls by inspecting the
// prompt, so tests can assert the OCR-first cascade (caption only fires as a
// fallback). The caption prompt is the only one mentioning a "description of
// the main content" of the image.
type promptAwareVLM struct {
	ocrResponse     string
	captionResponse string

	mu           sync.Mutex
	ocrCalls     int
	captionCalls int
}

func (f *promptAwareVLM) Predict(_ context.Context, _ [][]byte, prompt string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Contains(prompt, "description of the main content") {
		f.captionCalls++
		return f.captionResponse, nil
	}
	f.ocrCalls++
	return f.ocrResponse, nil
}

func (f *promptAwareVLM) GetModelName() string { return "prompt-aware-vlm" }
func (f *promptAwareVLM) GetModelID() string   { return "fake" }

// fakeVLMModelService embeds the shared stub and overrides GetVLMModel.
type fakeVLMModelService struct {
	stubModelService
	model vlm.VLM
}

func (s *fakeVLMModelService) GetVLMModel(context.Context, string) (vlm.VLM, error) {
	return s.model, nil
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
	fv := &fakeVLM{response: "a cat sitting on a mat"}
	svc := &temporaryDocumentService{modelService: &fakeVLMModelService{model: fv}}
	options := types.TemporaryDocumentCreateOptions{VLMModelID: "fake"}
	content := svc.applyImageUnderstanding(context.Background(), "png", options, []byte("imgbytes"), nil, "")
	if !strings.Contains(content, "a cat sitting on a mat") {
		t.Fatalf("image understanding should inject VLM text, got %q", content)
	}
	if fv.calls == 0 {
		t.Fatal("VLM should have been invoked for an image file")
	}
}

func TestApplyImageUnderstandingImageOCRSufficientSkipsCaption(t *testing.T) {
	fv := &promptAwareVLM{
		ocrResponse:     strings.Repeat("发票明细行内容 ", 8), // > temporaryDocumentOCRSufficientRunes
		captionResponse: "should-not-be-used",
	}
	svc := &temporaryDocumentService{modelService: &fakeVLMModelService{model: fv}}
	options := types.TemporaryDocumentCreateOptions{VLMModelID: "fake"}
	content := svc.applyImageUnderstanding(context.Background(), "png", options, []byte("imgbytes"), nil, "")
	if strings.Contains(content, "should-not-be-used") {
		t.Fatalf("text-rich OCR must not trigger a caption fallback, got %q", content)
	}
	if fv.captionCalls != 0 {
		t.Fatalf("caption calls = %d, want 0 when OCR is sufficient", fv.captionCalls)
	}
	if fv.ocrCalls != 1 {
		t.Fatalf("ocr calls = %d, want 1 for a single image", fv.ocrCalls)
	}
}

func TestApplyImageUnderstandingImageCaptionFallbackOnSparseOCR(t *testing.T) {
	fv := &promptAwareVLM{
		ocrResponse:     "No text content.", // sanitized to empty → sparse OCR
		captionResponse: "a flowchart describing the login process",
	}
	svc := &temporaryDocumentService{modelService: &fakeVLMModelService{model: fv}}
	options := types.TemporaryDocumentCreateOptions{VLMModelID: "fake"}
	content := svc.applyImageUnderstanding(context.Background(), "png", options, []byte("imgbytes"), nil, "")
	if !strings.Contains(content, "a flowchart describing the login process") {
		t.Fatalf("sparse OCR should fall back to a caption, got %q", content)
	}
	if fv.captionCalls != 1 {
		t.Fatalf("caption calls = %d, want 1 as an OCR fallback", fv.captionCalls)
	}
	if fv.ocrCalls != 1 {
		t.Fatalf("ocr calls = %d, want 1 before falling back", fv.ocrCalls)
	}
}

func TestApplyImageUnderstandingScannedDocumentNeverCaptions(t *testing.T) {
	fv := &promptAwareVLM{
		ocrResponse:     "No text content.",
		captionResponse: "should-not-be-used",
	}
	svc := &temporaryDocumentService{modelService: &fakeVLMModelService{model: fv}}
	options := types.TemporaryDocumentCreateOptions{VLMModelID: "fake", ImageUnderstanding: true}
	pages := [][]byte{[]byte("page-1"), []byte("page-2")}
	content := svc.applyImageUnderstanding(context.Background(), "pdf", options, nil, pages, "![p](x)")
	if strings.Contains(content, "should-not-be-used") {
		t.Fatalf("scanned documents must not use the caption fallback, got %q", content)
	}
	if fv.captionCalls != 0 {
		t.Fatalf("caption calls = %d, want 0 for scanned documents", fv.captionCalls)
	}
}

func TestApplyImageUnderstandingWithoutVLMModelIsNoop(t *testing.T) {
	fv := &fakeVLM{}
	svc := &temporaryDocumentService{modelService: &fakeVLMModelService{model: fv}}
	options := types.TemporaryDocumentCreateOptions{}
	if got := svc.applyImageUnderstanding(context.Background(), "png", options, []byte("x"), nil, ""); got != "" {
		t.Fatalf("no VLM model should be a no-op, got %q", got)
	}
	if fv.calls != 0 {
		t.Fatal("VLM must not be called without a configured model")
	}
}

func TestApplyImageUnderstandingDocumentGatedByFlag(t *testing.T) {
	fv := &fakeVLM{}
	svc := &temporaryDocumentService{modelService: &fakeVLMModelService{model: fv}}
	options := types.TemporaryDocumentCreateOptions{VLMModelID: "fake", ImageUnderstanding: false}
	pages := [][]byte{[]byte("page-1")}
	if got := svc.applyImageUnderstanding(context.Background(), "pdf", options, nil, pages, "![p](x)"); got != "" {
		t.Fatalf("OCR fallback must stay off when the switch is disabled, got %q", got)
	}
	if fv.calls != 0 {
		t.Fatal("VLM must not run for a document when understanding is disabled")
	}
}

func TestApplyImageUnderstandingScannedDocumentRunsOCR(t *testing.T) {
	fv := &fakeVLM{response: "第一页扫描文字内容"}
	svc := &temporaryDocumentService{modelService: &fakeVLMModelService{model: fv}}
	options := types.TemporaryDocumentCreateOptions{VLMModelID: "fake", ImageUnderstanding: true}
	pages := [][]byte{[]byte("page-1")}
	content := svc.applyImageUnderstanding(context.Background(), "pdf", options, nil, pages, "![p](x)")
	if !strings.Contains(content, "第一页扫描文字内容") {
		t.Fatalf("scanned document should get OCR text merged, got %q", content)
	}
}

func TestApplyImageUnderstandingHighTextDocumentSkipsOCR(t *testing.T) {
	fv := &fakeVLM{}
	svc := &temporaryDocumentService{modelService: &fakeVLMModelService{model: fv}}
	options := types.TemporaryDocumentCreateOptions{VLMModelID: "fake", ImageUnderstanding: true}
	pages := [][]byte{[]byte("page-1")}
	longText := strings.Repeat("这是一段已经解析出来的正文内容。", 40)
	if got := svc.applyImageUnderstanding(context.Background(), "pdf", options, nil, pages, longText); got != "" {
		t.Fatalf("text-rich document should not trigger OCR, got a change")
	}
	if fv.calls != 0 {
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
