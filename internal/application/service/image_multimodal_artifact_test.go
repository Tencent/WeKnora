package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeArtifactVLM struct {
	mu    sync.Mutex
	calls int
	out   []string
	err   error
}

func (v *fakeArtifactVLM) Predict(context.Context, [][]byte, string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls++
	if v.err != nil {
		return "", v.err
	}
	if len(v.out) >= v.calls {
		return v.out[v.calls-1], nil
	}
	return fmt.Sprintf("caption-%d", v.calls), nil
}

func (v *fakeArtifactVLM) GetModelName() string { return "fake-vlm" }
func (v *fakeArtifactVLM) GetModelID() string   { return "fake-vlm-id" }

type multimodalArtifactStore struct {
	mu      sync.Mutex
	records map[string]*artifact.Record
	puts    int
}

func newMultimodalArtifactStore() *multimodalArtifactStore {
	return &multimodalArtifactStore{records: map[string]*artifact.Record{}}
}

func (s *multimodalArtifactStore) PutIfAbsent(_ context.Context, record *artifact.Record) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.key(record.TenantID, record.Stage, record.KeyVersion, record.ArtifactKey)
	if _, ok := s.records[key]; ok {
		return false, nil
	}
	copy := *record
	copy.Payload = append([]byte(nil), record.Payload...)
	s.records[key] = &copy
	s.puts++
	return true, nil
}

func (s *multimodalArtifactStore) Get(_ context.Context, tenantID uint64, stage string, keyVersion int, artifactKey string) (*artifact.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.records[s.key(tenantID, stage, keyVersion, artifactKey)]
	if record == nil {
		return nil, artifact.ErrCacheMiss
	}
	copy := *record
	copy.Payload = append([]byte(nil), record.Payload...)
	return &copy, nil
}

func (s *multimodalArtifactStore) DeleteObservedChecksum(_ context.Context, tenantID uint64, id string, payloadChecksum string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, record := range s.records {
		if record.TenantID == tenantID && record.ID == id && record.PayloadChecksum == payloadChecksum {
			delete(s.records, key)
			return true, nil
		}
	}
	return false, nil
}

func (s *multimodalArtifactStore) key(tenantID uint64, stage string, keyVersion int, artifactKey string) string {
	return fmt.Sprintf("%d:%s:%d:%s", tenantID, stage, keyVersion, artifactKey)
}

func TestPredictVLMWithArtifactReusesCaption(t *testing.T) {
	ctx := context.Background()
	store := newMultimodalArtifactStore()
	svc := &ImageMultimodalService{
		artifactRuntime: artifact.NewRuntime(store, artifact.RuntimeOptions{ReadEnabled: true, WriteEnabled: true}),
		config:          &config.Config{ArtifactCache: &config.ArtifactCacheConfig{Stages: map[string]bool{"vlm_caption": true}}},
	}
	model := &fakeArtifactVLM{out: []string{"first caption", "second caption"}}
	payload := types.ImageMultimodalPayload{TenantID: 42}
	cfg := types.VLMConfig{ModelID: "vlm-1", DescriptionLanguage: "English"}

	got, meta, err := svc.predictVLMWithArtifact(ctx, model, payload, cfg, []byte("image"), "prompt", "vlm_caption")
	if err != nil {
		t.Fatal(err)
	}
	if got != "first caption" || meta.Outcome != artifact.OutcomeComputed {
		t.Fatalf("first call got text=%q outcome=%s", got, meta.Outcome)
	}
	got, meta, err = svc.predictVLMWithArtifact(ctx, model, payload, cfg, []byte("image"), "prompt", "vlm_caption")
	if err != nil {
		t.Fatal(err)
	}
	if got != "first caption" || meta.Outcome != artifact.OutcomeHit {
		t.Fatalf("second call got text=%q outcome=%s", got, meta.Outcome)
	}
	if model.calls != 1 {
		t.Fatalf("provider calls=%d, want 1", model.calls)
	}
}

func TestPredictVLMWithArtifactInvalidatesOnPromptAndModel(t *testing.T) {
	ctx := context.Background()
	store := newMultimodalArtifactStore()
	svc := &ImageMultimodalService{
		artifactRuntime: artifact.NewRuntime(store, artifact.RuntimeOptions{ReadEnabled: true, WriteEnabled: true}),
		config:          &config.Config{ArtifactCache: &config.ArtifactCacheConfig{Stages: map[string]bool{"vlm_caption": true}}},
	}
	model := &fakeArtifactVLM{out: []string{
		"first caption",
		"prompt changed caption",
		"model changed caption",
	}}
	payload := types.ImageMultimodalPayload{TenantID: 42}
	baseCfg := types.VLMConfig{ModelID: "vlm-1", DescriptionLanguage: "English"}

	if _, _, err := svc.predictVLMWithArtifact(ctx, model, payload, baseCfg, []byte("image"), "describe labels", "vlm_caption"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.predictVLMWithArtifact(ctx, model, payload, baseCfg, []byte("image"), "describe layout", "vlm_caption"); err != nil {
		t.Fatal(err)
	}
	modelChangedCfg := baseCfg
	modelChangedCfg.ModelID = "vlm-2"
	if _, _, err := svc.predictVLMWithArtifact(ctx, model, payload, modelChangedCfg, []byte("image"), "describe labels", "vlm_caption"); err != nil {
		t.Fatal(err)
	}

	if model.calls != 3 {
		t.Fatalf("provider calls=%d, want one call per prompt/model cache key", model.calls)
	}
}

func TestPredictVLMWithArtifactDoesNotCacheEmptyOCR(t *testing.T) {
	ctx := context.Background()
	store := newMultimodalArtifactStore()
	svc := &ImageMultimodalService{
		artifactRuntime: artifact.NewRuntime(store, artifact.RuntimeOptions{ReadEnabled: true, WriteEnabled: true}),
		config:          &config.Config{ArtifactCache: &config.ArtifactCacheConfig{Stages: map[string]bool{"vlm_ocr": true}}},
	}
	model := &fakeArtifactVLM{out: []string{"No text content.", "real text"}}
	payload := types.ImageMultimodalPayload{TenantID: 42}
	cfg := types.VLMConfig{ModelID: "vlm-1"}

	got, _, err := svc.predictVLMWithArtifact(ctx, model, payload, cfg, []byte("image"), "prompt", "vlm_ocr")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" || store.puts != 0 {
		t.Fatalf("empty OCR got %q puts=%d, want no cached payload", got, store.puts)
	}
	got, meta, err := svc.predictVLMWithArtifact(ctx, model, payload, cfg, []byte("image"), "prompt", "vlm_ocr")
	if err != nil {
		t.Fatal(err)
	}
	if got != "real text" || meta.Outcome != artifact.OutcomeComputed || store.puts != 1 {
		t.Fatalf("second OCR got text=%q outcome=%s puts=%d", got, meta.Outcome, store.puts)
	}
}

func TestPredictVLMWithArtifactBypassesWhenStageDisabled(t *testing.T) {
	ctx := context.Background()
	store := newMultimodalArtifactStore()
	svc := &ImageMultimodalService{
		artifactRuntime: artifact.NewRuntime(store, artifact.RuntimeOptions{ReadEnabled: true, WriteEnabled: true}),
		config:          &config.Config{ArtifactCache: &config.ArtifactCacheConfig{Stages: map[string]bool{"vlm_caption": false}}},
	}
	model := &fakeArtifactVLM{out: []string{"one", "two"}}
	payload := types.ImageMultimodalPayload{TenantID: 42}
	cfg := types.VLMConfig{ModelID: "vlm-1"}

	first, meta, err := svc.predictVLMWithArtifact(ctx, model, payload, cfg, []byte("image"), "prompt", "vlm_caption")
	if err != nil {
		t.Fatal(err)
	}
	second, meta2, err := svc.predictVLMWithArtifact(ctx, model, payload, cfg, []byte("image"), "prompt", "vlm_caption")
	if err != nil {
		t.Fatal(err)
	}
	if first != "one" || second != "two" || meta.Outcome != artifact.OutcomeBypass || meta2.Outcome != artifact.OutcomeBypass {
		t.Fatalf("stage disabled got first=%q second=%q outcomes=%s/%s", first, second, meta.Outcome, meta2.Outcome)
	}
	if store.puts != 0 || model.calls != 2 {
		t.Fatalf("puts=%d calls=%d, want no cache and two provider calls", store.puts, model.calls)
	}
}

func TestPredictVLMWithArtifactReturnsProviderError(t *testing.T) {
	ctx := context.Background()
	svc := &ImageMultimodalService{}
	modelErr := errors.New("provider down")
	model := &fakeArtifactVLM{err: modelErr}

	_, _, err := svc.predictVLMWithArtifact(ctx, model, types.ImageMultimodalPayload{TenantID: 42}, types.VLMConfig{}, []byte("image"), "prompt", "vlm_caption")
	if !errors.Is(err, modelErr) {
		t.Fatalf("err=%v, want provider error", err)
	}
}

func TestImageMultimodalPostProcessPayloadCarriesGenerationFence(t *testing.T) {
	queue := &manualReparseTaskEnqueuer{}
	svc := &ImageMultimodalService{taskEnqueuer: queue}

	svc.enqueueKnowledgePostProcessTask(context.Background(), types.ImageMultimodalPayload{
		TenantID:        42,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		Language:        "en",
		Attempt:         5,
		GenerationID:    "generation-5",
	})

	if queue.task == nil {
		t.Fatal("expected post-process task")
	}
	var payload types.KnowledgePostProcessPayload
	if err := json.Unmarshal(queue.task.Payload(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Attempt != 5 || payload.GenerationID != "generation-5" {
		t.Fatalf("post-process fence attempt=%d generation=%q", payload.Attempt, payload.GenerationID)
	}
}
