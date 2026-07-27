package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func TestBatchFolderScopeMergeDeduplicatesStableOrder(t *testing.T) {
	require.Equal(t, []string{"explicit", "shared", "folder"},
		mergeKnowledgeScope([]string{" explicit ", "shared", "", "shared"}, []string{"shared", "folder", " "}))
}

type batchFolderPayloadEnqueuer struct {
	tasks []*asynq.Task
}

func (e *batchFolderPayloadEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	e.tasks = append(e.tasks, task)
	return &asynq.TaskInfo{ID: "task-" + task.Type()}, nil
}

type batchHTTPKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *batchHTTPKBService) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if s.kb != nil && s.kb.ID == id {
		return s.kb, nil
	}
	return nil, apprepo.ErrKnowledgeBaseNotFound
}

type batchScopeCall struct {
	kbID          string
	explicitIDs   []string
	folderIDs     []string
	enumerateFull bool
}

type batchMoveCall struct {
	kbID, targetID          string
	knowledgeIDs, folderIDs []string
}

type batchHTTPKnowledgeService struct {
	interfaces.KnowledgeService
	kbID          string
	folderResults map[string][]string
	knowledges    map[string]*types.Knowledge
	scopeCalls    []batchScopeCall
	tagWrites     []map[string][]string
	moveCalls     []batchMoveCall
	moveWrites    int
	conflictMoves bool
}

func (s *batchHTTPKnowledgeService) ResolveBatchKnowledgeScope(
	_ context.Context, kbID string, explicitIDs, folderIDs []string, enumerateFull bool,
) ([]string, error) {
	s.scopeCalls = append(s.scopeCalls, batchScopeCall{
		kbID: kbID, explicitIDs: append([]string(nil), explicitIDs...), folderIDs: append([]string(nil), folderIDs...),
		enumerateFull: enumerateFull,
	})
	resolved := append([]string(nil), explicitIDs...)
	for _, folderID := range folderIDs {
		resolved = mergeKnowledgeScope(resolved, s.folderResults[folderID])
	}
	return resolved, nil
}

func (s *batchHTTPKnowledgeService) GetKnowledgeBatch(
	_ context.Context, _ uint64, ids []string,
) ([]*types.Knowledge, error) {
	out := make([]*types.Knowledge, 0, len(ids))
	for _, id := range ids {
		if knowledge := s.knowledges[id]; knowledge != nil {
			out = append(out, knowledge)
		}
	}
	return out, nil
}

func (s *batchHTTPKnowledgeService) GetKnowledgeByIDOnly(_ context.Context, id string) (*types.Knowledge, error) {
	if knowledge := s.knowledges[id]; knowledge != nil {
		return knowledge, nil
	}
	return nil, apprepo.ErrKnowledgeNotFound
}

func (s *batchHTTPKnowledgeService) UpdateKnowledgeTagBatch(
	_ context.Context, kbID string, updates map[string][]string,
) error {
	if kbID != s.kbID {
		return types.ErrInvalidArgument
	}
	copyUpdates := make(map[string][]string, len(updates))
	for id, tags := range updates {
		copyUpdates[id] = append([]string(nil), tags...)
	}
	s.tagWrites = append(s.tagWrites, copyUpdates)
	return nil
}

func (s *batchHTTPKnowledgeService) MoveBatchToFolder(
	_ context.Context, kbID string, knowledgeIDs, folderIDs []string, targetID string,
) error {
	s.moveCalls = append(s.moveCalls, batchMoveCall{
		kbID: kbID, targetID: targetID, knowledgeIDs: append([]string(nil), knowledgeIDs...),
		folderIDs: append([]string(nil), folderIDs...),
	})
	if s.conflictMoves && len(folderIDs) == 2 && folderIDs[0] == "same-a" && folderIDs[1] == "same-b" {
		return types.ErrFolderAlreadyExists
	}
	s.moveWrites++
	return nil
}

func newBatchHTTPHandlerEngine(
	t *testing.T, service *batchHTTPKnowledgeService, enqueuer *batchFolderPayloadEnqueuer,
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	enabled := true
	kb := &types.KnowledgeBase{ID: service.kbID, TenantID: 7, CreatorID: "owner"}
	h := NewKnowledgeHandler(
		&config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}, service,
		&batchHTTPKBService{kb: kb}, nil, nil, enqueuer, nil,
	)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleAdmin)
		ctx = context.WithValue(ctx, types.UserIDContextKey, "owner")
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		c.Set(types.UserIDContextKey.String(), "owner")
		c.Next()
	})
	r.POST("/knowledge/batch-delete", h.BatchDeleteKnowledge)
	r.POST("/knowledge/batch-reparse", h.BatchReparseKnowledge)
	r.PUT("/knowledge/tags", h.UpdateKnowledgeTagBatch)
	r.POST("/knowledges/batch-move-folder", h.BatchMoveToFolder)
	return r
}

func serveBatchHTTP(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	return rec
}

func newBatchHTTPService() *batchHTTPKnowledgeService {
	const kbID = "kb-1"
	knowledges := map[string]*types.Knowledge{}
	for _, id := range []string{"explicit", "folder-preview", "legacy", "root-file", "tag-explicit", "tag-folder", "move-doc"} {
		knowledges[id] = &types.Knowledge{ID: id, TenantID: 7, KnowledgeBaseID: kbID}
	}
	return &batchHTTPKnowledgeService{
		kbID: kbID,
		folderResults: map[string][]string{
			"folder-1":   {"folder-preview"},
			"":           {"root-file"},
			"tag-folder": {"tag-folder"},
		},
		knowledges: knowledges,
	}
}

func decodeDeletePayload(t *testing.T, task *asynq.Task) types.KnowledgeListDeletePayload {
	t.Helper()
	var payload types.KnowledgeListDeletePayload
	require.NoError(t, json.Unmarshal(task.Payload(), &payload))
	return payload
}

func decodeReparsePayload(t *testing.T, task *asynq.Task) types.KnowledgeListReparsePayload {
	t.Helper()
	var payload types.KnowledgeListReparsePayload
	require.NoError(t, json.Unmarshal(task.Payload(), &payload))
	return payload
}

func TestBatchFolderDeleteKnowledgeServeHTTPPayloads(t *testing.T) {
	t.Run("folder preview never enters worker knowledge IDs", func(t *testing.T) {
		service, enqueuer := newBatchHTTPService(), &batchFolderPayloadEnqueuer{}
		rec := serveBatchHTTP(t, newBatchHTTPHandlerEngine(t, service, enqueuer), http.MethodPost,
			"/knowledge/batch-delete",
			`{"kb_id":"kb-1","knowledge_ids":["explicit"],"folder_ids":["folder-1"]}`)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.Len(t, enqueuer.tasks, 1)
		payload := decodeDeletePayload(t, enqueuer.tasks[0])
		require.Equal(t, uint64(7), payload.TenantID)
		require.Equal(t, "kb-1", payload.KnowledgeBaseID)
		require.Equal(t, []string{"explicit"}, payload.KnowledgeIDs)
		require.Equal(t, []string{"folder-1"}, payload.FolderIDs)
		require.Equal(t, batchScopeCall{
			kbID: "kb-1", explicitIDs: []string{"explicit"}, folderIDs: []string{"folder-1"}, enumerateFull: true,
		}, service.scopeCalls[0])
	})

	t.Run("empty named scope still enqueues folder cleanup", func(t *testing.T) {
		service, enqueuer := newBatchHTTPService(), &batchFolderPayloadEnqueuer{}
		rec := serveBatchHTTP(t, newBatchHTTPHandlerEngine(t, service, enqueuer), http.MethodPost,
			"/knowledge/batch-delete", `{"kb_id":"kb-1","folder_ids":["empty"]}`)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.Len(t, enqueuer.tasks, 1)
		payload := decodeDeletePayload(t, enqueuer.tasks[0])
		require.Empty(t, payload.KnowledgeIDs)
		require.Equal(t, []string{"empty"}, payload.FolderIDs)
	})

	t.Run("legacy ids only", func(t *testing.T) {
		service, enqueuer := newBatchHTTPService(), &batchFolderPayloadEnqueuer{}
		rec := serveBatchHTTP(t, newBatchHTTPHandlerEngine(t, service, enqueuer), http.MethodPost,
			"/knowledge/batch-delete", `{"kb_id":"kb-1","ids":["legacy"]}`)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		payload := decodeDeletePayload(t, enqueuer.tasks[0])
		require.Equal(t, []string{"legacy"}, payload.KnowledgeIDs)
		require.Empty(t, payload.FolderIDs)
	})

	t.Run("root scope payload", func(t *testing.T) {
		service, enqueuer := newBatchHTTPService(), &batchFolderPayloadEnqueuer{}
		rec := serveBatchHTTP(t, newBatchHTTPHandlerEngine(t, service, enqueuer), http.MethodPost,
			"/knowledge/batch-delete", `{"kb_id":"kb-1","folder_ids":["__root__"]}`)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		payload := decodeDeletePayload(t, enqueuer.tasks[0])
		require.Empty(t, payload.KnowledgeIDs)
		require.Equal(t, []string{types.FolderRootID}, payload.FolderIDs)
	})

	t.Run("large folder scope is enqueued for complete worker deletion", func(t *testing.T) {
		service, enqueuer := newBatchHTTPService(), &batchFolderPayloadEnqueuer{}
		for i := 0; i < 201; i++ {
			id := fmt.Sprintf("large-%03d", i)
			service.folderResults["large-folder"] = append(service.folderResults["large-folder"], id)
			service.knowledges[id] = &types.Knowledge{ID: id, TenantID: 7, KnowledgeBaseID: "kb-1"}
		}
		rec := serveBatchHTTP(t, newBatchHTTPHandlerEngine(t, service, enqueuer), http.MethodPost,
			"/knowledge/batch-delete", `{"kb_id":"kb-1","folder_ids":["large-folder"]}`)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.Len(t, enqueuer.tasks, 1)
		payload := decodeDeletePayload(t, enqueuer.tasks[0])
		require.Empty(t, payload.KnowledgeIDs)
		require.Equal(t, []string{"large-folder"}, payload.FolderIDs)
	})
}

func TestBatchFolderReparseKnowledgeServeHTTP(t *testing.T) {
	tests := []struct {
		name, body string
		wantIDs    []string
		wantTasks  int
	}{
		{"legacy ids", `{"kb_id":"kb-1","ids":["legacy"]}`, []string{"legacy"}, 1},
		{"knowledge_ids field", `{"kb_id":"kb-1","knowledge_ids":["explicit"]}`, []string{"explicit"}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service, enqueuer := newBatchHTTPService(), &batchFolderPayloadEnqueuer{}
			rec := serveBatchHTTP(t, newBatchHTTPHandlerEngine(t, service, enqueuer), http.MethodPost,
				"/knowledge/batch-reparse", tc.body)
			require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
			require.Len(t, enqueuer.tasks, tc.wantTasks)
			if tc.wantTasks == 1 {
				require.Equal(t, tc.wantIDs, decodeReparsePayload(t, enqueuer.tasks[0]).KnowledgeIDs)
			}
		})
	}
}

func TestBatchFolderUpdateKnowledgeTagServeHTTP(t *testing.T) {
	t.Run("legacy updates format", func(t *testing.T) {
		service := newBatchHTTPService()
		rec := serveBatchHTTP(t, newBatchHTTPHandlerEngine(t, service, &batchFolderPayloadEnqueuer{}), http.MethodPut,
			"/knowledge/tags", `{"updates":{"legacy":["old-tag"]}}`)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.Equal(t, []map[string][]string{{"legacy": {"old-tag"}}}, service.tagWrites)
	})
}

func TestBatchMoveToFolderServeHTTP(t *testing.T) {
	t.Run("successful request binds all fields", func(t *testing.T) {
		service := newBatchHTTPService()
		rec := serveBatchHTTP(t, newBatchHTTPHandlerEngine(t, service, &batchFolderPayloadEnqueuer{}), http.MethodPost,
			"/knowledges/batch-move-folder",
			`{"kb_id":"kb-1","knowledge_ids":["move-doc"],"folder_ids":["source-folder"],"target_folder_id":"target-folder"}`)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.Equal(t, []batchMoveCall{{
			kbID: "kb-1", targetID: "target-folder", knowledgeIDs: []string{"move-doc"}, folderIDs: []string{"source-folder"},
		}}, service.moveCalls)
		require.Equal(t, 1, service.moveWrites)
	})

	t.Run("selected same-name conflict is 409 with zero writes", func(t *testing.T) {
		service := newBatchHTTPService()
		service.conflictMoves = true
		rec := serveBatchHTTP(t, newBatchHTTPHandlerEngine(t, service, &batchFolderPayloadEnqueuer{}), http.MethodPost,
			"/knowledges/batch-move-folder",
			`{"kb_id":"kb-1","folder_ids":["same-a","same-b"],"target_folder_id":"target-folder"}`)
		require.Equal(t, http.StatusConflict, rec.Code, "body=%s", rec.Body.String())
		require.Len(t, service.moveCalls, 1)
		require.Zero(t, service.moveWrites)
	})
}
