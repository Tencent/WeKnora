package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type imageEditRepo struct {
	interfaces.ChunkRepository
	rows map[string]*types.Chunk
}

type imageEditEmbedding struct {
	embedding.Embedder
	mu     sync.Mutex
	inputs []string
}

func (e *imageEditEmbedding) Embed(_ context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inputs = append(e.inputs, text)
	return []float32{1, 0}, nil
}

func (*imageEditEmbedding) GetDimensions() int { return 2 }

type imageEditModels struct {
	interfaces.ModelService
	embedding *imageEditEmbedding
}

func (m imageEditModels) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return m.embedding, nil
}

type imageEditKB struct{ editableChunkKBRepo }

func (imageEditKB) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: "kb", TenantID: 1, IndexingStrategy: types.DefaultIndexingStrategy()}, nil
}

type imageEditEngine struct {
	interfaces.RetrieveEngineService
	mu      sync.Mutex
	fail    bool
	ids     []string
	calls   int
	started chan struct{}
	release chan struct{}
	indexed map[string]string
}

func (*imageEditEngine) EngineType() types.RetrieverEngineType {
	return types.SQLiteRetrieverEngineType
}

func (*imageEditEngine) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (e *imageEditEngine) DeleteByChunkIDList(_ context.Context, ids []string, _ int, _ string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ids = append(e.ids, ids...)
	for _, id := range ids {
		delete(e.indexed, id)
	}
	return nil
}

func (e *imageEditEngine) BatchIndex(
	ctx context.Context, model embedding.Embedder, items []*types.IndexInfo, _ []types.RetrieverType,
) error {
	e.mu.Lock()
	e.calls++
	first := e.calls == 1 && e.started != nil
	e.mu.Unlock()
	for _, item := range items {
		if _, err := model.Embed(ctx, item.Content); err != nil {
			return err
		}
	}
	if first {
		close(e.started)
		<-e.release
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.indexed == nil {
		e.indexed = make(map[string]string)
	}
	for _, item := range items {
		e.indexed[item.ChunkID] = item.Content
	}
	if e.fail {
		return errors.New("index unavailable")
	}
	return nil
}

func TestEditImageEvidenceSurvivesConcurrentParentWrite(t *testing.T) {
	for _, scenario := range []struct{ enableParent, disableChild bool }{{false, false}, {true, false}, {true, true}} {
		enableParent := scenario.enableParent
		name := "parent content edit"
		if enableParent {
			name = "parent enabled state"
			if scenario.disableChild {
				name += " and child disabled during indexing"
			}
		}
		t.Run(name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			require.NoError(t, db.AutoMigrate(&types.Chunk{}, &types.ChunkRevision{}))
			r := repository.NewChunkRepository(db)
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
			ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{
				RetrieverEngines: types.RetrieverEngines{
					Engines: []types.RetrieverEngineParams{{
						RetrieverType:       types.VectorRetrieverType,
						RetrieverEngineType: types.SQLiteRetrieverEngineType,
					}},
				},
			})
			ctx = (&access.KBAccess{
				KnowledgeBase: &types.KnowledgeBase{ID: "kb", TenantID: 1},
				Caller:        types.CallerFromContext(ctx), EffectiveTenantID: 1,
				Permission: types.OrgRoleEditor,
			}).Context(ctx)
			info := `[{"url":"local://chart.png","caption":"old"}]`
			source := &types.Chunk{
				ID: "source", TenantID: 1, KnowledgeID: "knowledge", KnowledgeBaseID: "kb",
				Content: "Source ![chart](local://chart.png)", ImageInfo: info, ChunkType: types.ChunkTypeText,
				IsEnabled: !enableParent, IndexStatus: "ready",
			}
			child := &types.Chunk{
				ID: "caption", TenantID: 1, KnowledgeID: "knowledge", KnowledgeBaseID: "kb",
				Content: "old", ImageInfo: info, ChunkType: types.ChunkTypeImageCaption, ParentChunkID: source.ID,
				IsEnabled: !enableParent, IndexStatus: "ready",
			}
			require.NoError(t, r.CreateChunks(ctx, []*types.Chunk{source, child}))
			engine := &imageEditEngine{started: make(chan struct{}), release: make(chan struct{})}
			s := &chunkService{
				chunkRepository: r, knowledgeRepo: editableChunkKnowledgeRepo{}, kbRepository: imageEditKB{},
				modelService:   imageEditModels{embedding: &imageEditEmbedding{}},
				retrieveEngine: imageEditRegistry{engine: engine},
			}
			finished := make(chan error, 1)
			go func() {
				content, enabled := source.Content+" corrected", true
				if enableParent {
					_, err := s.UpdateDocumentChunk(ctx, source.ID, nil, &enabled, nil)
					finished <- err
				} else {
					_, err := s.UpdateDocumentChunk(ctx, source.ID, &content, nil, nil)
					finished <- err
				}
			}()
			select {
			case <-engine.started:
			case err := <-finished:
				t.Fatalf("parent stopped before indexing: %v", err)
			}
			corrected := "corrected caption"
			var editErr error
			if scenario.disableChild {
				corrected = "old"
				enabled := false
				_, editErr = s.UpdateDocumentChunk(ctx, child.ID, nil, &enabled, nil)
			} else {
				_, editErr = s.UpdateDocumentChunk(ctx, child.ID, &corrected, nil, nil)
			}
			close(engine.release)
			require.NoError(t, <-finished)
			require.NoError(t, editErr)
			stored, err := r.GetChunkByID(ctx, 1, child.ID)
			require.NoError(t, err)
			require.Equal(t, corrected, stored.Content)
			require.Equal(t, 1, stored.ContentRevision)
			parent, err := r.GetChunkByID(ctx, 1, source.ID)
			require.NoError(t, err)
			require.Contains(t, parent.ImageInfo, corrected)
			if scenario.disableChild {
				require.False(t, stored.IsEnabled, "late status updates must not re-enable the child")
				require.NotContains(t, engine.indexed, child.ID)
			} else {
				require.Contains(t, engine.indexed[child.ID], corrected,
					"late old index writes must repair the current revision")
			}
			revision := stored.ContentRevision
			reverted, err := s.RevertDocumentChunk(ctx, child.ID, 0, &revision)
			require.NoError(t, err)
			require.Equal(t, "old", reverted.Content)
			require.Equal(t, 2, reverted.ContentRevision)
			parent, err = r.GetChunkByID(ctx, 1, source.ID)
			require.NoError(t, err)
			require.Contains(t, parent.ImageInfo, `"caption":"old"`)
		})
	}
}

type imageEditRegistry struct {
	interfaces.RetrieveEngineRegistry
	engine *imageEditEngine
}

func (r imageEditRegistry) GetRetrieveEngineService(
	types.RetrieverEngineType,
) (interfaces.RetrieveEngineService, error) {
	return r.engine, nil
}

func TestEditImageEvidenceRetriesOnlyItsTextIndex(t *testing.T) {
	r := &imageEditRepo{rows: map[string]*types.Chunk{"image": {
		ID: "image", TenantID: 1, KnowledgeID: "knowledge", KnowledgeBaseID: "kb",
		ChunkType: types.ChunkTypeImageCaption, Content: "old", IsEnabled: true,
		ImageInfo: `[{"url":"local://chart.png","caption":"old","future":{"keep":true}}]`,
	}}}
	emb := &imageEditEmbedding{}
	engine := &imageEditEngine{fail: true}
	s := &chunkService{
		chunkRepository: r, knowledgeRepo: editableChunkKnowledgeRepo{}, kbRepository: imageEditKB{},
		modelService: imageEditModels{embedding: emb}, retrieveEngine: imageEditRegistry{engine: engine},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{
		RetrieverEngines: types.RetrieverEngines{
			Engines: []types.RetrieverEngineParams{{
				RetrieverType:       types.VectorRetrieverType,
				RetrieverEngineType: types.SQLiteRetrieverEngineType,
			}},
		},
	})
	ctx = (&access.KBAccess{
		KnowledgeBase: &types.KnowledgeBase{ID: "kb", TenantID: 1},
		Caller:        types.CallerFromContext(ctx), EffectiveTenantID: 1,
		Permission: types.OrgRoleEditor,
	}).Context(ctx)
	content := "corrected diagram"
	updated, err := s.UpdateDocumentChunk(ctx, "image", &content, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "failed", updated.IndexStatus)
	require.Equal(t, content, r.rows["image"].Content)
	require.Contains(t, emb.inputs[0], content)
	require.Contains(t, updated.ImageInfo, `"future":{"keep":true}`)
	engine.fail = false
	revision := updated.ContentRevision
	updated, err = s.UpdateDocumentChunk(ctx, "image", nil, nil, &revision)
	require.NoError(t, err)
	require.Equal(t, "ready", updated.IndexStatus)
	require.Equal(t, revision, updated.ContentRevision, "index retry must not invent a content edit")
	require.Equal(t, []string{"image", "image"}, engine.ids, "never replace the sibling's visual vector")
}

func (r *imageEditRepo) GetChunkByID(_ context.Context, _ uint64, id string) (*types.Chunk, error) {
	c := *r.rows[id]
	return &c, nil
}

func (r *imageEditRepo) ListChunkByParentID(_ context.Context, _ uint64, id string) ([]*types.Chunk, error) {
	var out []*types.Chunk
	for _, c := range r.rows {
		if c.ParentChunkID == id {
			copyOfRow := *c
			out = append(out, &copyOfRow)
		}
	}
	return out, nil
}

func (r *imageEditRepo) UpdateChunk(_ context.Context, c *types.Chunk) error {
	copyOfRow := *c
	r.rows[c.ID] = &copyOfRow
	return nil
}

func (r *imageEditRepo) SaveChunkRevision(ctx context.Context, c *types.Chunk, _ *types.ChunkRevision, _ int) error {
	return r.UpdateChunk(ctx, c)
}

func (r *imageEditRepo) UpdateChunkFieldsByIDs(
	_ context.Context, _ uint64, ids []string, fields map[string]interface{},
) error {
	for _, id := range ids {
		r.rows[id].IndexStatus = fields["index_status"].(string)
	}
	return nil
}

func TestEditImageEvidencePreservesParent(t *testing.T) {
	for _, kind := range []types.ChunkType{types.ChunkTypeImageOCR, types.ChunkTypeImageCaption} {
		t.Run(string(kind), func(t *testing.T) {
			parent := &types.Chunk{
				ID: "parent", TenantID: 1, KnowledgeID: "knowledge", KnowledgeBaseID: "kb",
				ChunkType: types.ChunkTypeText,
				Content:   "Source ![chart](local://chart.png) paragraph", IsEnabled: true,
			}
			info := []types.ImageInfo{{URL: "local://chart.png", Caption: "old caption", OCRText: "old OCR"}}
			raw, err := json.Marshal(info)
			require.NoError(t, err)
			child := &types.Chunk{
				ID: "image", TenantID: 1, KnowledgeID: "knowledge", KnowledgeBaseID: "kb",
				ChunkType: kind, ParentChunkID: parent.ID, Content: "old content",
				ImageInfo: string(raw), IsEnabled: true,
			}
			r := &imageEditRepo{rows: map[string]*types.Chunk{parent.ID: parent, child.ID: child}}
			s := &chunkService{
				chunkRepository: r, knowledgeRepo: editableChunkKnowledgeRepo{},
				kbRepository: editableChunkKBRepo{},
			}
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
			ctx = (&access.KBAccess{
				KnowledgeBase: &types.KnowledgeBase{ID: "kb", TenantID: 1},
				Caller:        types.CallerFromContext(ctx), EffectiveTenantID: 1,
				Permission: types.OrgRoleEditor,
			}).Context(ctx)
			content, revision := "corrected evidence", 0
			updated, err := s.UpdateDocumentChunk(ctx, child.ID, &content, nil, &revision)
			require.NoError(t, err)
			require.Equal(t, content, updated.Content)
			require.Equal(t, 1, updated.ContentRevision)
			require.Equal(t, parent.Content, r.rows[parent.ID].Content, "image evidence has no parser text offsets")
			require.NoError(t, json.Unmarshal([]byte(updated.ImageInfo), &info))
			if kind == types.ChunkTypeImageOCR {
				require.Equal(t, content, info[0].OCRText)
				require.Equal(t, "old caption", info[0].Caption)
			} else {
				require.Equal(t, content, info[0].Caption)
				require.Equal(t, "old OCR", info[0].OCRText)
			}
			require.NotEmpty(t, updated.ContentHash)
			_, err = s.UpdateDocumentChunk(ctx, child.ID, &content, nil, &revision)
			require.ErrorIs(t, err, ErrChunkRevisionConflict)
		})
	}
}
