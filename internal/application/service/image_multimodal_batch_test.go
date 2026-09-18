package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ---------------------------------------------------------------------------
// Test doubles for the batched multimodal path.
// ---------------------------------------------------------------------------

// batchVLMCall records one Predict invocation so a test can assert how many
// requests a batch actually cost and how many images each one carried.
type batchVLMCall struct {
	prompt string
	images int
}

type batchFakeVLM struct {
	calls []batchVLMCall
	reply func(prompt string, images int) (string, error)
}

func (f *batchFakeVLM) Predict(_ context.Context, imgBytes [][]byte, prompt string) (string, error) {
	f.calls = append(f.calls, batchVLMCall{prompt: prompt, images: len(imgBytes)})
	if f.reply == nil {
		return "", nil
	}
	return f.reply(prompt, len(imgBytes))
}

func (f *batchFakeVLM) GetModelName() string { return "batch-fake-vlm" }
func (f *batchFakeVLM) GetModelID() string   { return "batch-fake-vlm-id" }

var _ vlm.VLM = (*batchFakeVLM)(nil)

// batchFileService serves every read from memory so readImageBytes resolves a
// local:// reference without touching disk or the network. gets counts reads,
// which is how the "batch pre-reads, per-image stage reuses" contract is
// verified.
type batchFileService struct {
	interfaces.FileService
	body []byte
	err  error
	// failFor marks individual paths unreadable, so a batch can mix healthy and
	// broken objects.
	failFor map[string]bool
	gets    []string
}

func (s *batchFileService) GetFile(_ context.Context, filePath string) (io.ReadCloser, error) {
	s.gets = append(s.gets, filePath)
	if s.err != nil {
		return nil, s.err
	}
	if s.failFor[filePath] {
		return nil, fmt.Errorf("object %s is gone", filePath)
	}
	return io.NopCloser(bytes.NewReader(s.body)), nil
}

// batchTenantRepo reports "no tenant" so resolveFileServiceForPayload falls
// back to the service's default FileService.
type batchTenantRepo struct {
	interfaces.TenantRepository
}

func (r *batchTenantRepo) GetTenantByID(_ context.Context, _ uint64) (*types.Tenant, error) {
	return nil, nil
}

type batchChunkRepo struct {
	interfaces.ChunkRepository
	created []*types.Chunk
}

func (r *batchChunkRepo) CreateChunks(_ context.Context, chunks []*types.Chunk) error {
	r.created = append(r.created, chunks...)
	return nil
}

type batchChunkService struct {
	interfaces.ChunkService
	repo *batchChunkRepo
}

func (s *batchChunkService) GetRepository() interfaces.ChunkRepository { return s.repo }

// newBatchTestService wires a service whose file reads come from memory and
// whose knowledge base lookup returns nil, which makes indexChunks skip vector
// work (it bails out on a nil KB). That keeps the test focused on the batch
// protocol rather than on the retrieval engine.
func newBatchTestService(fileSvc interfaces.FileService, repo *batchChunkRepo) *ImageMultimodalService {
	return &ImageMultimodalService{
		chunkService: &batchChunkService{repo: repo},
		kbService:    &orphanKBService{},
		tenantRepo:   &batchTenantRepo{},
		fileSvc:      fileSvc,
	}
}

// persistedClasses counts the class written onto each persisted chunk, which is
// how the "the class is stored, not just logged" contract is checked.
func persistedClasses(t *testing.T, chunks []*types.Chunk) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, chunk := range chunks {
		var infos []types.ImageInfo
		if err := json.Unmarshal([]byte(chunk.ImageInfo), &infos); err != nil {
			t.Fatalf("decode chunk image_info: %v", err)
		}
		for _, info := range infos {
			counts[info.Class]++
		}
	}
	return counts
}

func equalCounts(got, want map[string]int) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Config / prompt / parser unit tests
// ---------------------------------------------------------------------------

func TestNormalizeImageBatchSize(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ in, want int }{
		"unset is one":        {0, 1},
		"negative is one":     {-3, 1},
		"one stays one":       {1, 1},
		"eight stays eight":   {8, 8},
		"cap is allowed":      {types.ImageBatchSizeMax, types.ImageBatchSizeMax},
		"over cap is clamped": {types.ImageBatchSizeMax + 40, types.ImageBatchSizeMax},
	}
	for name, tc := range cases {
		if got := types.NormalizeImageBatchSize(tc.in); got != tc.want {
			t.Errorf("%s: NormalizeImageBatchSize(%d) = %d, want %d", name, tc.in, got, tc.want)
		}
	}
}

func TestImageMultimodalBatchSizeReadsKBConfig(t *testing.T) {
	t.Parallel()
	if got := imageMultimodalBatchSize(nil); got != 1 {
		t.Fatalf("nil KB should default to 1, got %d", got)
	}
	if got := imageMultimodalBatchSize(&types.KnowledgeBase{}); got != 1 {
		t.Fatalf("unconfigured KB should default to 1, got %d", got)
	}
	kb := &types.KnowledgeBase{}
	kb.ImageProcessingConfig.BatchSize = 16
	if got := imageMultimodalBatchSize(kb); got != 16 {
		t.Fatalf("configured batch size 16 = %d, want 16", got)
	}
	kb.ImageProcessingConfig.BatchSize = 999
	if got := imageMultimodalBatchSize(kb); got != types.ImageBatchSizeMax {
		t.Fatalf("over-cap batch size clamped to %d, got %d", types.ImageBatchSizeMax, got)
	}
}

func TestImageClassifyMaxEdgeReadsKBConfig(t *testing.T) {
	t.Parallel()
	if got := imageClassifyMaxEdge(nil); got != 0 {
		t.Fatalf("nil KB should disable downscaling, got %d", got)
	}
	if got := imageClassifyMaxEdge(&types.KnowledgeBase{}); got != types.DefaultClassifyMaxEdge {
		t.Fatalf("unconfigured KB should take the default edge, got %d", got)
	}
	kb := &types.KnowledgeBase{}
	kb.ImageProcessingConfig.ClassifyMaxEdge = 320
	if got := imageClassifyMaxEdge(kb); got != 320 {
		t.Fatalf("configured edge = %d, want 320", got)
	}
}

func TestNormalizeImageClass(t *testing.T) {
	t.Parallel()
	cases := map[string]types.ImageClass{
		"decorative":        types.ImageClassDecorative,
		"Decorative logo":   types.ImageClassLogo,
		"logo":              types.ImageClassLogo,
		"brand_logo":        types.ImageClassLogo,
		"decoration":        types.ImageClassDecorative,
		"watermark":         types.ImageClassDecorative,
		"photo":             types.ImageClassPhoto,
		"photograph":        types.ImageClassPhoto,
		"text_screenshot":   types.ImageClassTextScreenshot,
		"screenshot":        types.ImageClassTextScreenshot,
		"screen-shot":       types.ImageClassTextScreenshot,
		"table_image":       types.ImageClassTableImage,
		"table":             types.ImageClassTableImage,
		"chart":             types.ImageClassChart,
		"infographic":       types.ImageClassChart,
		"`chart`":           types.ImageClassChart,
		"**chart**":         types.ImageClassChart,
		"":                  types.ImageClassOther,
		"something_new":     types.ImageClassOther,
		"here is my answer": types.ImageClassOther,
	}
	for raw, want := range cases {
		if got := types.NormalizeImageClass(raw); got != want {
			t.Errorf("NormalizeImageClass(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestBuildBatchImagePrompt(t *testing.T) {
	t.Parallel()
	got := buildBatchImagePrompt(context.Background(), types.VLMConfig{
		DescriptionLanguage: "English",
		CustomInstructions:  "Focus on alarm codes.",
	}, 16)

	for _, want := range []string{
		"16 images",
		"in English",
		"### IMAGE <n>",
		"CLASS: <",
		"DESCRIPTION: <",
		"16 blocks in total",
		"Focus on alarm codes.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("batch prompt missing %q:\n%s", want, got)
		}
	}

	// Every class the parser understands must be offered to the model,
	// otherwise a label the prompt never mentions can never be produced.
	for _, class := range types.ImageClasses {
		if !strings.Contains(got, string(class)) {
			t.Errorf("batch prompt does not mention class %q", class)
		}
	}

	ctx := context.WithValue(context.Background(), types.LanguageContextKey, "ko-KR")
	if got := buildBatchImagePrompt(ctx, types.VLMConfig{}, 2); !strings.Contains(got, "in Korean") {
		t.Errorf("batch prompt should fall back to the context language:\n%s", got)
	}
}

func TestParseBatchImageResponse(t *testing.T) {
	t.Parallel()
	raw := "### IMAGE 1\nCLASS: chart\nDESCRIPTION: A red circle.\n\n" +
		"### IMAGE 2\n\n" +
		"### IMAGE 3\nCLASS: table_image\nDESCRIPTION: A blue square.\n"
	got := parseBatchImageResponse(raw, 3)

	if len(got) != 2 {
		t.Fatalf("expected 2 described images, got %d: %v", len(got), got)
	}
	if got[1].class != types.ImageClassChart || got[1].description != "A red circle." {
		t.Errorf("image 1 = %+v", got[1])
	}
	if got[3].class != types.ImageClassTableImage || got[3].description != "A blue square." {
		t.Errorf("image 3 = %+v", got[3])
	}
	if _, present := got[2]; present {
		t.Errorf("an empty block must not register as a description: %v", got)
	}

	t.Run("tolerates heading level and order", func(t *testing.T) {
		got := parseBatchImageResponse("## IMAGE 2\nCLASS: photo\nDESCRIPTION: second\n"+
			"#### IMAGE 1\nCLASS: chart\nDESCRIPTION: first\n", 2)
		if got[1].description != "first" || got[2].description != "second" {
			t.Fatalf("heading level / order handling broken: %v", got)
		}
	})

	t.Run("ignores out-of-range and duplicate numbers", func(t *testing.T) {
		got := parseBatchImageResponse("### IMAGE 9\nCLASS: photo\nDESCRIPTION: nope\n"+
			"### IMAGE 1\nCLASS: chart\nDESCRIPTION: one\n"+
			"### IMAGE 1\nCLASS: photo\nDESCRIPTION: again\n", 2)
		if len(got) != 1 || got[1].description != "one" {
			t.Fatalf("expected only image 1 = \"one\", got %v", got)
		}
	})

	t.Run("unformatted response yields nothing", func(t *testing.T) {
		if got := parseBatchImageResponse("Sure! Here are the descriptions.", 4); len(got) != 0 {
			t.Fatalf("unformatted response should map nothing, got %v", got)
		}
	})

	t.Run("keeps a description that has no class line", func(t *testing.T) {
		got := parseBatchImageResponse("### IMAGE 1\n- A wiring diagram.\n", 1)
		if got[1].description != "A wiring diagram." {
			t.Fatalf("list marker not stripped: %q", got[1].description)
		}
		if got[1].class != types.ImageClassOther {
			t.Errorf("a missing CLASS line should fall back to other, got %q", got[1].class)
		}
	})

	t.Run("joins multi-line descriptions and reads an emphasised class", func(t *testing.T) {
		got := parseBatchImageResponse("### IMAGE 1\nCLASS: **text_screenshot**\nDESCRIPTION: first line\n"+
			"second line\n", 1)
		if got[1].class != types.ImageClassTextScreenshot {
			t.Errorf("class = %q, want text_screenshot", got[1].class)
		}
		if got[1].description != "first line second line" {
			t.Errorf("description = %q", got[1].description)
		}
	})
}

// ---------------------------------------------------------------------------
// Batch execution
// ---------------------------------------------------------------------------

// TestProcessImageBatchDescribesOnceAndFallsBack pins the behaviours that make
// batching worth having: one describe request covers the whole batch, classes
// come back with the descriptions and are persisted, and an image the batch
// response skipped is still described (and classified) on its own.
func TestProcessImageBatchDescribesOnceAndFallsBack(t *testing.T) {
	t.Parallel()

	fileSvc := &batchFileService{body: []byte("fake-image-bytes")}
	repo := &batchChunkRepo{}
	svc := newBatchTestService(fileSvc, repo)

	refs := []types.ImageBatchRef{
		{Index: 0, URL: "local://img/0.png", ChunkID: "chunk-a"},
		{Index: 1, URL: "local://img/1.png", ChunkID: "chunk-a"},
		{Index: 2, URL: "local://img/2.png", ChunkID: "chunk-b"},
	}

	fake := &batchFakeVLM{}
	fake.reply = func(prompt string, images int) (string, error) {
		switch {
		case strings.Contains(prompt, "OCR assistant"):
			if images != 1 {
				return "", fmt.Errorf("OCR call carried %d images, want 1", images)
			}
			return "OCR-TEXT", nil
		case images > 1:
			if images != 3 {
				return "", fmt.Errorf("batch describe carried %d images, want 3", images)
			}
			// Image 2 is deliberately absent to exercise the fallback.
			return "### IMAGE 1\nCLASS: chart\nDESCRIPTION: first\n\n" +
				"### IMAGE 3\nCLASS: photo\nDESCRIPTION: third\n", nil
		default:
			if images != 1 {
				return "", fmt.Errorf("per-image describe carried %d images, want 1", images)
			}
			return "### IMAGE 1\nCLASS: text_screenshot\nDESCRIPTION: fallback-caption\n", nil
		}
	}

	payload := &types.ImageMultimodalPayload{
		TenantID:                1,
		KnowledgeID:             "k-1",
		KnowledgeBaseID:         "kb-1",
		EnableOCR:               true,
		EnableCaption:           true,
		PostProcessImageEnabled: true,
	}

	outs, err := svc.processImageBatch(
		context.Background(), payload, refs, fake, types.VLMConfig{}, noopSpanTracker{})
	if err != nil {
		t.Fatalf("processImageBatch: %v", err)
	}

	// One batch describe + three OCR + one fallback describe.
	if len(fake.calls) != 5 {
		t.Fatalf("expected 5 VLM calls, got %d: %+v", len(fake.calls), fake.calls)
	}
	batchDescribe, perImageDescribe, ocrCalls := 0, 0, 0
	for _, c := range fake.calls {
		switch {
		case strings.Contains(c.prompt, "OCR assistant"):
			ocrCalls++
		case c.images > 1:
			batchDescribe++
		default:
			perImageDescribe++
		}
	}
	if batchDescribe != 1 {
		t.Errorf("batch describe requests = %d, want 1 (batching must not describe per image)", batchDescribe)
	}
	if perImageDescribe != 1 {
		t.Errorf("per-image describe requests = %d, want 1 (only the uncovered image)", perImageDescribe)
	}
	if ocrCalls != 3 {
		t.Errorf("OCR requests = %d, want 3 (one per image)", ocrCalls)
	}

	// Every image is pre-read exactly once, and the per-image stage reuses those
	// bytes rather than reading the object a second time.
	if len(fileSvc.gets) != 3 {
		t.Errorf("file reads = %d (%v), want 3", len(fileSvc.gets), fileSvc.gets)
	}

	if len(outs) != 3 {
		t.Fatalf("outputs = %d, want 3", len(outs))
	}
	// Each image yields an OCR chunk plus a caption chunk.
	if len(repo.created) != 6 {
		t.Errorf("persisted chunks = %d, want 6", len(repo.created))
	}

	// Classes reach the trace and the persisted rows (two chunks per image).
	wantClasses := map[string]int{"chart": 2, "text_screenshot": 2, "photo": 2}
	if got := persistedClasses(t, repo.created); !equalCounts(got, wantClasses) {
		t.Errorf("persisted classes = %v, want %v", got, wantClasses)
	}
	if got := outs[0]["image_class"]; got != "chart" {
		t.Errorf("image 1 class = %v, want chart", got)
	}
	if got := outs[1]["image_class"]; got != "text_screenshot" {
		t.Errorf("image 2 class = %v, want text_screenshot (from the fallback)", got)
	}
	if got := outs[2]["image_class"]; got != "photo" {
		t.Errorf("image 3 class = %v, want photo", got)
	}

	if got := outs[0]["caption_chars"]; got != len([]rune("first")) {
		t.Errorf("image 1 caption_chars = %v, want %d", got, len([]rune("first")))
	}
	if got := outs[1]["caption_chars"]; got != len([]rune("fallback-caption")) {
		t.Errorf("image 2 caption_chars = %v, want %d", got, len([]rune("fallback-caption")))
	}
}

// TestProcessImageBatchLegacyPipeline pins the upstream behaviour the master
// switch falls back to: no classification, one caption request per image with
// the plain prompt, and OCR for every image regardless of any class policy.
func TestProcessImageBatchLegacyPipeline(t *testing.T) {
	t.Parallel()

	fileSvc := &batchFileService{body: []byte("fake-image-bytes")}
	repo := &batchChunkRepo{}
	svc := newBatchTestService(fileSvc, repo)

	refs := []types.ImageBatchRef{
		{Index: 0, URL: "local://img/0.png"},
		{Index: 1, URL: "local://img/1.png"},
	}

	fake := &batchFakeVLM{}
	fake.reply = func(prompt string, images int) (string, error) {
		if strings.Contains(prompt, "OCR assistant") {
			return "OCR-TEXT", nil
		}
		if images != 1 {
			return "", fmt.Errorf("legacy caption carried %d images, want 1", images)
		}
		if strings.Contains(prompt, "CLASS:") {
			return "", fmt.Errorf("legacy caption must not ask for a class")
		}
		return "plain caption", nil
	}

	payload := &types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		EnableOCR:       true,
		EnableCaption:   true,
		// A table is present but the legacy pipeline must ignore it: even a
		// photo, which the classified pipeline would not OCR, gets its call.
		ClassPolicies: types.DefaultImageClassPolicies(),
	}

	outs, err := svc.processImageBatch(
		context.Background(), payload, refs, fake, types.VLMConfig{}, noopSpanTracker{})
	if err != nil {
		t.Fatalf("processImageBatch: %v", err)
	}

	// Two captions + two OCR calls; no batch describe round at all.
	if len(fake.calls) != 4 {
		t.Fatalf("legacy pipeline made %d VLM calls, want 4 (2 caption + 2 OCR): %+v",
			len(fake.calls), fake.calls)
	}
	for _, c := range fake.calls {
		if c.images != 1 {
			t.Errorf("legacy request carried %d images, want 1 (no batching)", c.images)
		}
	}
	if len(repo.created) != 4 {
		t.Errorf("persisted chunks = %d, want 4 (caption + OCR per image)", len(repo.created))
	}
	if got := outs[0]["ocr_skipped"]; got != nil {
		t.Errorf("legacy OCR must run for every image, got ocr_skipped=%v", got)
	}
	if got := outs[0]["class_policy"]; got != nil {
		t.Errorf("legacy pipeline must not emit a class policy, got %v", got)
	}
}

// TestProcessImageBatchFailsOnTransportError makes sure an API error fails the
// task (so asynq retries it) instead of being silently absorbed into a batch
// with no captions.
func TestProcessImageBatchFailsOnTransportError(t *testing.T) {
	t.Parallel()

	fileSvc := &batchFileService{body: []byte("fake-image-bytes")}
	svc := newBatchTestService(fileSvc, &batchChunkRepo{})

	sentinel := fmt.Errorf("upstream unavailable")
	fake := &batchFakeVLM{}
	fake.reply = func(_ string, images int) (string, error) {
		if images > 1 {
			return "", sentinel
		}
		return "x", nil
	}

	payload := &types.ImageMultimodalPayload{
		TenantID: 1, KnowledgeID: "k-1", KnowledgeBaseID: "kb-1",
		PostProcessImageEnabled: true,
	}
	refs := []types.ImageBatchRef{
		{Index: 0, URL: "local://img/0.png"},
		{Index: 1, URL: "local://img/1.png"},
	}

	_, err := svc.processImageBatch(context.Background(), payload, refs, fake, types.VLMConfig{}, noopSpanTracker{})
	if err == nil {
		t.Fatal("a failed batch describe must fail the task, got nil")
	}
	if !strings.Contains(err.Error(), sentinel.Error()) {
		t.Fatalf("error should wrap the transport failure, got %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("a failed batch describe must not trigger per-image calls, got %d calls", len(fake.calls))
	}
}

// TestProcessImageBatchSkipsUnreadableImage checks that one bad object does not
// cost its siblings: the rest of the batch is still described and persisted.
func TestProcessImageBatchSkipsUnreadableImage(t *testing.T) {
	t.Parallel()

	fileSvc := &batchFileService{
		body:    []byte("fake-image-bytes"),
		failFor: map[string]bool{"local://img/1.png": true},
	}
	repo := &batchChunkRepo{}
	svc := newBatchTestService(fileSvc, repo)

	fake := &batchFakeVLM{}
	fake.reply = func(prompt string, images int) (string, error) {
		switch {
		case strings.Contains(prompt, "OCR assistant"):
			return "OCR-TEXT", nil
		case images > 1:
			if images != 2 {
				return "", fmt.Errorf("batch describe carried %d images, want 2", images)
			}
			return "### IMAGE 1\nCLASS: chart\nDESCRIPTION: alpha\n\n" +
				"### IMAGE 2\nCLASS: photo\nDESCRIPTION: beta\n", nil
		default:
			return "### IMAGE 1\nCLASS: photo\nDESCRIPTION: caption\n", nil
		}
	}

	payload := &types.ImageMultimodalPayload{
		TenantID:                1,
		KnowledgeID:             "k-1",
		KnowledgeBaseID:         "kb-1",
		EnableOCR:               true,
		EnableCaption:           true,
		PostProcessImageEnabled: true,
	}
	refs := []types.ImageBatchRef{
		{Index: 0, URL: "local://img/0.png"},
		{Index: 1, URL: "local://img/1.png"},
		{Index: 2, URL: "local://img/2.png"},
	}

	outs, err := svc.processImageBatch(context.Background(), payload, refs, fake, types.VLMConfig{}, noopSpanTracker{})
	if err != nil {
		t.Fatalf("processImageBatch: %v", err)
	}

	if len(outs) != 3 {
		t.Fatalf("outputs = %d, want 3", len(outs))
	}
	if got := outs[1]["skipped"]; got != "unreadable_image" {
		t.Errorf("middle image should be marked skipped, got %v", got)
	}
	if _, ok := outs[0]["skipped"]; ok {
		t.Errorf("first image should not be skipped: %v", outs[0])
	}
	if _, ok := outs[2]["skipped"]; ok {
		t.Errorf("third image should not be skipped: %v", outs[2])
	}
}

// TestProcessOneImageReusesCallerVerdict pins the zero-overhead path a batched
// caller relies on: bytes and a describe verdict supplied by the caller mean no
// extra read and no extra describe call.
func TestProcessOneImageReusesCallerVerdict(t *testing.T) {
	t.Parallel()

	fileSvc := &batchFileService{body: []byte("should-not-be-read")}
	repo := &batchChunkRepo{}
	svc := newBatchTestService(fileSvc, repo)

	fake := &batchFakeVLM{}
	fake.reply = func(prompt string, _ int) (string, error) {
		if strings.Contains(prompt, "OCR assistant") {
			return "OCR-TEXT", nil
		}
		return "should-not-be-called", nil
	}

	payload := &types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		EnableOCR:       true,
		EnableCaption:   true,
	}
	out, err := svc.processOneImage(context.Background(), payload,
		types.ImageBatchRef{Index: 0, URL: "local://img/0.png", ChunkID: "chunk-a"},
		fake, types.VLMConfig{}, noopSpanTracker{}, imageProcessInput{
			Bytes:        []byte("caller-supplied"),
			Described:    batchImageEntry{class: types.ImageClassChart, description: "batched caption"},
			HasDescribed: true,
			BatchSize:    4,
		})
	if err != nil {
		t.Fatalf("processOneImage: %v", err)
	}

	if len(fileSvc.gets) != 0 {
		t.Errorf("caller-supplied bytes must skip the read, got reads %v", fileSvc.gets)
	}
	for _, c := range fake.calls {
		if !strings.Contains(c.prompt, "OCR assistant") {
			t.Errorf("no describe call expected when HasDescribed is set, got %q", c.prompt)
		}
	}
	if got := out["caption_chars"]; got != len([]rune("batched caption")) {
		t.Errorf("caption_chars = %v, want the caller-supplied caption length", got)
	}
	if got := out["image_class"]; got != "chart" {
		t.Errorf("image_class = %v, want chart", got)
	}
	if got := out["image_bytes"]; got != len("caller-supplied") {
		t.Errorf("image_bytes = %v, want the caller-supplied byte count", got)
	}
	// The class must land on the persisted row, not just the trace.
	if got := persistedClasses(t, repo.created); !equalCounts(got, map[string]int{"chart": 2}) {
		t.Errorf("persisted classes = %v, want one chart image", got)
	}
}

// ---------------------------------------------------------------------------
// Class policy routing (2d / 2e)
// ---------------------------------------------------------------------------

// samePolicyTable compares two class→work tables by value. ImageClassPolicy is two
// bools, so it is directly comparable and needs no deeper walk.
func samePolicyTable(got, want map[string]types.ImageClassPolicy) bool {
	if len(got) != len(want) {
		return false
	}
	for class, policy := range want {
		if got[class] != policy {
			return false
		}
	}
	return true
}

// TestImageClassPoliciesReadsKBConfig pins 2e: the class table comes from the KB
// config and falls back to the built-in one when unset.
func TestImageClassPoliciesReadsKBConfig(t *testing.T) {
	t.Parallel()

	if got := imageClassPolicies(nil); !samePolicyTable(got, types.DefaultImageClassPolicies()) {
		t.Fatalf("nil KB should take the built-in table, got %v", got)
	}
	if got := imageClassPolicies(&types.KnowledgeBase{}); !samePolicyTable(got, types.DefaultImageClassPolicies()) {
		t.Fatalf("unconfigured KB should take the built-in table, got %v", got)
	}

	// The configured rows replace the default rows per class; classes the
	// table does not mention keep the built-in values.
	kb := &types.KnowledgeBase{}
	kb.ImageProcessingConfig.ClassPolicies = map[string]types.ImageClassPolicy{
		string(types.ImageClassChart): {OCR: false, Caption: false},
	}
	got := imageClassPolicies(kb)
	want := types.DefaultImageClassPolicies()
	want[string(types.ImageClassChart)] = types.ImageClassPolicy{OCR: false, Caption: false}
	if !samePolicyTable(got, want) {
		t.Fatalf("configured rows not merged over the defaults: %v", got)
	}
}

// TestProcessOneImageHonoursClassPolicy pins 2d: the class decides which work
// runs. A decorative image must not cost an OCR call even while the whole-task
// switch is on, and its description must survive as the only child chunk.
func TestProcessOneImageHonoursClassPolicy(t *testing.T) {
	t.Parallel()

	fileSvc := &batchFileService{body: []byte("decorative-bytes")}
	repo := &batchChunkRepo{}
	svc := newBatchTestService(fileSvc, repo)

	fake := &batchFakeVLM{}
	fake.reply = func(prompt string, _ int) (string, error) {
		if strings.Contains(prompt, "OCR assistant") {
			return "SHOULD-NOT-BE-ASKED", nil
		}
		return "ignored", nil
	}

	payload := &types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		EnableOCR:       true, // whole-task switch stays on; the class is what vetoes
		EnableCaption:   true,
		ClassPolicies: map[string]types.ImageClassPolicy{
			string(types.ImageClassDecorative): {OCR: false, Caption: true},
		},
	}
	out, err := svc.processOneImage(context.Background(), payload,
		types.ImageBatchRef{Index: 0, URL: "local://img/0.png", ChunkID: "chunk-a"},
		fake, types.VLMConfig{}, noopSpanTracker{}, imageProcessInput{
			Bytes:        []byte("decorative-bytes"),
			Described:    batchImageEntry{class: types.ImageClassDecorative, description: "a plain divider rule"},
			HasDescribed: true,
			BatchSize:    4,
		})
	if err != nil {
		t.Fatalf("processOneImage: %v", err)
	}

	for _, c := range fake.calls {
		if strings.Contains(c.prompt, "OCR assistant") {
			t.Fatalf("a decorative image must not be OCR'd, got call %q", c.prompt)
		}
	}
	if got := out["ocr_skipped"]; got != "class_policy" {
		t.Errorf("ocr_skipped = %v, want class_policy", got)
	}
	if got, ok := out["class_policy"].(types.JSONMap); !ok || got["ocr"] != false || got["caption"] != true {
		t.Errorf("class_policy = %v, want {ocr:false caption:true}", out["class_policy"])
	}
	if len(repo.created) != 1 {
		t.Fatalf("persisted chunks = %d, want 1 (caption only)", len(repo.created))
	}
	if got := repo.created[0].Content; !strings.Contains(got, "a plain divider rule") {
		t.Errorf("persisted caption chunk missing the description: %q", got)
	}
	if got := repo.created[0].ChunkType; got != types.ChunkTypeImageCaption {
		t.Errorf("persisted chunk type = %v, want the caption chunk", got)
	}
}

// TestProcessOneImageConservativeWhenClassUnset pins the other half of 2d: an
// image whose class never parsed must still be OCR'd, because a missed block of
// text costs more than one extra call.
func TestProcessOneImageConservativeWhenClassUnset(t *testing.T) {
	t.Parallel()

	fileSvc := &batchFileService{body: []byte("bytes")}
	repo := &batchChunkRepo{}
	svc := newBatchTestService(fileSvc, repo)

	fake := &batchFakeVLM{}
	fake.reply = func(prompt string, _ int) (string, error) {
		if strings.Contains(prompt, "OCR assistant") {
			return "OCR-TEXT", nil
		}
		return "x", nil
	}

	payload := &types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		EnableOCR:       true,
		EnableCaption:   true,
		ClassPolicies: map[string]types.ImageClassPolicy{
			string(types.ImageClassDecorative): {OCR: false, Caption: true},
		},
	}
	out, err := svc.processOneImage(context.Background(), payload,
		types.ImageBatchRef{Index: 0, URL: "local://img/0.png", ChunkID: "chunk-a"},
		fake, types.VLMConfig{}, noopSpanTracker{}, imageProcessInput{
			Bytes: []byte("bytes"),
			// The describe round returned free text, so no class was parsed.
			Described:    batchImageEntry{description: "unstructured answer"},
			HasDescribed: true,
			BatchSize:    4,
		})
	if err != nil {
		t.Fatalf("processOneImage: %v", err)
	}

	ocrCalls := 0
	for _, c := range fake.calls {
		if strings.Contains(c.prompt, "OCR assistant") {
			ocrCalls++
		}
	}
	if ocrCalls != 1 {
		t.Errorf("OCR calls = %d, want 1 (an unknown class must not skip OCR)", ocrCalls)
	}
	if got, ok := out["ocr_skipped"]; ok {
		t.Errorf("OCR must not be reported skipped, got %v", got)
	}
	if got, ok := out["class_policy"].(types.JSONMap); !ok || got["ocr"] != true {
		t.Errorf("class_policy = %v, want the conservative default", out["class_policy"])
	}
}
