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
// Test doubles for the attribute-observed multimodal path.
// ---------------------------------------------------------------------------

// attrsVLMCall records one Predict invocation so a test can assert which
// prompt a step used and how many images it carried.
type attrsVLMCall struct {
	prompt string
	images int
}

type attrsFakeVLM struct {
	calls []attrsVLMCall
	reply func(prompt string, images int) (string, error)
}

func (f *attrsFakeVLM) Predict(_ context.Context, imgBytes [][]byte, prompt string) (string, error) {
	f.calls = append(f.calls, attrsVLMCall{prompt: prompt, images: len(imgBytes)})
	if f.reply == nil {
		return "", nil
	}
	return f.reply(prompt, len(imgBytes))
}

func (f *attrsFakeVLM) GetModelName() string { return "attrs-fake-vlm" }
func (f *attrsFakeVLM) GetModelID() string   { return "attrs-fake-vlm-id" }

var _ vlm.VLM = (*attrsFakeVLM)(nil)

// attrsFileService serves every read from memory so readImageBytes resolves
// a local:// reference without touching disk or the network.
type attrsFileService struct {
	interfaces.FileService
	body []byte
	err  error
	gets []string
}

func (s *attrsFileService) GetFile(_ context.Context, filePath string) (io.ReadCloser, error) {
	s.gets = append(s.gets, filePath)
	if s.err != nil {
		return nil, s.err
	}
	return io.NopCloser(bytes.NewReader(s.body)), nil
}

// attrsTenantRepo reports "no tenant" so resolveFileServiceForPayload falls
// back to the service's default FileService.
type attrsTenantRepo struct {
	interfaces.TenantRepository
}

func (r *attrsTenantRepo) GetTenantByID(_ context.Context, _ uint64) (*types.Tenant, error) {
	return nil, nil
}

type attrsChunkRepo struct {
	interfaces.ChunkRepository
	created []*types.Chunk
}

func (r *attrsChunkRepo) CreateChunks(_ context.Context, chunks []*types.Chunk) error {
	r.created = append(r.created, chunks...)
	return nil
}

type attrsChunkService struct {
	interfaces.ChunkService
	repo *attrsChunkRepo
}

func (s *attrsChunkService) GetRepository() interfaces.ChunkRepository { return s.repo }

// newAttrsTestService wires a service whose file reads come from memory and
// whose knowledge base lookup returns nil, which makes indexChunks skip vector
// work (it bails out on a nil KB). That keeps the test focused on the image
// pipeline rather than on the retrieval engine.
func newAttrsTestService(fileSvc interfaces.FileService, repo *attrsChunkRepo) *ImageMultimodalService {
	return &ImageMultimodalService{
		chunkService: &attrsChunkService{repo: repo},
		kbService:    &orphanKBService{},
		tenantRepo:   &attrsTenantRepo{},
		fileSvc:      fileSvc,
	}
}

// runProcessImage drives one image through the pipeline and returns the trace
// map the per-image span is closed with — the same map Handle owns.
func runProcessImage(
	t *testing.T,
	svc *ImageMultimodalService,
	payload *types.ImageMultimodalPayload,
	fake *attrsFakeVLM,
) types.JSONMap {
	t.Helper()
	out := types.JSONMap{}
	err := svc.processImage(context.Background(), payload, fake, types.VLMConfig{}, noopSpanTracker{}, out)
	if err != nil {
		t.Fatalf("processImage: %v", err)
	}
	return out
}

// persistedAttrCounts counts a single observed attribute value written onto
// each persisted chunk — how the "the observation is stored, not just logged"
// contract is checked.
func persistedAttrCounts(t *testing.T, chunks []*types.Chunk, key string) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, chunk := range chunks {
		var infos []types.ImageInfo
		if err := json.Unmarshal([]byte(chunk.ImageInfo), &infos); err != nil {
			t.Fatalf("decode chunk image_info: %v", err)
		}
		for _, info := range infos {
			if info.Attrs.Attrs == nil {
				counts["<unset>"]++
				continue
			}
			if v, ok := info.Attrs.Attrs[key]; ok {
				counts[fmt.Sprint(v)]++
			} else {
				counts["<unset>"]++
			}
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

// countCallsWith returns how many recorded calls used a prompt containing
// needle, which is how a test tells the observe round from the OCR round.
func countCallsWith(calls []attrsVLMCall, needle string) int {
	n := 0
	for _, c := range calls {
		if strings.Contains(c.prompt, needle) {
			n++
		}
	}
	return n
}

// observedAttrs builds a complete attribute table — the shape the parser
// produces from an observe round the model answered in full.
func observedAttrs(text string, dataVisual bool) types.ImageAttrs {
	return types.ImageAttrs{Attrs: map[string]any{
		"contain.text":        text,
		"contain.data_visual": dataVisual,
	}}
}

// ---------------------------------------------------------------------------
// Prompt / parser / decision unit tests
// ---------------------------------------------------------------------------

func TestBuildImageAttrsPrompt(t *testing.T) {
	t.Parallel()
	got := buildImageAttrsPrompt(context.Background(), types.VLMConfig{
		DescriptionLanguage: "English",
		CustomInstructions:  "Focus on alarm codes.",
	})

	for _, want := range []string{
		"in English",
		"contain.text:",
		"contain.data_visual:",
		"DESCRIPTION:",
		"Focus on alarm codes.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("attrs prompt missing %q:\n%s", want, got)
		}
	}

	// Every attribute the parser understands must be offered to the model,
	// otherwise a label the prompt never mentions can never be produced.
	for _, spec := range types.ImageAttrRegistry {
		if !strings.Contains(got, spec.Name) {
			t.Errorf("attrs prompt does not mention attribute %q", spec.Name)
		}
	}

	ctx := context.WithValue(context.Background(), types.LanguageContextKey, "ko-KR")
	if got := buildImageAttrsPrompt(ctx, types.VLMConfig{}); !strings.Contains(got, "in Korean") {
		t.Errorf("attrs prompt should fall back to the context language:\n%s", got)
	}
}

// TestDecideOCR pins the pure OCR decision: a block of text, a data visual, or
// an attribute the observation did not answer for all trigger OCR; sparse (a
// logo) and an explicit absence do not. The decision is a function of observed
// attributes and the policy only — no model, no repo.
func TestDecideOCR(t *testing.T) {
	t.Parallel()
	def := types.DefaultImageActions()

	cases := []struct {
		name  string
		attrs types.ImageAttrs
		want  bool
	}{
		{"block of text", observedAttrs("block", false), true},
		{"sparse logo", observedAttrs("sparse", false), false},
		{"data visual", observedAttrs("sparse", true), true},
		{"none", observedAttrs("none", false), false},
		// Incomplete observations: the parser leaves an unanswered attribute
		// absent, so the conservative clause has to carry the decision — these
		// must never be read as a confident negative.
		{
			"unobserved text is conservative",
			types.ImageAttrs{Attrs: map[string]any{"contain.data_visual": false}},
			true,
		},
		{
			"unobserved data visual is conservative",
			types.ImageAttrs{Attrs: map[string]any{"contain.text": "none"}},
			true,
		},
		{"empty observation is conservative", types.ImageAttrs{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecideOCR(tc.attrs, def); got != tc.want {
				t.Errorf("DecideOCR = %v, want %v", got, tc.want)
			}
		})
	}

	// A policy that does not OCR on an unobserved attribute flips the
	// conservative path for both of the incomplete cases above.
	strict := types.ImageActionsConfig{
		OCR: types.ImageOCRAction{
			On: []types.ImageAttrCondition{
				{Prop: "contain.text", Is: "block"},
				{Prop: "contain.data_visual", Is: "true"},
			},
			OnUnobserved: false,
		},
	}
	for _, attrs := range []types.ImageAttrs{
		{Attrs: map[string]any{"contain.data_visual": false}},
		{Attrs: map[string]any{"contain.text": "none"}},
		{},
	} {
		if DecideOCR(attrs, strict) {
			t.Errorf("with OnUnobserved=false an incomplete observation (%v) must not trigger OCR", attrs.Attrs)
		}
	}
}

// ---------------------------------------------------------------------------
// Pipeline execution
// ---------------------------------------------------------------------------

// TestProcessImageObservesAndDescribes pins the attribute pipeline: one observe
// round returns both attributes and a description, the observation is persisted
// with the image, and an attribute the policy keeps still gets its OCR round.
func TestProcessImageObservesAndDescribes(t *testing.T) {
	t.Parallel()

	fileSvc := &attrsFileService{body: []byte("table-bytes")}
	repo := &attrsChunkRepo{}
	svc := newAttrsTestService(fileSvc, repo)

	fake := &attrsFakeVLM{}
	fake.reply = func(prompt string, _ int) (string, error) {
		if strings.Contains(prompt, "OCR assistant") {
			return "Q1 Q2 Q3", nil
		}
		return "contain.text: block\ncontain.data_visual: false\nDESCRIPTION: A pricing table.", nil
	}

	payload := &types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		ImageURL:        "local://img/0.png",
		ChunkID:         "chunk-a",
		ImagePipelineParams: map[string]any{
			smartFieldKeyAllowOCR:       true,
			smartFieldKeyCaptureCaption: true,
		},
		ImageAttrsEnabled: true,
		ImageActions:      types.DefaultImageActions(),
	}
	out := runProcessImage(t, svc, payload, fake)

	attrs, ok := out["image_attrs"].(map[string]any)
	if !ok {
		t.Fatalf("image_attrs = %v, want the observed attribute map", out["image_attrs"])
	}
	if attrs["contain.text"] != "block" {
		t.Errorf("contain.text = %v, want block", attrs["contain.text"])
	}
	if got, ok := out["attr_policy"].(types.JSONMap); !ok || got["ocr"] != true {
		t.Errorf("attr_policy = %v, want {ocr:true}", out["attr_policy"])
	}
	if _, skipped := out["ocr_skipped"]; skipped {
		t.Errorf("a block of text must be OCR'd, got %v", out["ocr_skipped"])
	}
	// One observe call (never the legacy caption prompt) plus one OCR call.
	if n := countCallsWith(fake.calls, "OCR assistant"); n != 1 {
		t.Errorf("OCR calls = %d, want 1", n)
	}
	if n := countCallsWith(fake.calls, "contain.text:"); n != 1 {
		t.Errorf("observe calls = %d, want 1", n)
	}
	if len(repo.created) != 2 {
		t.Fatalf("persisted chunks = %d, want OCR + caption", len(repo.created))
	}
	if got := persistedAttrCounts(t, repo.created, "contain.text"); !equalCounts(got, map[string]int{"block": 2}) {
		t.Errorf("persisted contain.text = %v, want block on both chunks", got)
	}
}

// TestProcessImageMarksObservationFailed pins the defensive signal: when the
// model answers in prose and ignores the attribute protocol (e.g. a user
// custom instruction derails the format), the pipeline must flag
// observation_failed in the trace instead of passing an empty attribute table
// off as a confident read. Nothing is invented to fill the table, so the OCR
// policy falls back to its conservative OnUnobserved clause (OCR on) and the
// image is not lost — only the observation is recorded as having failed.
func TestProcessImageMarksObservationFailed(t *testing.T) {
	t.Parallel()

	fileSvc := &attrsFileService{body: []byte("bytes")}
	repo := &attrsChunkRepo{}
	svc := newAttrsTestService(fileSvc, repo)

	fake := &attrsFakeVLM{}
	fake.reply = func(prompt string, _ int) (string, error) {
		if strings.Contains(prompt, "OCR assistant") {
			return "some text", nil
		}
		// Prose only — no contain.text: / contain.data_visual: lines.
		return "这是一张示意图，展示了系统整体的数据流向与模块划分。", nil
	}

	payload := &types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		ImageURL:        "local://img/0.png",
		ChunkID:         "chunk-a",
		ImagePipelineParams: map[string]any{
			smartFieldKeyAllowOCR:       true,
			smartFieldKeyCaptureCaption: true,
		},
		ImageAttrsEnabled: true,
		ImageActions:      types.DefaultImageActions(),
	}
	out := runProcessImage(t, svc, payload, fake)

	if out["observation_failed"] != true {
		t.Errorf("observation_failed = %v, want true for a prose-only answer", out["observation_failed"])
	}
	// No attribute is invented for the unanswered questions: the table stays
	// empty, and the conservative clause is what keeps OCR running.
	if attrs, ok := out["image_attrs"].(map[string]any); !ok || len(attrs) != 0 {
		t.Errorf("image_attrs = %v, want an empty table for a prose-only answer", out["image_attrs"])
	}
	if n := countCallsWith(fake.calls, "OCR assistant"); n != 1 {
		t.Errorf("OCR calls = %d, want 1 (the conservative clause must still extract)", n)
	}
	// And the description was kept, so caption_missing must NOT be set.
	if _, missing := out["caption_missing"]; missing {
		t.Errorf("caption_missing should be unset for a prose answer that has a description")
	}
}

// TestProcessImageSkipsOCRForQuietAttr is the payoff of the feature: an image
// whose observed text is absent (none) must not cost an OCR call, and its
// description must survive as the only child chunk.
func TestProcessImageSkipsOCRForQuietAttr(t *testing.T) {
	t.Parallel()

	fileSvc := &attrsFileService{body: []byte("decorative-bytes")}
	repo := &attrsChunkRepo{}
	svc := newAttrsTestService(fileSvc, repo)

	fake := &attrsFakeVLM{}
	fake.reply = func(prompt string, _ int) (string, error) {
		if strings.Contains(prompt, "OCR assistant") {
			return "SHOULD-NOT-BE-ASKED", nil
		}
		return "contain.text: none\ncontain.data_visual: false\nDESCRIPTION: A plain divider rule.", nil
	}

	payload := &types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		ImageURL:        "local://img/0.png",
		ChunkID:         "chunk-a",
		// The ceiling stays on; the observation is what vetoes.
		ImagePipelineParams: map[string]any{
			smartFieldKeyAllowOCR:       true,
			smartFieldKeyCaptureCaption: true,
		},
		ImageAttrsEnabled: true,
		ImageActions:      types.DefaultImageActions(),
	}
	out := runProcessImage(t, svc, payload, fake)

	if n := countCallsWith(fake.calls, "OCR assistant"); n != 0 {
		t.Fatalf("a quiet image must not be OCR'd, got %d OCR call(s)", n)
	}
	if got := out["ocr_skipped"]; got != "attr_policy" {
		t.Errorf("ocr_skipped = %v, want attr_policy", got)
	}
	if got, ok := out["attr_policy"].(types.JSONMap); !ok || got["ocr"] != false {
		t.Errorf("attr_policy = %v, want {ocr:false}", out["attr_policy"])
	}
	if len(repo.created) != 1 {
		t.Fatalf("persisted chunks = %d, want 1 (caption only)", len(repo.created))
	}
	if got := repo.created[0].ChunkType; got != types.ChunkTypeImageCaption {
		t.Errorf("persisted chunk type = %v, want the caption chunk", got)
	}
	if got := repo.created[0].Content; !strings.Contains(got, "A plain divider rule.") {
		t.Errorf("persisted caption chunk missing the description: %q", got)
	}
}

// TestProcessImageKeepsAttrsButDropsCaptionWhenCaptionsAreOff pins how the two
// whole-task switches compose in the attribute pipeline: the observe round is
// not optional, because the observation is what the OCR policy reads — but when
// captions are turned off the description that round produced must not be
// stored, or the switch would be a no-op on this path.
func TestProcessImageKeepsAttrsButDropsCaptionWhenCaptionsAreOff(t *testing.T) {
	t.Parallel()

	fileSvc := &attrsFileService{body: []byte("chart-bytes")}
	repo := &attrsChunkRepo{}
	svc := newAttrsTestService(fileSvc, repo)

	fake := &attrsFakeVLM{}
	fake.reply = func(prompt string, _ int) (string, error) {
		if strings.Contains(prompt, "OCR assistant") {
			return "OCR-TEXT", nil
		}
		return "contain.text: block\ncontain.data_visual: true\nDESCRIPTION: A bar chart of quarterly revenue.", nil
	}

	payload := &types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		ImageURL:        "local://img/0.png",
		ChunkID:         "chunk-a",
		ImagePipelineParams: map[string]any{
			smartFieldKeyAllowOCR:       true,
			smartFieldKeyCaptureCaption: false,
		},
		ImageAttrsEnabled: true,
		ImageActions:      types.DefaultImageActions(),
	}
	out := runProcessImage(t, svc, payload, fake)

	attrs, ok := out["image_attrs"].(map[string]any)
	if !ok {
		t.Fatalf("image_attrs = %v, want the observed attribute map", out["image_attrs"])
	}
	if attrs["contain.data_visual"] != true {
		t.Errorf("contain.data_visual = %v, want true (needed even without captions)",
			attrs["contain.data_visual"])
	}
	if got := out["caption_skipped"]; got != "disabled" {
		t.Errorf("caption_skipped = %v, want disabled", got)
	}
	if got, present := out["caption_preview"]; present {
		t.Errorf("a caption must not be recorded when captions are off, got %v", got)
	}
	if n := countCallsWith(fake.calls, "OCR assistant"); n != 1 {
		t.Errorf("OCR calls = %d, want 1 (data visual keeps OCR)", n)
	}
	if len(repo.created) != 1 {
		t.Fatalf("persisted chunks = %d, want the OCR chunk alone", len(repo.created))
	}
	if got := repo.created[0].ChunkType; got != types.ChunkTypeImageOCR {
		t.Errorf("persisted chunk type = %v, want the OCR chunk", got)
	}
}

// TestProcessImageProseAnswerIsStillCaptionedAndOCRed pins the safety half of
// the observation contract. A model that answers the question in prose, without
// the attribute protocol, has still described the image: the prose becomes the
// caption, no attribute is claimed, and the conservative clause decides OCR the
// safe way. A missed block of text costs more than one extra call.
func TestProcessImageProseAnswerIsStillCaptionedAndOCRed(t *testing.T) {
	t.Parallel()

	fileSvc := &attrsFileService{body: []byte("bytes")}
	repo := &attrsChunkRepo{}
	svc := newAttrsTestService(fileSvc, repo)

	fake := &attrsFakeVLM{}
	fake.reply = func(prompt string, _ int) (string, error) {
		if strings.Contains(prompt, "OCR assistant") {
			return "OCR-TEXT", nil
		}
		// The model ignored the label protocol.
		return "Sure! Here is a picture of a pump.", nil
	}

	payload := &types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		ImageURL:        "local://img/0.png",
		ChunkID:         "chunk-a",
		ImagePipelineParams: map[string]any{
			smartFieldKeyAllowOCR:       true,
			smartFieldKeyCaptureCaption: true,
		},
		ImageAttrsEnabled: true,
		ImageActions:      types.DefaultImageActions(),
	}
	out := runProcessImage(t, svc, payload, fake)

	attrs, _ := out["image_attrs"].(map[string]any)
	if len(attrs) != 0 {
		t.Errorf("image_attrs = %v, want nothing claimed for a prose answer", attrs)
	}
	if n := countCallsWith(fake.calls, "OCR assistant"); n != 1 {
		t.Errorf("OCR calls = %d, want 1 (the conservative clause must not skip OCR)", n)
	}
	if _, skipped := out["ocr_skipped"]; skipped {
		t.Errorf("OCR must not be reported skipped, got %v", out["ocr_skipped"])
	}
	if got, ok := out["attr_policy"].(types.JSONMap); !ok || got["ocr"] != true {
		t.Errorf("attr_policy = %v, want the conservative decision", out["attr_policy"])
	}
	if _, missing := out["caption_missing"]; missing {
		t.Error("prose is a description: caption_missing must not be reported")
	}
	if len(repo.created) != 2 {
		t.Fatalf("persisted chunks = %d, want OCR + caption", len(repo.created))
	}
}

// TestProcessImageLabelOnlyAnswerKeepsAttrsWithoutCaption covers the one reply
// the parser reports as unusable: attributes and nothing else. The attributes
// are real information and are kept — they are what the OCR policy reads — but
// the raw reply must never become the caption, or "contain.text: block" ends up
// in the index as the image's description.
func TestProcessImageLabelOnlyAnswerKeepsAttrsWithoutCaption(t *testing.T) {
	t.Parallel()

	fileSvc := &attrsFileService{body: []byte("bytes")}
	repo := &attrsChunkRepo{}
	svc := newAttrsTestService(fileSvc, repo)

	fake := &attrsFakeVLM{}
	fake.reply = func(prompt string, _ int) (string, error) {
		if strings.Contains(prompt, "OCR assistant") {
			return "OCR-TEXT", nil
		}
		// Truncated answer: the attributes arrived, the description never did.
		return "contain.text: block\ncontain.data_visual: true", nil
	}

	payload := &types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		ImageURL:        "local://img/0.png",
		ChunkID:         "chunk-a",
		ImagePipelineParams: map[string]any{
			smartFieldKeyAllowOCR:       true,
			smartFieldKeyCaptureCaption: true,
		},
		ImageAttrsEnabled: true,
		ImageActions:      types.DefaultImageActions(),
	}
	out := runProcessImage(t, svc, payload, fake)

	if got := out["caption_missing"]; got != true {
		t.Errorf("caption_missing = %v, want true", got)
	}
	if n := countCallsWith(fake.calls, "OCR assistant"); n != 1 {
		t.Errorf("OCR calls = %d, want 1 (block + data visual keep OCR)", n)
	}
	if len(repo.created) != 1 {
		t.Fatalf("persisted chunks = %d, want OCR only", len(repo.created))
	}
	if got := repo.created[0].ChunkType; got != types.ChunkTypeImageOCR {
		t.Errorf("persisted chunk type = %v, want the OCR chunk", got)
	}
	if got := repo.created[0].Content; strings.Contains(got, "contain.text") {
		t.Errorf("protocol line leaked into the index: %q", got)
	}
}

// TestProcessImageCaptionOCRPipeline pins the upstream behaviour the master
// switch falls back to (PipelineCaptionOCR): no attribute observation, the
// plain caption prompt, and OCR for every image regardless of any action policy.
func TestProcessImageCaptionOCRPipeline(t *testing.T) {
	t.Parallel()

	fileSvc := &attrsFileService{body: []byte("bytes")}
	repo := &attrsChunkRepo{}
	svc := newAttrsTestService(fileSvc, repo)

	fake := &attrsFakeVLM{}
	fake.reply = func(prompt string, _ int) (string, error) {
		if strings.Contains(prompt, "OCR assistant") {
			return "PLAIN-OCR", nil
		}
		return "A pump on a bench.", nil
	}

	payload := &types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		ImageURL:        "local://img/0.png",
		ChunkID:         "chunk-a",
		ImagePipelineParams: map[string]any{
			captionFieldKeyEnableCaption: true,
			captionFieldKeyEnableOCR:     true,
		},
		ImageAttrsEnabled: false,
		// An action table is present but must be ignored: caption+OCR mode has
		// no observation to feed it.
		ImageActions: types.DefaultImageActions(),
	}
	out := runProcessImage(t, svc, payload, fake)

	if n := countCallsWith(fake.calls, "contain.text:"); n != 0 {
		t.Errorf("caption+OCR mode must not observe, got %d observe call(s)", n)
	}
	if n := countCallsWith(fake.calls, "OCR assistant"); n != 1 {
		t.Errorf("OCR calls = %d, want 1 (caption+OCR mode OCRs every image)", n)
	}
	if _, present := out["image_attrs"]; present {
		t.Errorf("caption+OCR mode must not record attributes, got %v", out["image_attrs"])
	}
	if _, present := out["attr_policy"]; present {
		t.Errorf("caption+OCR mode must not evaluate an action policy, got %v", out["attr_policy"])
	}
	if len(repo.created) != 2 {
		t.Fatalf("persisted chunks = %d, want OCR + caption", len(repo.created))
	}
}

// TestProcessImageSkipsUnreadableImage checks that an unreadable object is
// reported and counted rather than failing the task.
func TestProcessImageSkipsUnreadableImage(t *testing.T) {
	t.Parallel()

	fileSvc := &attrsFileService{err: fmt.Errorf("object is gone")}
	repo := &attrsChunkRepo{}
	svc := newAttrsTestService(fileSvc, repo)

	fake := &attrsFakeVLM{}
	payload := &types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		ImageURL:        "local://img/0.png",
		ChunkID:         "chunk-a",
		ImagePipelineParams: map[string]any{
			smartFieldKeyAllowOCR:       true,
			smartFieldKeyCaptureCaption: true,
		},
		ImageAttrsEnabled: true,
		ImageActions:      types.DefaultImageActions(),
	}

	out := runProcessImage(t, svc, payload, fake)
	if got := out["skipped"]; got != "unreadable_image" {
		t.Errorf("skipped = %v, want unreadable_image", got)
	}
	if len(fake.calls) != 0 {
		t.Errorf("no VLM call should be made for an unreadable image, got %d", len(fake.calls))
	}
	if len(repo.created) != 0 {
		t.Errorf("no chunk should be persisted, got %d", len(repo.created))
	}
}
