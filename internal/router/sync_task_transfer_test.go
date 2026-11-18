package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type liteTransferHarness struct {
	engine *gin.Engine
	db     *gorm.DB
	files  interfaces.FileService
	tenant *types.Tenant
}

func newLiteTransferHarness(t *testing.T) *liteTransferHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join(t.TempDir(), "lite-transfer.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	require.NoError(t, db.AutoMigrate(
		&types.Tenant{},
		&types.KnowledgeBase{},
		&types.Knowledge{},
		&types.Chunk{},
		&types.KnowledgeTag{},
		&types.KnowledgeTagRelation{},
		&types.ChunkRevision{},
	))

	tenant := &types.Tenant{ID: 7, Name: "lite-workspace"}
	require.NoError(t, db.Create(tenant).Error)

	enabled := true
	cfg := &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}

	kbRepository := repository.NewKnowledgeBaseRepository(db)
	kgRepository := repository.NewKnowledgeRepository(db)
	chunkRepository := repository.NewChunkRepository(db)
	tagRepository := repository.NewKnowledgeTagRepository(db)
	tenantRepository := repository.NewTenantRepository(db)
	localFiles := filesvc.NewLocalFileService(t.TempDir(), "")

	syncExec := NewSyncTaskExecutor()
	kbService := service.NewKnowledgeBaseService(
		kbRepository, kgRepository, chunkRepository,
		nil, nil, nil, nil, nil,
		tenantRepository, localFiles, nil, nil,
		syncExec, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	chunkSvc := service.NewChunkService(chunkRepository, kgRepository, kbRepository, nil, nil, nil, syncExec, nil)
	// Lite / Desktop mode: redisClient is nil, tasks execute via SyncTaskExecutor.
	kgService, err := service.NewKnowledgeService(
		cfg, kgRepository, nil, kbService, tenantRepository, nil,
		chunkSvc, chunkRepository, tagRepository, nil,
		localFiles, nil, nil, nil, syncExec, nil, nil, nil, nil,
		nil, // redisClient == nil
		nil, nil, nil, nil, nil, nil, nil,
	)
	require.NoError(t, err)

	syncExec.RegisterHandler(types.TypeKBClone, kgService.ProcessKBClone)
	syncExec.RegisterHandler(types.TypeKnowledgeMove, kgService.ProcessKnowledgeMove)

	kbHandler := handler.NewKnowledgeBaseHandler(
		cfg, kbService, kgService, nil, nil, syncExec, nil, nil, localFiles, nil, nil,
	)
	kgHandler := handler.NewKnowledgeHandler(
		cfg, kgService, kbService, nil, nil, chunkSvc, nil, nil, syncExec, nil, nil,
	)

	r := gin.New()
	r.Use(middleware.Recovery(), middleware.ErrorHandler(), func(c *gin.Context) {
		ctx := types.WithCaller(
			c.Request.Context(),
			types.Caller{TenantID: tenant.ID, UserID: "user-1", Role: types.TenantRoleAdmin},
		)
		ctx = types.WithExecutionTenant(ctx, tenant.ID)
		ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
		c.Set(types.TenantIDContextKey.String(), tenant.ID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})

	guards := &rbacGuards{cfg: cfg}
	v1 := r.Group("/api/v1")
	RegisterKnowledgeBaseRoutes(v1, kbHandler, guards)
	RegisterKnowledgeRoutes(v1, kgHandler, guards)

	return &liteTransferHarness{
		engine: r,
		db:     db,
		files:  localFiles,
		tenant: tenant,
	}
}

func (h *liteTransferHarness) doJSON(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.engine.ServeHTTP(w, req)
	return w
}

func TestLiteKBCloneEndToEndWithoutRedis(t *testing.T) {
	h := newLiteTransferHarness(t)
	ctx := context.Background()

	filePath1, err := h.files.SaveBytes(ctx, []byte("alpha document bytes"), h.tenant.ID, "alpha.md", false)
	require.NoError(t, err)
	filePath2, err := h.files.SaveBytes(ctx, []byte("beta document bytes"), h.tenant.ID, "beta.md", false)
	require.NoError(t, err)

	srcKB := &types.KnowledgeBase{
		ID:        "src-doc-kb",
		TenantID:  h.tenant.ID,
		CreatorID: "user-1",
		Name:      "Source Docs",
		Type:      types.KnowledgeBaseTypeDocument,
	}
	require.NoError(t, h.db.Create(srcKB).Error)
	require.NoError(t, h.db.Create(&types.KnowledgeTag{
		ID: "tag-1", TenantID: h.tenant.ID, KnowledgeBaseID: srcKB.ID, Name: "guides",
	}).Error)

	for _, doc := range []*types.Knowledge{
		{
			ID: "doc-alpha", TenantID: h.tenant.ID, KnowledgeBaseID: srcKB.ID,
			Title: "Alpha", Type: "file", FileName: "alpha.md", FileType: "md",
			FilePath: filePath1, FileSize: 20, StorageSize: 20,
			ParseStatus: types.ParseStatusCompleted, EnableStatus: "enabled",
		},
		{
			ID: "doc-beta", TenantID: h.tenant.ID, KnowledgeBaseID: srcKB.ID,
			Title: "Beta", Type: "file", FileName: "beta.md", FileType: "md",
			FilePath: filePath2, FileSize: 19, StorageSize: 19,
			ParseStatus: types.ParseStatusCompleted, EnableStatus: "enabled",
		},
	} {
		require.NoError(t, h.db.Create(doc).Error)
	}
	require.NoError(t, h.db.Create([]*types.Chunk{
		{
			ID: "chunk-alpha", TenantID: h.tenant.ID, KnowledgeID: "doc-alpha",
			KnowledgeBaseID: srcKB.ID, TagID: "tag-1", ChunkType: types.ChunkTypeText,
			Content: "alpha chunk content", IsEnabled: true, Status: int(types.ChunkStatusIndexed),
		},
		{
			ID: "chunk-beta", TenantID: h.tenant.ID, KnowledgeID: "doc-beta",
			KnowledgeBaseID: srcKB.ID, ChunkType: types.ChunkTypeText,
			Content: "beta chunk content", IsEnabled: true, Status: int(types.ChunkStatusIndexed),
		},
	}).Error)

	// 1. Trigger KB copy via HTTP handler -> SyncTaskExecutor -> KnowledgeService (redisClient == nil)
	copyResp := h.doJSON(t, http.MethodPost, "/api/v1/knowledge-bases/copy", `{"source_id":"src-doc-kb"}`)
	require.Equal(t, http.StatusOK, copyResp.Code, copyResp.Body.String())

	var startBody struct {
		Success bool `json:"success"`
		Data    struct {
			TaskID   string `json:"task_id"`
			SourceID string `json:"source_id"`
			TargetID string `json:"target_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(copyResp.Body.Bytes(), &startBody))
	require.True(t, startBody.Success)
	require.NotEmpty(t, startBody.Data.TaskID)
	require.NotEmpty(t, startBody.Data.TargetID)

	// 2. Poll progress endpoint until completed
	var finalProgress types.KBCloneProgress
	require.Eventually(t, func() bool {
		pollResp := h.doJSON(t, http.MethodGet, "/api/v1/knowledge-bases/copy/progress/"+startBody.Data.TaskID, "")
		if pollResp.Code != http.StatusOK {
			return false
		}
		var pollBody struct {
			Success bool                  `json:"success"`
			Data    types.KBCloneProgress `json:"data"`
		}
		require.NoError(t, json.Unmarshal(pollResp.Body.Bytes(), &pollBody))
		finalProgress = pollBody.Data
		return pollBody.Data.Status == types.KBCloneStatusCompleted
	}, 5*time.Second, 20*time.Millisecond)

	require.Equal(t, 100, finalProgress.Progress)
	require.Equal(t, 2, finalProgress.Total)
	require.Equal(t, 2, finalProgress.Processed)

	// 3. Verify cloned KB, documents, chunks, and tags in SQLite
	var clonedDocs []types.Knowledge
	require.NoError(t, h.db.Where("knowledge_base_id = ?", startBody.Data.TargetID).Find(&clonedDocs).Error)
	require.Len(t, clonedDocs, 2)
	for _, doc := range clonedDocs {
		require.Equal(t, types.ParseStatusCompleted, doc.ParseStatus)
	}

	var clonedChunks []types.Chunk
	require.NoError(t, h.db.Where("knowledge_base_id = ?", startBody.Data.TargetID).Find(&clonedChunks).Error)
	require.Len(t, clonedChunks, 2)

	// 4. Unknown task ID returns 404 (not 500 panic)
	missingCloneID := utils.GenerateTaskID("kb_clone", h.tenant.ID, "missing")
	missingResp := h.doJSON(t, http.MethodGet, "/api/v1/knowledge-bases/copy/progress/"+missingCloneID, "")
	require.Equal(t, http.StatusNotFound, missingResp.Code, missingResp.Body.String())

	// 5. Empty FAQ KB clone also completes cleanly without Redis
	faqKB := &types.KnowledgeBase{
		ID:        "src-faq-kb",
		TenantID:  h.tenant.ID,
		CreatorID: "user-1",
		Name:      "Source FAQ",
		Type:      types.KnowledgeBaseTypeFAQ,
	}
	require.NoError(t, h.db.Create(faqKB).Error)
	faqCopyResp := h.doJSON(t, http.MethodPost, "/api/v1/knowledge-bases/copy", `{"source_id":"src-faq-kb"}`)
	require.Equal(t, http.StatusOK, faqCopyResp.Code, faqCopyResp.Body.String())
	require.NoError(t, json.Unmarshal(faqCopyResp.Body.Bytes(), &startBody))

	require.Eventually(t, func() bool {
		pollResp := h.doJSON(t, http.MethodGet, "/api/v1/knowledge-bases/copy/progress/"+startBody.Data.TaskID, "")
		if pollResp.Code != http.StatusOK {
			return false
		}
		var pollBody struct {
			Data types.KBCloneProgress `json:"data"`
		}
		require.NoError(t, json.Unmarshal(pollResp.Body.Bytes(), &pollBody))
		return pollBody.Data.Status == types.KBCloneStatusCompleted && pollBody.Data.Progress == 100
	}, 5*time.Second, 20*time.Millisecond)
}

func TestLiteKnowledgeMoveBatchEndToEndWithoutRedis(t *testing.T) {
	h := newLiteTransferHarness(t)

	for _, kb := range []*types.KnowledgeBase{
		{
			ID: "move-src-kb", TenantID: h.tenant.ID, CreatorID: "user-1",
			Name: "Move Source", Type: types.KnowledgeBaseTypeDocument,
		},
		{
			ID: "move-dst-kb", TenantID: h.tenant.ID, CreatorID: "user-1",
			Name: "Move Target", Type: types.KnowledgeBaseTypeDocument,
		},
	} {
		require.NoError(t, h.db.Create(kb).Error)
	}

	for _, doc := range []*types.Knowledge{
		{
			ID: "move-doc-1", TenantID: h.tenant.ID, KnowledgeBaseID: "move-src-kb",
			Title: "Move Doc 1", Type: types.KnowledgeTypeManual,
			ParseStatus: types.ParseStatusCompleted, EnableStatus: "enabled",
		},
		{
			ID: "move-doc-2", TenantID: h.tenant.ID, KnowledgeBaseID: "move-src-kb",
			Title: "Move Doc 2", Type: types.KnowledgeTypeManual,
			ParseStatus: types.ParseStatusCompleted, EnableStatus: "enabled",
		},
	} {
		require.NoError(t, h.db.Create(doc).Error)
	}
	require.NoError(t, h.db.Create([]*types.Chunk{
		{
			ID: "move-chunk-1", TenantID: h.tenant.ID, KnowledgeID: "move-doc-1",
			KnowledgeBaseID: "move-src-kb", ChunkType: types.ChunkTypeText, Content: "chunk 1",
		},
		{
			ID: "move-chunk-2", TenantID: h.tenant.ID, KnowledgeID: "move-doc-2",
			KnowledgeBaseID: "move-src-kb", ChunkType: types.ChunkTypeText, Content: "chunk 2",
		},
	}).Error)

	// 1. Trigger multi-document move via HTTP handler -> SyncTaskExecutor -> KnowledgeService (redisClient == nil).
	// Regression: previously SaveKnowledgeMoveProgress panicked with HTTP 500 AND
	// ProcessKnowledgeMove panicked on saveKnowledgeMoveProgress after item 1, tearing the batch.
	moveResp := h.doJSON(t, http.MethodPost, "/api/v1/knowledge/move",
		`{"source_kb_id":"move-src-kb","target_kb_id":"move-dst-kb","knowledge_ids":["move-doc-1","move-doc-2"],"mode":"reuse_vectors"}`)
	require.Equal(t, http.StatusOK, moveResp.Code, moveResp.Body.String())

	var startBody struct {
		Success bool `json:"success"`
		Data    struct {
			TaskID         string `json:"task_id"`
			KnowledgeCount int    `json:"knowledge_count"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(moveResp.Body.Bytes(), &startBody))
	require.True(t, startBody.Success)
	require.Equal(t, 2, startBody.Data.KnowledgeCount)
	require.NotEmpty(t, startBody.Data.TaskID)

	// 2. Poll progress endpoint until completed
	var finalProgress types.KnowledgeMoveProgress
	require.Eventually(t, func() bool {
		pollResp := h.doJSON(t, http.MethodGet, "/api/v1/knowledge/move/progress/"+startBody.Data.TaskID, "")
		if pollResp.Code != http.StatusOK {
			return false
		}
		var pollBody struct {
			Success bool                        `json:"success"`
			Data    types.KnowledgeMoveProgress `json:"data"`
		}
		require.NoError(t, json.Unmarshal(pollResp.Body.Bytes(), &pollBody))
		finalProgress = pollBody.Data
		return pollBody.Data.Status == types.KBCloneStatusCompleted
	}, 5*time.Second, 20*time.Millisecond)

	require.Equal(t, 100, finalProgress.Progress)
	require.Equal(t, 2, finalProgress.Total)
	require.Equal(t, 2, finalProgress.Processed)
	require.Zero(t, finalProgress.Failed)

	// 3. Verify BOTH documents and chunks moved cleanly without batch tear
	var srcRemaining []types.Knowledge
	require.NoError(t, h.db.Where("knowledge_base_id = ?", "move-src-kb").Find(&srcRemaining).Error)
	require.Empty(t, srcRemaining)

	var dstMoved []types.Knowledge
	require.NoError(t, h.db.Where("knowledge_base_id = ?", "move-dst-kb").Find(&dstMoved).Error)
	require.Len(t, dstMoved, 2)
	for _, doc := range dstMoved {
		require.Equal(t, types.ParseStatusCompleted, doc.ParseStatus)
	}

	var dstChunks []types.Chunk
	require.NoError(t, h.db.Where("knowledge_base_id = ?", "move-dst-kb").Find(&dstChunks).Error)
	require.Len(t, dstChunks, 2)

	// 4. Unknown task ID returns 404 (not 500 panic)
	missingMoveID := utils.GenerateTaskID("kg_move", h.tenant.ID, "missing")
	missingResp := h.doJSON(t, http.MethodGet, "/api/v1/knowledge/move/progress/"+missingMoveID, "")
	require.Equal(t, http.StatusNotFound, missingResp.Code, missingResp.Body.String())
}
