package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/types"
)

type imageCacheStore struct {
	mu   sync.Mutex
	rows map[string]*processingcache.Record
}

func copyImageCacheRecord(r *processingcache.Record) *processingcache.Record {
	if r == nil {
		return nil
	}
	v := *r
	v.Payload = append([]byte(nil), r.Payload...)
	return &v
}

func (s *imageCacheStore) Get(_ context.Context, t uint64, scope, stage, key string) (*processingcache.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.rows[scope+stage+key]; r != nil && r.TenantID == t {
		return copyImageCacheRecord(r), nil
	}
	return nil, processingcache.ErrMiss
}

func (s *imageCacheStore) PutIfAbsent(_ context.Context, r *processingcache.Record) (*processingcache.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := r.ScopeID + r.Stage + r.Key
	if old := s.rows[k]; old != nil {
		return copyImageCacheRecord(old), nil
	}
	s.rows[k] = copyImageCacheRecord(r)
	return copyImageCacheRecord(r), nil
}

func (s *imageCacheStore) Delete(_ context.Context, r *processingcache.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, r.ScopeID+r.Stage+r.Key)
	return nil
}

type imageCacheModel struct {
	vlm.VLM
	identity, reply string
	calls           int
	err             error
}

type imageBoundaryModel struct {
	*imageCacheModel
	rotate           bool
	completeRequired bool
}

func (m *imageBoundaryModel) PredictWithOptions(
	ctx context.Context,
	images [][]byte,
	prompt string,
	opts *vlm.PredictOptions,
) (string, error) {
	m.completeRequired = opts != nil && opts.RequireComplete
	value, err := m.imageCacheModel.PredictWithOptions(ctx, images, prompt, opts)
	if m.rotate {
		m.identity += "changed"
	}
	return value, err
}

func TestImageProcessingCacheRequiresCompletionAndStableIdentity(t *testing.T) {
	store := &imageCacheStore{rows: map[string]*processingcache.Record{}}
	model := &imageBoundaryModel{imageCacheModel: &imageCacheModel{identity: "model", reply: "caption"}, rotate: true}
	r := &runContext{
		processingCache: processingcache.New(store, processingcache.Options{ReadEnabled: true, WriteEnabled: true}),
		payload:         &types.ImageMultimodalPayload{TenantID: 1, KnowledgeID: "doc"},
		model:           model,
		imageBytes:      []byte("pixels"),
	}
	for i := 0; i < 2; i++ {
		value, err := r.cachedImagePrediction(context.Background(), "prompt", "caption", false)
		if err != nil || value != "caption" {
			t.Fatalf("live output lost: %q %v", value, err)
		}
	}
	if !model.completeRequired || len(store.rows) != 0 || model.calls != 2 {
		t.Fatal("completion/identity admission contract failed")
	}
}

func (m *imageCacheModel) ProcessingCacheIdentity() string { return m.identity }
func (m *imageCacheModel) PredictWithOptions(context.Context, [][]byte, string, *vlm.PredictOptions) (string, error) {
	m.calls++
	return m.reply, m.err
}

func TestImageProcessingCacheActualInputs(t *testing.T) {
	m := &imageCacheModel{identity: "model-v1", reply: "a caption"}
	store := &imageCacheStore{rows: map[string]*processingcache.Record{}}
	runtime := processingcache.New(store, processingcache.Options{ReadEnabled: true, WriteEnabled: true})
	makeRun := func(url string) *runContext {
		return &runContext{
			processingCache: runtime,
			payload:         &types.ImageMultimodalPayload{TenantID: 1, KnowledgeID: "doc", ImageURL: url},
			model:           m,
			imageBytes:      []byte("same pixels"),
			out:             types.JSONMap{},
		}
	}
	a := makeRun("old")
	b := makeRun("new")
	for _, r := range []*runContext{a, b} {
		if _, err := r.think(context.Background(), "caption prompt", "caption.thinking_enabled"); err != nil {
			t.Fatal(err)
		}
	}
	if m.calls != 1 {
		t.Fatalf("same bytes/new URL calls=%d", m.calls)
	}
	b.imageBytes = []byte("different pixels")
	if _, err := b.think(context.Background(), "caption prompt", "caption.thinking_enabled"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.think(context.Background(), "changed prompt", "caption.thinking_enabled"); err != nil {
		t.Fatal(err)
	}
	m.identity = "model-v2"
	if _, err := b.think(context.Background(), "changed prompt", "caption.thinking_enabled"); err != nil {
		t.Fatal(err)
	}
	if m.calls != 4 {
		t.Fatalf("invalidation calls=%d", m.calls)
	}
	m.err = errors.New("network")
	for i := 0; i < 2; i++ {
		if _, err := b.think(context.Background(), "failure", "caption.thinking_enabled"); !errors.Is(err, m.err) {
			t.Fatalf("expected model error %v, got %v", m.err, err)
		}
	}
	if m.calls != 6 {
		t.Fatal("cached error")
	}
	m.err = nil
	m.reply = ""
	for i := 0; i < 2; i++ {
		if _, err := b.think(context.Background(), "empty caption", "caption.thinking_enabled"); err != nil {
			t.Fatal(err)
		}
	}
	if m.calls != 8 {
		t.Fatal("cached empty caption")
	}
}

func TestImageProcessingCacheValidationAndCurrentBinding(t *testing.T) {
	store := &imageCacheStore{rows: map[string]*processingcache.Record{}}
	runtime := processingcache.New(store, processingcache.Options{ReadEnabled: true, WriteEnabled: true})
	m := &imageCacheModel{identity: "model", reply: "No text content."}
	r := &runContext{
		processingCache: runtime,
		payload:         &types.ImageMultimodalPayload{TenantID: 1, KnowledgeID: "doc", ImageURL: "old"},
		model:           m,
		imageBytes:      []byte("pixels"),
		out:             types.JSONMap{},
		imageInfo:       &types.ImageInfo{},
	}
	for i := 0; i < 2; i++ {
		if err := runOCRAction(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	if m.calls != 1 || r.out[keyOCRResult] != "skipped_no_text" {
		t.Fatalf("empty OCR calls=%d result=%v", m.calls, r.out)
	}
	m.reply = "recognized text"
	r.imageBytes = []byte("new pixels")
	if err := runOCRAction(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	r.payload.ImageURL = "current"
	r.payload.ChunkID = "new UUID"
	r.imageInfo = &types.ImageInfo{}
	if err := runOCRAction(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if m.calls != 2 || r.imageInfo.OCRText != "recognized text" || r.payload.ChunkID != "new UUID" {
		t.Fatal("failed current binding")
	}
	m.reply = ""
	r.imageBytes = []byte("invalid output")
	for i := 0; i < 2; i++ {
		if err := runOCRAction(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	if m.calls != 4 {
		t.Fatal("cached invalid OCR")
	}
	m.err = vlm.ErrTruncatedCompletion
	for i := 0; i < 2; i++ {
		if _, err := r.think(context.Background(), "truncated", "ocr.thinking_enabled"); !errors.Is(err, m.err) {
			t.Fatalf("expected model error %v, got %v", m.err, err)
		}
	}
	if m.calls != 6 {
		t.Fatal("cached truncated completion")
	}
	m.err = nil
	m.reply = "valid caption"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.think(ctx, "canceled", "caption.thinking_enabled"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	before := m.calls
	if _, err := r.think(context.Background(), "canceled", "caption.thinking_enabled"); err != nil {
		t.Fatal(err)
	}
	if m.calls != before+1 {
		t.Fatal("canceled result persisted")
	}
	for _, row := range store.rows {
		row.Payload = []byte("corrupt")
	}
	before = m.calls
	if _, err := r.think(context.Background(), "canceled", "caption.thinking_enabled"); err != nil {
		t.Fatal(err)
	}
	if m.calls != before+1 {
		t.Fatal("did not repair corrupt payload")
	}
}

type identifiedPreprocessor struct {
	*stubPreprocessor
	identity string
}

func (p *identifiedPreprocessor) ProcessingCacheIdentity() string { return p.identity }
func TestImageProcessingCachePreprocess(t *testing.T) {
	store := &imageCacheStore{rows: map[string]*processingcache.Record{}}
	runtime := processingcache.New(store, processingcache.Options{ReadEnabled: true, WriteEnabled: true})
	p := &identifiedPreprocessor{
		stubPreprocessor: &stubPreprocessor{rectified: testImageCachePNG(t)},
		identity:         "engine-v1",
	}
	r := preprocessRun(types.PreprocessModeAlways, p, types.ImageAttrs{})
	r.processingCache = runtime
	r.payload.TenantID = 1
	r.payload.KnowledgeID = "doc"
	first, _, err := r.cachedImagePreprocess(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first[0] = 0
	second, _, err := r.cachedImagePreprocess(context.Background())
	if err != nil || second[0] == 0 || p.calls != 1 {
		t.Fatalf("clone/hit: %d %v", p.calls, err)
	}
	p.identity = "engine-v2"
	if _, _, err := r.cachedImagePreprocess(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 2 {
		t.Fatal("engine version not keyed")
	}
	p.fallback = true
	p.identity = "decline"
	for i := 0; i < 2; i++ {
		if _, _, err := r.cachedImagePreprocess(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if p.calls != 4 {
		t.Fatal("cached transient fallback")
	}
	p.fallback = false
	p.rectified = []byte("invalid image")
	p.identity = "invalid"
	for i := 0; i < 2; i++ {
		if _, _, err := r.cachedImagePreprocess(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if p.calls != 6 {
		t.Fatal("cached invalid image")
	}
}

func testImageCachePNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImageProcessingCacheObservationAndThinking(t *testing.T) {
	store := &imageCacheStore{rows: map[string]*processingcache.Record{}}
	runtime := processingcache.New(store, processingcache.Options{ReadEnabled: true, WriteEnabled: true})
	m := &imageCacheModel{identity: "model", reply: "CONTAIN.TEXT: block\nCONTAIN.DATA_VISUAL: false"}
	r := &runContext{
		processingCache: runtime,
		payload:         &types.ImageMultimodalPayload{TenantID: 1, KnowledgeID: "doc"},
		model:           m,
		imageBytes:      []byte("pixels"),
		out:             types.JSONMap{},
		params:          map[string]any{},
	}
	for i := 0; i < 2; i++ {
		if _, err := r.think(context.Background(), "observe", "observation.thinking_enabled"); err != nil {
			t.Fatal(err)
		}
	}
	if m.calls != 1 {
		t.Fatalf("observation calls=%d", m.calls)
	}
	r.params["observation.thinking_enabled"] = true
	if _, err := r.think(context.Background(), "observe", "observation.thinking_enabled"); err != nil {
		t.Fatal(err)
	}
	if m.calls != 2 {
		t.Fatal("thinking change not keyed")
	}
	m.reply = "CONTAIN.TEXT: block"
	for i := 0; i < 2; i++ {
		if _, err := r.think(context.Background(), "partial", "observation.thinking_enabled"); err != nil {
			t.Fatal(err)
		}
	}
	if m.calls != 4 {
		t.Fatal("cached partial observation")
	}
	m.identity = ""
	m.reply = "caption"
	for i := 0; i < 2; i++ {
		if _, err := r.think(context.Background(), "identity missing", "caption.thinking_enabled"); err != nil {
			t.Fatal(err)
		}
	}
	if m.calls != 6 {
		t.Fatal("missing identity used cache")
	}
}

type changingIdentityPreprocessor struct {
	identity string
	calls    int
	image    []byte
}

func (p *changingIdentityPreprocessor) ProcessingCacheIdentity() string { return p.identity }
func (p *changingIdentityPreprocessor) PreprocessImage(context.Context, []byte) ([]byte, bool, error) {
	p.calls++
	p.identity = "reconnected-engine"
	return p.image, false, nil
}

func TestImageProcessingCachePreprocessIdentityChangesDuringCall(t *testing.T) {
	store := &imageCacheStore{rows: map[string]*processingcache.Record{}}
	p := &changingIdentityPreprocessor{identity: "old-engine", image: testImageCachePNG(t)}
	r := preprocessRun(types.PreprocessModeAlways, p, types.ImageAttrs{})
	r.processingCache = processingcache.New(store, processingcache.Options{ReadEnabled: true, WriteEnabled: true})
	r.payload.TenantID = 1
	r.payload.KnowledgeID = "doc"
	got, _, err := r.cachedImagePreprocess(context.Background())
	if err != nil || len(got) == 0 {
		t.Fatalf("original result lost: %v", err)
	}
	if len(store.rows) != 0 {
		t.Fatal("persisted reconnected result under old identity")
	}
	if _, _, err := r.cachedImagePreprocess(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 2 || len(store.rows) != 1 {
		t.Fatal("current endpoint did not compute/store independently")
	}
}
