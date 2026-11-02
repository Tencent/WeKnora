package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type reparseBatchHandlerStub struct {
	interfaces.KnowledgeService
	batches     map[string]*types.ReparseBatch
	creates     int
	reads       int
	maxReadSize int
}

var _ service.ReparseBatchService = (*reparseBatchHandlerStub)(nil)

func (s *reparseBatchHandlerStub) GetKnowledgeBatch(
	_ context.Context,
	tenant uint64,
	ids []string,
) ([]*types.Knowledge, error) {
	s.reads++
	s.maxReadSize = max(s.maxReadSize, len(ids))
	rows := make([]*types.Knowledge, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, &types.Knowledge{ID: id, TenantID: tenant, KnowledgeBaseID: "kb"})
	}
	return rows, nil
}

func (s *reparseBatchHandlerStub) CreateReparseBatch(
	_ context.Context,
	tenant uint64,
	kb, mode, name string,
	_ []string,
) (*types.ReparseBatch, error) {
	s.creates++
	b := &types.ReparseBatch{ID: "batch-1", TenantID: tenant, KnowledgeBaseID: kb, Mode: mode, Name: name}
	s.batches[b.ID] = b
	return b, nil
}

func (s *reparseBatchHandlerStub) GetReparseBatch(
	_ context.Context,
	tenant uint64,
	id string,
) (*types.ReparseBatch, error) {
	b := s.batches[id]
	if b == nil || b.TenantID != tenant {
		return nil, gorm.ErrRecordNotFound
	}
	return b, nil
}

func (s *reparseBatchHandlerStub) FindTrackingReparseBatch(
	_ context.Context,
	tenant uint64,
	kb, _ string,
) (*types.ReparseBatch, error) {
	for _, b := range s.batches {
		if b.TenantID == tenant && b.KnowledgeBaseID == kb && b.Mode == "tracking_only" {
			return b, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (*reparseBatchHandlerStub) ListReparseBatches(
	context.Context,
	uint64,
	string,
	int,
	int,
) ([]*types.ReparseBatch, int64, error) {
	return nil, 0, nil
}

func (*reparseBatchHandlerStub) ListReparseBatchMembers(
	context.Context,
	uint64,
	string,
	int,
	int,
) ([]*types.ReparseBatchMember, int64, error) {
	return nil, 0, nil
}

func (*reparseBatchHandlerStub) RecordReparseMember(context.Context, string, string, string, string) error {
	return nil
}

func (s *reparseBatchHandlerStub) GetReparseBatchView(
	ctx context.Context,
	tenant uint64,
	id string,
) (*types.ReparseBatchView, error) {
	b, e := s.GetReparseBatch(ctx, tenant, id)
	return &types.ReparseBatchView{ReparseBatch: b}, e
}

func (*reparseBatchHandlerStub) ListReparseBatchMemberViews(
	context.Context,
	uint64,
	string,
	string,
	int,
	int,
) ([]*types.ReparseBatchMemberView, int64, error) {
	return nil, 0, nil
}

func (*reparseBatchHandlerStub) ListReparseBatchViews(
	context.Context,
	uint64,
	string,
	int,
	int,
) ([]*types.ReparseBatchView, int64, error) {
	return nil, 0, nil
}

func (*reparseBatchHandlerStub) ListReparseBatchViewsByIDs(
	context.Context,
	uint64,
	[]string,
) ([]*types.ReparseBatchView, error) {
	return nil, nil
}

func reparseBatchTestHandler(s *reparseBatchHandlerStub) *KnowledgeHandler {
	return &KnowledgeHandler{cfg: transferHandlerConfig(), kbService: transferHandlerKB(), kgService: s}
}

func TestRegisterReparseBatchTrackingOnlyDoesNotInvokeKnowledgeMutation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &reparseBatchHandlerStub{batches: map[string]*types.ReparseBatch{}}
	r := transferHandlerRouter(nil)
	r.POST("/transfer", reparseBatchTestHandler(s).RegisterReparseBatch)
	w := transferRequest(r, `{"kb_id":"kb","ids":["missing","missing"],"mode":"tracking_only"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, 1, s.creates)
	require.Zero(t, s.reads, "tracking registration must not read or mutate knowledge rows")
	// Same canonical selection is a registration read, not another create.
	w = transferRequest(r, `{"kb_id":"kb","ids":["missing"],"mode":"tracking_only"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, 1, s.creates)
}

func TestRegisterManagedReparseBatchAccepts50000WithBoundedReads(t *testing.T) {
	s := &reparseBatchHandlerStub{batches: map[string]*types.ReparseBatch{}}
	r := transferHandlerRouter(nil)
	r.POST("/transfer", reparseBatchTestHandler(s).RegisterReparseBatch)
	var ids strings.Builder
	ids.WriteByte('[')
	for i := range 50000 {
		if i > 0 {
			ids.WriteByte(',')
		}
		ids.WriteString(`"synthetic-` + strconv.Itoa(i) + `"`)
	}
	ids.WriteByte(']')
	w := transferRequest(r, `{"kb_id":"kb","ids":`+ids.String()+`,"mode":"managed"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, 1, s.creates)
	require.Equal(t, 250, s.reads)
	require.Equal(t, 200, s.maxReadSize)
}

func TestBatchReparseStillRejectsMoreThan200Members(t *testing.T) {
	s := &reparseBatchHandlerStub{batches: map[string]*types.ReparseBatch{}}
	r := transferHandlerRouter(nil)
	r.POST("/transfer", reparseBatchTestHandler(s).BatchReparseKnowledge)
	var ids strings.Builder
	ids.WriteByte('[')
	for i := range 201 {
		if i > 0 {
			ids.WriteByte(',')
		}
		ids.WriteString(`"synthetic-` + strconv.Itoa(i) + `"`)
	}
	ids.WriteByte(']')
	w := transferRequest(r, `{"kb_id":"kb","ids":`+ids.String()+`}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Zero(t, s.creates)
	require.Zero(t, s.reads)
}

func TestRegisterReparseBatchRejectsOversizedAndInvalidManifestInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &reparseBatchHandlerStub{batches: map[string]*types.ReparseBatch{}}
	h := reparseBatchTestHandler(s)
	r := transferHandlerRouter(nil)
	r.POST("/transfer", h.RegisterReparseBatch)
	var ids strings.Builder
	ids.WriteByte('[')
	for i := 0; i < 50001; i++ {
		if i > 0 {
			ids.WriteByte(',')
		}
		ids.WriteString(`"x` + strconv.Itoa(i) + `"`)
	}
	ids.WriteByte(']')
	w := transferRequest(r, `{"kb_id":"kb","ids":`+ids.String()+`,"mode":"managed"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	w = transferRequest(r, `{"kb_id":"kb","ids":[" bad"],"mode":"tracking_only"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestReparseBatchRejectsCrossTenantAndRestrictedAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, scope := range []*types.TenantAPIKeyScope{nil, {KnowledgeBaseIDs: types.StringArray{"allowed-kb"}}} {
		t.Run(strconv.FormatBool(scope != nil), func(t *testing.T) {
			tenant := uint64(8)
			if scope != nil {
				tenant = 7
			}
			s := &reparseBatchHandlerStub{batches: map[string]*types.ReparseBatch{
				"batch-1": {ID: "batch-1", TenantID: tenant, KnowledgeBaseID: "private-kb", Name: "private-name"},
			}}
			r := transferHandlerRouter(scope)
			h := reparseBatchTestHandler(s)
			r.GET("/batches/:batch_id", h.GetReparseBatch)
			r.GET("/batches/:batch_id/items", h.ListReparseBatchMembers)
			for _, path := range []string{"/batches/batch-1", "/batches/batch-1/items"} {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
				if scope == nil {
					require.Equal(t, http.StatusNotFound, w.Code)
				} else {
					require.Equal(t, http.StatusForbidden, w.Code)
				}
				require.NotContains(t, w.Body.String(), "private-name")
			}
		})
	}
}

func TestRegisterReparseBatchRequiresAPIKeyKnowledgeBaseScope(t *testing.T) {
	s := &reparseBatchHandlerStub{batches: map[string]*types.ReparseBatch{}}
	r := transferHandlerRouter(&types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"allowed-kb"}})
	r.POST("/transfer", reparseBatchTestHandler(s).RegisterReparseBatch)
	w := transferRequest(r, `{"kb_id":"private-kb","ids":["synthetic"],"mode":"tracking_only"}`)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Zero(t, s.creates)
}
