package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	sqliteretriever "github.com/Tencent/WeKnora/internal/application/repository/retriever/sqlite"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Use the actual SQLite/sqlite-vec repository, KV indexer, composite retrieval,
// handlers and services. Only the embedding model and resource registry are
// interface fixtures, as in the repository's own offline tests.
type recordingBackend struct {
	interfaces.RetrieveEngineService
	mu     sync.Mutex
	writes int
	fail   bool
}

func (e *recordingBackend) BatchIndex(
	ctx context.Context, m embedding.Embedder, infos []*types.IndexInfo, types []types.RetrieverType,
) error {
	if e.fail {
		return errors.New("transient storage failure")
	}
	if err := e.RetrieveEngineService.BatchIndex(ctx, m, infos, types); err != nil {
		return err
	}
	e.mu.Lock()
	e.writes += len(infos)
	e.mu.Unlock()
	return nil
}

type registry struct {
	interfaces.RetrieveEngineRegistry
	backend *recordingBackend
}

func (r registry) GetOrLoadByStoreID(context.Context, uint64, string) (interfaces.RetrieveEngineService, error) {
	return r.backend, nil
}

func (r registry) GetRetrieveEngineService(types.RetrieverEngineType) (interfaces.RetrieveEngineService, error) {
	return r.backend, nil
}

type owner struct{ storeID string }

func (o owner) StoreOwnedBy(_ context.Context, id string, tenant uint64) (bool, error) {
	return id == o.storeID && tenant == 7, nil
}

type embedder struct {
	mu      sync.Mutex
	indexed []string
	fail    bool
}

func (*embedder) GetDimensions() int                               { return 1 }
func (*embedder) GetModelName() string                             { return "validation-model" }
func (*embedder) GetModelID() string                               { return "embedding-model" }
func (*embedder) Embed(context.Context, string) ([]float32, error) { return []float32{1}, nil }
func (m *embedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	if m.fail {
		return nil, errors.New("transient embedding failure")
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1}
	}
	return out, nil
}

func (m *embedder) BatchEmbedWithPool(ctx context.Context, _ embedding.Embedder, texts []string) ([][]float32, error) {
	m.mu.Lock()
	m.indexed = append(m.indexed, texts...)
	m.mu.Unlock()
	return m.BatchEmbed(ctx, texts)
}

type models struct {
	interfaces.ModelService
	embedder *embedder
}

func (m models) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return m.embedder, nil
}

func (m models) GetEmbeddingModelForTenant(context.Context, string, uint64) (embedding.Embedder, error) {
	return m.embedder, nil
}

func (m models) GetModelByID(_ context.Context, id string) (*types.Model, error) {
	return &types.Model{ID: id, Type: types.ModelTypeEmbedding, Name: "validation-model"}, nil
}

type tenants struct {
	interfaces.TenantService
	tenant *types.Tenant
}

func (t tenants) GetTenantByID(context.Context, uint64) (*types.Tenant, error) { return t.tenant, nil }

func TestWikiIndexLifecycleHTTP(t *testing.T) {
	for _, mode := range []string{"retry_embedding", "retry_storage", "model_change"} {
		t.Run(mode, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			sqlitevec.Auto()
			db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
			require.NoError(t, err)
			require.NoError(t, db.AutoMigrate(
				&types.KnowledgeBase{}, &types.Knowledge{}, &types.Chunk{},
				&types.WikiPage{}, &types.WikiPageRevision{}, &types.WikiFolder{},
			))
			kbRepo := repository.NewKnowledgeBaseRepository(db)
			chunkRepo := repository.NewChunkRepository(db)
			wikiRepo := repository.NewWikiPageRepository(db)
			storeID := uuid.NewString()
			kb := &types.KnowledgeBase{
				ID: uuid.NewString(), TenantID: 7, Name: "Wiki HTTP regression", Type: "document",
				EmbeddingModelID: "embedding-model", VectorStoreID: &storeID,
				IndexingStrategy: types.IndexingStrategy{WikiEnabled: true, VectorEnabled: true},
				ChunkingConfig:   types.ChunkingConfig{ChunkSize: 512, ChunkOverlap: 80},
			}
			require.NoError(t, kbRepo.CreateKnowledgeBase(context.Background(), kb))
			engine := &recordingBackend{RetrieveEngineService: retriever.NewKVHybridRetrieveEngine(
				sqliteretriever.NewSQLiteRetrieveEngineRepository(db), types.SQLiteRetrieverEngineType,
			)}
			model := &embedder{}
			modelService := models{embedder: model}
			reg := registry{backend: engine}
			ownership := owner{storeID: storeID}
			tenant := &types.Tenant{ID: 7}
			kbService := service.NewKnowledgeBaseService(
				kbRepo, repository.NewKnowledgeRepository(db), chunkRepo, nil, nil, modelService, reg, ownership,
				nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, wikiRepo,
			)
			wikiService := service.NewIndexedWikiPageService(
				wikiRepo, chunkRepo, kbService, nil, nil, nil, modelService, reg, ownership, tenants{tenant: tenant},
			)
			wikiHandler := handler.NewWikiPageHandler(wikiService, kbService, nil, nil, nil)
			searchHandler := handler.NewKnowledgeBaseHandler(
				nil, kbService, nil, nil, nil, nil, nil, nil, nil, nil, nil,
			)
			router := gin.New()
			router.Use(middleware.ErrorHandler())
			router.Use(func(c *gin.Context) {
				ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
				ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
				ctx = context.WithValue(ctx, types.UserIDContextKey, "validation-owner")
				ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleOwner)
				c.Request = c.Request.WithContext(ctx)
				c.Set(types.TenantIDContextKey.String(), uint64(7))
				c.Next()
			})
			router.POST("/api/v1/knowledgebase/:kb_id/wiki/pages", wikiHandler.CreatePage)
			router.POST("/api/v1/knowledge-bases/:id/hybrid-search", searchHandler.HybridSearch)
			perform := func(path, body string) *httptest.ResponseRecorder {
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				return response
			}
			requestBody := `{
 "slug":"concept/validation-4372","title":"Wiki-only concept",
 "content":"WIKI_ONLY_4372_TERM is synthesized across documents.",
 "page_type":"concept","status":"published","page_metadata":{"b":2,"a":1}}`
			createURL := "/api/v1/knowledgebase/" + kb.ID + "/wiki/pages"
			model.fail = mode == "retry_embedding"
			engine.fail = mode == "retry_storage"
			created := perform(createURL, requestBody)
			if mode != "model_change" {
				require.Equal(t, http.StatusInternalServerError, created.Code)
				persisted, err := wikiRepo.GetBySlug(context.Background(), kb.ID, "concept/validation-4372")
				require.NoError(t, err)
				model.fail = false
				engine.fail = false
				conflict := perform(createURL, strings.Replace(requestBody, "Wiki-only concept", "Different title", 1))
				require.Equal(t, http.StatusInternalServerError, conflict.Code,
					"retry must not overwrite another creation")
				created = perform(createURL, requestBody)
				require.Equal(t, http.StatusCreated, created.Code,
					"identical create must recover indexing: %s", created.Body.String())
				var retried types.WikiPage
				require.NoError(t, json.Unmarshal(created.Body.Bytes(), &retried))
				require.Equal(t, persisted.ID, retried.ID)
				var count int64
				require.NoError(t, db.Model(&types.WikiPage{}).Count(&count).Error)
				require.EqualValues(t, 1, count)
			}
			require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
			if mode == "model_change" {
				for _, next := range []string{"same-dimension-other-model", "different-dimension-model"} {
					_, err := kbService.UpdateKnowledgeBase(ctx, kb.ID, kb.Name, "",
						&types.KnowledgeBaseConfig{EmbeddingModelID: &next}, nil)
					require.Error(t, err, "Wiki vectors must prevent model changes even without knowledge rows")
					saved, err := kbRepo.GetKnowledgeBaseByID(ctx, kb.ID)
					require.NoError(t, err)
					require.Equal(t, "embedding-model", saved.EmbeddingModelID)
				}
			}
			searched := perform("/api/v1/knowledge-bases/"+kb.ID+"/hybrid-search", `{
 "query_text":"WIKI_ONLY_4372_TERM","match_count":5,
 "disable_keywords_match":true,"skip_context_enrichment":true}`)
			require.Equal(t, http.StatusOK, searched.Code, searched.Body.String())
			var body struct {
				Data []*types.SearchResult `json:"data"`
			}
			require.NoError(t, json.Unmarshal(searched.Body.Bytes(), &body))
			require.NotEmpty(t, body.Data, "a published Wiki page must be returned by HTTP hybrid-search")
			require.Equal(t, types.ChunkTypeWikiPage, body.Data[0].ChunkType)
			require.Equal(t, "wiki", body.Data[0].KnowledgeSource)
			require.Equal(t, "concept/validation-4372", body.Data[0].Metadata["wiki_slug"])
			require.NotEmpty(t, body.Data[0].Metadata["wiki_page_id"])
			require.Contains(t, body.Data[0].Content, "WIKI_ONLY_4372_TERM")
			require.NotEmpty(t, model.indexed, "published Wiki content must reach the embedding interface")
			require.Positive(t, engine.writes, "retrieval must use artifacts written by Wiki publication")
			if mode == "model_change" {
				require.NoError(t, wikiService.DeletePage(ctx, kb.ID, "concept/validation-4372"))
				next := "different-dimension-model"
				_, err := kbService.UpdateKnowledgeBase(ctx, kb.ID, kb.Name, "",
					&types.KnowledgeBaseConfig{EmbeddingModelID: &next}, nil)
				require.NoError(t, err, "cleared Wiki indexes must not permanently lock an empty KB")
			} else {
				page, err := wikiRepo.GetBySlug(ctx, kb.ID, "concept/validation-4372")
				require.NoError(t, err)
				page.Status = types.WikiPageStatusArchived
				_, err = wikiService.UpdatePage(ctx, page)
				require.NoError(t, err)
				stale := perform(createURL, requestBody)
				require.Equal(t, http.StatusInternalServerError, stale.Code, "an old create must not undo archival")
			}
		})
	}
}
