package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type transferHandlerKnowledge struct{ interfaces.KnowledgeService }

func (*transferHandlerKnowledge) SaveKBCloneProgress(context.Context, *types.KBCloneProgress) error {
	return nil
}

func (*transferHandlerKnowledge) SaveKnowledgeMoveProgress(context.Context, *types.KnowledgeMoveProgress) error {
	return nil
}

func (*transferHandlerKnowledge) GetKnowledgeByID(_ context.Context, id string) (*types.Knowledge, error) {
	return &types.Knowledge{
		ID:              id,
		TenantID:        7,
		KnowledgeBaseID: "source",
		ParseStatus:     types.ParseStatusCompleted,
	}, nil
}

func transferHandlerRouter(scope *types.TenantAPIKeyScope) *gin.Engine {
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		ctx := types.WithCaller(
			c.Request.Context(),
			types.Caller{TenantID: 7, UserID: "user", Role: types.TenantRoleContributor},
		)
		ctx = types.WithExecutionTenant(ctx, 7)
		if scope != nil {
			ctx = types.WithTenantAPIKeyScope(ctx, *scope)
		}
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	return r
}

func transferHandlerConfig() *config.Config {
	enabled := true
	return &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}
}

func transferHandlerKB() *stubKBService {
	return &stubKBService{get: func(_ context.Context, id string) (*types.KnowledgeBase, error) {
		return &types.KnowledgeBase{ID: id, TenantID: 7, CreatorID: "user"}, nil
	}}
}

func transferRequest(r *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/transfer", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCopyAdmissionReservesDestinationAndPreservesCreator(t *testing.T) {
	q := &documentDeleteEnqueuer{}
	h := &KnowledgeBaseHandler{
		cfg:              transferHandlerConfig(),
		service:          transferHandlerKB(),
		knowledgeService: &transferHandlerKnowledge{},
		asynqClient:      q,
	}
	r := transferHandlerRouter(nil)
	r.POST("/transfer", h.CopyKnowledgeBase)
	w := transferRequest(r, `{"source_id":"source"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NotNil(t, q.task)
	var payload types.KBClonePayload
	require.NoError(t, json.Unmarshal(q.task.Payload(), &payload))
	require.NotEmpty(t, payload.TargetID)
	require.NotEqual(t, "source", payload.TargetID)
	require.True(t, payload.CreateTarget)
	require.Equal(t, "user", payload.CreatorID)
	require.Equal(t, uint64(7), payload.TenantID)
	require.Contains(t, w.Body.String(), payload.TargetID)
}

func TestCopyAdmissionRequiresExistingDestinationOwnership(t *testing.T) {
	q := &documentDeleteEnqueuer{}
	kb := &stubKBService{get: func(_ context.Context, id string) (*types.KnowledgeBase, error) {
		return &types.KnowledgeBase{ID: id, TenantID: 7, CreatorID: "someone-else"}, nil
	}}
	h := &KnowledgeBaseHandler{
		cfg:              transferHandlerConfig(),
		service:          kb,
		knowledgeService: &transferHandlerKnowledge{},
		asynqClient:      q,
	}
	r := transferHandlerRouter(nil)
	r.POST("/transfer", h.CopyKnowledgeBase)
	w := transferRequest(r, `{"source_id":"source","target_id":"target"}`)
	require.Equal(t, 403, w.Code, w.Body.String())
	require.Nil(t, q.task)
}

func TestCopyAdmissionKeepsManageCapabilityForNewDestination(t *testing.T) {
	for _, capability := range []string{"manage_kbs", "ingest"} {
		t.Run(capability, func(t *testing.T) {
			q := &documentDeleteEnqueuer{}
			h := &KnowledgeBaseHandler{
				cfg:              transferHandlerConfig(),
				service:          transferHandlerKB(),
				knowledgeService: &transferHandlerKnowledge{},
				asynqClient:      q,
			}
			r := transferHandlerRouter(
				&types.TenantAPIKeyScope{
					Capabilities:     types.StringArray{capability},
					KnowledgeBaseIDs: types.StringArray{"source"},
				},
			)
			r.POST("/transfer", h.CopyKnowledgeBase)
			w := transferRequest(r, `{"source_id":"source"}`)
			if capability == "manage_kbs" {
				require.Equal(t, 200, w.Code, w.Body.String())
				require.NotNil(t, q.task)
			} else {
				require.Equal(t, 403, w.Code, w.Body.String())
				require.Nil(t, q.task)
			}
		})
	}
}

func TestMoveAdmissionDeduplicatesIDsAndRequiresBothKBs(t *testing.T) {
	for _, both := range []bool{true, false} {
		ids := types.StringArray{"source"}
		if both {
			ids = append(ids, "target")
		}
		q := &documentDeleteEnqueuer{}
		h := &KnowledgeHandler{
			cfg:         transferHandlerConfig(),
			kbService:   transferHandlerKB(),
			kgService:   &transferHandlerKnowledge{},
			asynqClient: q,
		}
		r := transferHandlerRouter(
			&types.TenantAPIKeyScope{Capabilities: types.StringArray{"ingest"}, KnowledgeBaseIDs: ids},
		)
		r.POST("/transfer", h.MoveKnowledge)
		w := transferRequest(
			r,
			`{"source_kb_id":"source","target_kb_id":"target","knowledge_ids":["doc","doc"],"mode":"reuse_vectors"}`,
		)
		if !both {
			require.Equal(t, 403, w.Code, w.Body.String())
			require.Nil(t, q.task)
			continue
		}
		require.Equal(t, 200, w.Code, w.Body.String())
		var payload types.KnowledgeMovePayload
		require.NoError(t, json.Unmarshal(q.task.Payload(), &payload))
		require.Equal(t, []string{"doc"}, payload.KnowledgeIDs)
	}
}
