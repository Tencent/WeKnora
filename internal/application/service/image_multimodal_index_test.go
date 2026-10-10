package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ---------------------------------------------------------------------------
// Regression tests for #3834: when the index write for an image's OCR/caption
// chunks fails, processImage must return the error (so asynq retries the task)
// and must not record a successful index in the processing trace.
// ---------------------------------------------------------------------------

const idxEngineType = types.RetrieverEngineType("idx-vector-store")

// idxEngineRepo is a retrieval backend whose batch write can be made to fail,
// standing in for a vector store that is down or rejects the write.
type idxEngineRepo struct {
	interfaces.RetrieveEngineRepository
	batchSaveErr error
	deleteErr    error
	saved        []*types.IndexInfo
	deleted      []string
}

func (r *idxEngineRepo) EngineType() types.RetrieverEngineType { return idxEngineType }

func (r *idxEngineRepo) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType}
}

func (r *idxEngineRepo) BatchSave(_ context.Context, infos []*types.IndexInfo, _ map[string]any) error {
	r.saved = append(r.saved, infos...)
	return r.batchSaveErr
}

func (r *idxEngineRepo) DeleteBySourceIDList(
	_ context.Context, sourceIDList []string, _ int, _ string,
) error {
	r.deleted = append(r.deleted, sourceIDList...)
	return r.deleteErr
}

// idxRegistry resolves the tenant's engine type to the backend above, or fails
// the way the engine factory does when the store binding cannot be resolved.
type idxRegistry struct {
	interfaces.RetrieveEngineRegistry
	engine interfaces.RetrieveEngineService
	err    error
}

func (r *idxRegistry) GetRetrieveEngineService(
	_ types.RetrieverEngineType,
) (interfaces.RetrieveEngineService, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.engine, nil
}

type idxEmbedder struct{}

func (idxEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

func (idxEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{0.1, 0.2, 0.3}
	}
	return out, nil
}

func (idxEmbedder) BatchEmbedWithPool(
	_ context.Context, _ embedding.Embedder, texts []string,
) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{0.1, 0.2, 0.3}
	}
	return out, nil
}

func (idxEmbedder) GetModelName() string { return "idx-embed" }
func (idxEmbedder) GetDimensions() int   { return 3 }
func (idxEmbedder) GetModelID() string   { return "idx-embed" }

type idxModelService struct {
	interfaces.ModelService
	embedder embedding.Embedder
	err      error
}

func (s *idxModelService) GetEmbeddingModel(_ context.Context, _ string) (embedding.Embedder, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.embedder, nil
}

type idxTenantRepo struct {
	interfaces.TenantRepository
	tenant *types.Tenant
	err    error
}

func (r *idxTenantRepo) GetTenantByID(_ context.Context, _ uint64) (*types.Tenant, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.tenant, nil
}

type idxKBService struct {
	interfaces.KnowledgeBaseService
	kb  *types.KnowledgeBase
	err error
}

func (s *idxKBService) GetKnowledgeBaseByIDOnly(_ context.Context, _ string) (*types.KnowledgeBase, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.kb, nil
}

type idxChunkRepo struct {
	interfaces.ChunkRepository
	created []*types.Chunk
	byID    map[string]*types.Chunk
	deleted []string
	updates int
}

func newIdxChunkRepo() *idxChunkRepo {
	return &idxChunkRepo{byID: map[string]*types.Chunk{}}
}

func (r *idxChunkRepo) CreateChunks(_ context.Context, chunks []*types.Chunk) error {
	r.created = append(r.created, chunks...)
	for _, c := range chunks {
		r.byID[c.ID] = c
	}
	return nil
}

// ListChunksByKnowledgeIDAndTypes behaves like the SQL it stands in for: only
// this tenant's and knowledge's chunks of the requested types come back.
func (r *idxChunkRepo) ListChunksByKnowledgeIDAndTypes(
	_ context.Context, tenantID uint64, knowledgeID string, chunkTypes []types.ChunkType,
) ([]*types.Chunk, error) {
	wanted := make(map[types.ChunkType]bool, len(chunkTypes))
	for _, chunkType := range chunkTypes {
		wanted[chunkType] = true
	}
	var out []*types.Chunk
	for _, chunk := range r.byID {
		if chunk.TenantID == tenantID && chunk.KnowledgeID == knowledgeID && wanted[chunk.ChunkType] {
			out = append(out, chunk)
		}
	}
	return out, nil
}

func (r *idxChunkRepo) DeleteChunks(_ context.Context, tenantID uint64, ids []string) error {
	for _, id := range ids {
		if chunk, ok := r.byID[id]; ok && chunk.TenantID == tenantID {
			delete(r.byID, id)
			r.deleted = append(r.deleted, id)
		}
	}
	return nil
}

func (r *idxChunkRepo) GetChunkByIDOnly(_ context.Context, id string) (*types.Chunk, error) {
	c, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("chunk %s not found", id)
	}
	return c, nil
}

func (r *idxChunkRepo) UpdateChunk(_ context.Context, chunk *types.Chunk) error {
	r.updates++
	r.byID[chunk.ID] = chunk
	return nil
}

type idxChunkService struct {
	interfaces.ChunkService
	repo *idxChunkRepo
}

func (s *idxChunkService) GetRepository() interfaces.ChunkRepository { return s.repo }

func (s *idxChunkService) GetChunkByIDOnly(ctx context.Context, id string) (*types.Chunk, error) {
	return s.repo.GetChunkByIDOnly(ctx, id)
}

// idxVLM answers every prompt, so one observation-free run yields one OCR chunk.
type idxVLM struct{}

func (idxVLM) Predict(_ context.Context, _ [][]byte, _ string) (string, error) {
	return "INVOICE-8842 TOTAL 120.00", nil
}
func (idxVLM) GetModelName() string { return "idx-vlm" }
func (idxVLM) GetModelID() string   { return "idx-vlm" }

// idxHarness wires processImage to a KB that wants vector indexing and to the
// retrieval backend under test.
type idxHarness struct {
	svc     *ImageMultimodalService
	repo    *idxChunkRepo
	backend *idxEngineRepo
	payload *types.ImageMultimodalPayload
}

// defaultIndexKB is a knowledge base with a vector/keyword pipeline enabled, so
// indexChunks must go through the retrieval engine.
func defaultIndexKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID:               "kb-1",
		EmbeddingModelID: "emb-1",
		IndexingStrategy: types.DefaultIndexingStrategy(),
	}
}

func newIdxHarness(t *testing.T, backend *idxEngineRepo, kb *types.KnowledgeBase) *idxHarness {
	t.Helper()
	h := newIdxHarnessWithEngine(t, retriever.NewKVHybridRetrieveEngine(backend, idxEngineType), kb)
	h.backend = backend
	return h
}

// newIdxHarnessWithEngine is newIdxHarness for an engine that is not the
// idxEngineRepo double: a real repository, so the chain from its wire contract
// down to the replacement invariant is exercised without a service mock.
func newIdxHarnessWithEngine(
	t *testing.T, engine interfaces.RetrieveEngineService, kb *types.KnowledgeBase,
) *idxHarness {
	t.Helper()
	imgPath := filepath.Join(t.TempDir(), "index-test.png")
	if err := os.WriteFile(imgPath, []byte("image-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	tenant := &types.Tenant{ID: 1}
	tenant.RetrieverEngines = types.RetrieverEngines{Engines: []types.RetrieverEngineParams{
		{RetrieverEngineType: idxEngineType, RetrieverType: types.VectorRetrieverType},
		{RetrieverEngineType: idxEngineType, RetrieverType: types.KeywordsRetrieverType},
	}}

	repo := newIdxChunkRepo()
	return &idxHarness{
		svc: &ImageMultimodalService{
			chunkService:   &idxChunkService{repo: repo},
			modelService:   &idxModelService{embedder: idxEmbedder{}},
			kbService:      &idxKBService{kb: kb},
			tenantRepo:     &idxTenantRepo{tenant: tenant},
			retrieveEngine: &idxRegistry{engine: engine},
		},
		repo: repo,
		payload: &types.ImageMultimodalPayload{
			TenantID:        1,
			KnowledgeID:     "k-1",
			KnowledgeBaseID: "kb-1",
			ChunkID:         "parent-chunk-1",
			ImageURL:        "index-test.png",
			ImageLocalPath:  imgPath,
			EnableOCR:       true,
		},
	}
}

func (h *idxHarness) run(t *testing.T) (types.JSONMap, error) {
	t.Helper()
	return h.runWithContext(context.Background())
}

// runRetry runs processImage as a retry attempt of the task, the only attempt
// that cleans up chunks a previous attempt left behind.
func (h *idxHarness) runRetry(t *testing.T) (types.JSONMap, error) {
	t.Helper()
	return h.runWithContext(types.WithTaskRetryMetadata(context.Background(), 1, 3))
}

func (h *idxHarness) runWithContext(ctx context.Context) (types.JSONMap, error) {
	out := types.JSONMap{}
	err := h.svc.processImage(ctx, h.payload, idxVLM{}, types.VLMConfig{}, noopSpanTracker{}, out)
	return out, err
}

// decodeIndexTrace reads out["indexed"] back through JSON — the shape the
// persisted span output is read from — and returns its two counts. The pre-fix
// unconditional boolean cannot be decoded this way, which is exactly the
// regression this helper guards.
func decodeIndexTrace(t *testing.T, out types.JSONMap) (succeeded, failed int) {
	t.Helper()
	blob, err := json.Marshal(out["indexed"])
	if err != nil {
		t.Fatalf("encode out[\"indexed\"]: %v", err)
	}
	var counts struct {
		Succeeded int `json:"succeeded"`
		Failed    int `json:"failed"`
	}
	if err := json.Unmarshal(blob, &counts); err != nil {
		t.Fatalf("out[\"indexed\"] = %s, want {succeeded, failed}: %v", blob, err)
	}
	return counts.Succeeded, counts.Failed
}

// TestProcessImageFailsWhenIndexWriteFails is the regression test for #3834:
// the backend rejects the batch write, so the task must fail instead of
// finishing successfully with a trace that claims the chunks were indexed.
func TestProcessImageFailsWhenIndexWriteFails(t *testing.T) {
	t.Parallel()

	indexErr := errors.New("vector store unavailable")
	backend := &idxEngineRepo{batchSaveErr: indexErr}
	h := newIdxHarness(t, backend, defaultIndexKB())

	out, err := h.run(t)

	if !errors.Is(err, indexErr) {
		t.Fatalf("processImage error = %v, want the index failure %v", err, indexErr)
	}
	if len(backend.saved) != 1 {
		t.Fatalf("vector-store writes = %d, want the image's one OCR chunk", len(backend.saved))
	}
	if len(h.repo.created) != 1 {
		t.Fatalf("persisted chunks = %d, want 1", len(h.repo.created))
	}
	if _, present := out["indexed"]; present {
		t.Errorf("failed index write left out[\"indexed\"] = %#v", out["indexed"])
	}
	if h.repo.updates != 0 {
		t.Errorf("chunk status updates = %d, want 0 after a failed index write", h.repo.updates)
	}
	for _, chunk := range h.repo.created {
		if chunk.Status == int(types.ChunkStatusIndexed) {
			t.Errorf("chunk %s is marked indexed despite the failed write", chunk.ID)
		}
	}
}

// TestProcessImageReportsIndexCountsOnSuccess keeps the success path honest:
// the chunks reach the backend, the status is persisted, and the trace reports
// the counts instead of a bare boolean.
func TestProcessImageReportsIndexCountsOnSuccess(t *testing.T) {
	t.Parallel()

	backend := &idxEngineRepo{}
	h := newIdxHarness(t, backend, defaultIndexKB())

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("processImage: %v", err)
	}

	succeeded, failed := decodeIndexTrace(t, out)
	if succeeded != 1 || failed != 0 {
		t.Errorf("out[\"indexed\"] = {succeeded:%d, failed:%d}, want {succeeded:1, failed:0}", succeeded, failed)
	}
	if len(backend.saved) != 1 {
		t.Fatalf("vector-store writes = %d, want 1", len(backend.saved))
	}
	if got := backend.saved[0].Content; got != "INVOICE-8842 TOTAL 120.00" {
		t.Errorf("indexed content = %q, want the OCR text", got)
	}
	if !backend.saved[0].IsEnabled {
		t.Error("indexed chunk must carry is_enabled=true or engines filter it out of search")
	}
	if len(h.repo.created) != 1 {
		t.Fatalf("persisted chunks = %d, want 1", len(h.repo.created))
	}
	if got := h.repo.byID[h.repo.created[0].ID].Status; got != int(types.ChunkStatusIndexed) {
		t.Errorf("chunk status = %d, want indexed (%d)", got, types.ChunkStatusIndexed)
	}
}

// TestProcessImageSkipsIndexingWithoutEmbeddingPipeline pins the documented
// contract for KBs whose pipelines need no embedding model (e.g. Wiki-only):
// there is nothing to index, the image still succeeds, and its chunks are still
// marked indexed.
func TestProcessImageSkipsIndexingWithoutEmbeddingPipeline(t *testing.T) {
	t.Parallel()

	backend := &idxEngineRepo{}
	h := newIdxHarness(t, backend, &types.KnowledgeBase{ID: "kb-1"})

	out, err := h.run(t)
	if err != nil {
		t.Fatalf("processImage: %v", err)
	}

	succeeded, failed := decodeIndexTrace(t, out)
	if succeeded != 1 || failed != 0 {
		t.Errorf("out[\"indexed\"] = {succeeded:%d, failed:%d}, want {succeeded:1, failed:0}", succeeded, failed)
	}
	if len(backend.saved) != 0 {
		t.Errorf("vector-store writes = %d, want none without an embedding pipeline", len(backend.saved))
	}
	if got := h.repo.byID[h.repo.created[0].ID].Status; got != int(types.ChunkStatusIndexed) {
		t.Errorf("chunk status = %d, want indexed (%d)", got, types.ChunkStatusIndexed)
	}
}

// TestProcessImageFailsWhenIndexSetupFails covers the remaining failure
// branches: the KB, the embedding model, the tenant and the engine factory all
// abort the index write before it starts, and each must reach the caller.
func TestProcessImageFailsWhenIndexSetupFails(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		setup func(h *idxHarness)
		want  string
	}{
		{
			name:  "knowledge base lookup",
			setup: func(h *idxHarness) { h.svc.kbService = &idxKBService{err: errors.New("kb lookup down")} },
			want:  "kb lookup down",
		},
		{
			name:  "knowledge base missing",
			setup: func(h *idxHarness) { h.svc.kbService = &idxKBService{} },
			want:  "knowledge base kb-1 not found",
		},
		{
			name:  "embedding model",
			setup: func(h *idxHarness) { h.svc.modelService = &idxModelService{err: errors.New("embedding down")} },
			want:  "embedding down",
		},
		{
			name:  "tenant lookup",
			setup: func(h *idxHarness) { h.svc.tenantRepo = &idxTenantRepo{err: errors.New("tenant lookup down")} },
			want:  "tenant lookup down",
		},
		{
			name:  "retrieve engine factory",
			setup: func(h *idxHarness) { h.svc.retrieveEngine = &idxRegistry{err: errors.New("engine factory down")} },
			want:  "engine factory down",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newIdxHarness(t, &idxEngineRepo{}, defaultIndexKB())
			tc.setup(h)

			out, err := h.run(t)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("processImage error = %v, want it to mention %q", err, tc.want)
			}
			if _, present := out["indexed"]; present {
				t.Errorf("failed index setup left out[\"indexed\"] = %#v", out["indexed"])
			}
			if len(h.backend.saved) != 0 {
				t.Errorf("vector-store writes = %d, want none when the setup failed", len(h.backend.saved))
			}
			if h.repo.updates != 0 {
				t.Errorf("chunk status updates = %d, want none when the setup failed", h.repo.updates)
			}
		})
	}
}
