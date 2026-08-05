package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type stubDataSourceService struct {
	interfaces.DataSourceService
	getSyncLogs         func(ctx context.Context, dsID string, limit int, offset int) ([]*types.SyncLog, error)
	getDataSource       func(ctx context.Context, id string) (*types.DataSource, error)
	validateCredentials func(ctx context.Context, connectorType string, credentials map[string]interface{}) error
	previewResources    func(
		ctx context.Context,
		connectorType string,
		dataSourceID string,
		credentials map[string]interface{},
		settings map[string]interface{},
		parentID string,
		validateOnly bool,
	) ([]types.Resource, error)
	reconfigureDataSource func(
		ctx context.Context,
		ds *types.DataSource,
		credentials map[string]interface{},
	) (*types.DataSource, error)
}

func (s *stubDataSourceService) GetSyncLogs(ctx context.Context, dsID string, limit int, offset int) ([]*types.SyncLog, error) {
	if s.getSyncLogs != nil {
		return s.getSyncLogs(ctx, dsID, limit, offset)
	}
	return nil, nil
}

func (s *stubDataSourceService) GetDataSource(ctx context.Context, id string) (*types.DataSource, error) {
	if s.getDataSource != nil {
		return s.getDataSource(ctx, id)
	}
	return nil, nil
}

func (s *stubDataSourceService) ValidateCredentials(
	ctx context.Context,
	connectorType string,
	credentials map[string]interface{},
) error {
	if s.validateCredentials != nil {
		return s.validateCredentials(ctx, connectorType, credentials)
	}
	return nil
}

func (s *stubDataSourceService) PreviewResources(
	ctx context.Context,
	connectorType string,
	dataSourceID string,
	credentials map[string]interface{},
	settings map[string]interface{},
	parentID string,
	validateOnly bool,
) ([]types.Resource, error) {
	if s.previewResources != nil {
		return s.previewResources(
			ctx,
			connectorType,
			dataSourceID,
			credentials,
			settings,
			parentID,
			validateOnly,
		)
	}
	return nil, nil
}

func (s *stubDataSourceService) ReconfigureDataSource(
	ctx context.Context,
	ds *types.DataSource,
	credentials map[string]interface{},
) (*types.DataSource, error) {
	if s.reconfigureDataSource != nil {
		return s.reconfigureDataSource(ctx, ds, credentials)
	}
	return ds, nil
}

type stubKBServiceForDS struct {
	interfaces.KnowledgeBaseService
	getByID func(ctx context.Context, id string) (*types.KnowledgeBase, error)
}

func (s *stubKBServiceForDS) GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error) {
	if s.getByID != nil {
		return s.getByID(ctx, id)
	}
	return nil, nil
}

func newDataSourceTestRouter(h *DataSourceHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errorCapture())
	r.Use(func(c *gin.Context) {
		if tenantID, ok := c.Request.Context().Value(types.TenantIDContextKey).(uint64); ok {
			c.Set(types.TenantIDContextKey.String(), tenantID)
		}
		c.Next()
	})
	r.POST("/datasource/validate-credentials", h.ValidateCredentials)
	r.POST("/datasource/preview-resources", h.PreviewResources)
	r.PUT("/datasource/:id/reconfigure", h.ReconfigureDataSource)
	r.GET("/datasource/:id/logs", h.GetSyncLogs)
	return r
}

func withDSCtx(req *http.Request, tenantID uint64) *http.Request {
	ctx := req.Context()
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	return req.WithContext(ctx)
}

func TestDataSource_GetSyncLogs_ValidLimitWithinBounds(t *testing.T) {
	var capturedLimit, capturedOffset int
	dsSvc := &stubDataSourceService{
		getDataSource: func(_ context.Context, id string) (*types.DataSource, error) {
			return &types.DataSource{ID: id, KnowledgeBaseID: "kb1"}, nil
		},
		getSyncLogs: func(_ context.Context, _ string, limit int, offset int) ([]*types.SyncLog, error) {
			capturedLimit = limit
			capturedOffset = offset
			return []*types.SyncLog{
				{ID: "log1", DataSourceID: "ds1"},
			}, nil
		},
	}
	kbSvc := &stubKBServiceForDS{
		getByID: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb1", TenantID: 1}, nil
		},
	}
	h := NewDataSourceHandler(dsSvc, kbSvc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/datasource/ds1/logs?limit=50&offset=25", nil)
	req = withDSCtx(req, 1)
	newDataSourceTestRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if capturedLimit != 50 {
		t.Fatalf("expected limit=50, got %d", capturedLimit)
	}
	if capturedOffset != 25 {
		t.Fatalf("expected offset=25, got %d", capturedOffset)
	}
}

func TestDataSource_GetSyncLogs_LimitExceedingMaximum(t *testing.T) {
	dsSvc := &stubDataSourceService{
		getDataSource: func(_ context.Context, id string) (*types.DataSource, error) {
			return &types.DataSource{ID: id, KnowledgeBaseID: "kb1"}, nil
		},
		getSyncLogs: func(_ context.Context, _ string, _ int, _ int) ([]*types.SyncLog, error) {
			t.Fatalf("service must not be called when limit exceeds maximum")
			return nil, nil
		},
	}
	kbSvc := &stubKBServiceForDS{
		getByID: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb1", TenantID: 1}, nil
		},
	}
	h := NewDataSourceHandler(dsSvc, kbSvc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/datasource/ds1/logs?limit=999", nil)
	req = withDSCtx(req, 1)
	newDataSourceTestRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for limit > 100, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	errMsg, ok := resp["error"].(string)
	if !ok || errMsg == "" {
		t.Fatalf("expected error message in response")
	}
	if errMsg != "limit must be between 1 and 100" {
		t.Fatalf("expected specific error message, got %q", errMsg)
	}
}

func TestDataSource_GetSyncLogs_MissingLimitDefaultsCorrectly(t *testing.T) {
	var capturedLimit, capturedOffset int
	dsSvc := &stubDataSourceService{
		getDataSource: func(_ context.Context, id string) (*types.DataSource, error) {
			return &types.DataSource{ID: id, KnowledgeBaseID: "kb1"}, nil
		},
		getSyncLogs: func(_ context.Context, _ string, limit int, offset int) ([]*types.SyncLog, error) {
			capturedLimit = limit
			capturedOffset = offset
			return []*types.SyncLog{}, nil
		},
	}
	kbSvc := &stubKBServiceForDS{
		getByID: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb1", TenantID: 1}, nil
		},
	}
	h := NewDataSourceHandler(dsSvc, kbSvc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/datasource/ds1/logs", nil)
	req = withDSCtx(req, 1)
	newDataSourceTestRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if capturedLimit != 10 {
		t.Fatalf("expected default limit=10, got %d", capturedLimit)
	}
	if capturedOffset != 0 {
		t.Fatalf("expected default offset=0 (page 1), got %d", capturedOffset)
	}
}

func TestDataSource_GetSyncLogs_NonNumericLimitRejected(t *testing.T) {
	dsSvc := &stubDataSourceService{
		getDataSource: func(_ context.Context, id string) (*types.DataSource, error) {
			return &types.DataSource{ID: id, KnowledgeBaseID: "kb1"}, nil
		},
		getSyncLogs: func(_ context.Context, _ string, _ int, _ int) ([]*types.SyncLog, error) {
			t.Fatalf("service must not be called with non-numeric limit")
			return nil, nil
		},
	}
	kbSvc := &stubKBServiceForDS{
		getByID: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb1", TenantID: 1}, nil
		},
	}
	h := NewDataSourceHandler(dsSvc, kbSvc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/datasource/ds1/logs?limit=abc", nil)
	req = withDSCtx(req, 1)
	newDataSourceTestRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-numeric limit, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDataSource_GetSyncLogs_ZeroLimitRejected(t *testing.T) {
	dsSvc := &stubDataSourceService{
		getDataSource: func(_ context.Context, id string) (*types.DataSource, error) {
			return &types.DataSource{ID: id, KnowledgeBaseID: "kb1"}, nil
		},
		getSyncLogs: func(_ context.Context, _ string, _ int, _ int) ([]*types.SyncLog, error) {
			t.Fatalf("service must not be called with limit=0")
			return nil, nil
		},
	}
	kbSvc := &stubKBServiceForDS{
		getByID: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb1", TenantID: 1}, nil
		},
	}
	h := NewDataSourceHandler(dsSvc, kbSvc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/datasource/ds1/logs?limit=0", nil)
	req = withDSCtx(req, 1)
	newDataSourceTestRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for limit=0, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDataSource_GetSyncLogs_NegativeLimitRejected(t *testing.T) {
	dsSvc := &stubDataSourceService{
		getDataSource: func(_ context.Context, id string) (*types.DataSource, error) {
			return &types.DataSource{ID: id, KnowledgeBaseID: "kb1"}, nil
		},
		getSyncLogs: func(_ context.Context, _ string, _ int, _ int) ([]*types.SyncLog, error) {
			t.Fatalf("service must not be called with negative limit")
			return nil, nil
		},
	}
	kbSvc := &stubKBServiceForDS{
		getByID: func(_ context.Context, _ string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb1", TenantID: 1}, nil
		},
	}
	h := NewDataSourceHandler(dsSvc, kbSvc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/datasource/ds1/logs?limit=-5", nil)
	req = withDSCtx(req, 1)
	newDataSourceTestRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for negative limit, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDataSource_ValidateCredentialsPropagatesTenantIdentity(t *testing.T) {
	called := false
	dsSvc := &stubDataSourceService{
		validateCredentials: func(
			ctx context.Context,
			connectorType string,
			credentials map[string]interface{},
		) error {
			called = true
			if got, _ := ctx.Value(types.TenantIDContextKey).(uint64); got != 42 {
				t.Fatalf("tenant context = %d, want 42", got)
			}
			if connectorType != types.ConnectorTypeDingTalk {
				t.Fatalf("connector type = %q", connectorType)
			}
			if credentials["app_key"] != "candidate-app" {
				t.Fatalf("credentials = %v", credentials)
			}
			return nil
		},
	}
	h := NewDataSourceHandler(dsSvc, &stubKBServiceForDS{})
	body := `{"type":"dingtalk","credentials":{"app_key":"candidate-app"}}`
	req := httptest.NewRequest(
		http.MethodPost,
		"/datasource/validate-credentials",
		bytes.NewBufferString(body),
	)
	req.Header.Set("Content-Type", "application/json")
	req = withDSCtx(req, 42)
	w := httptest.NewRecorder()

	newDataSourceTestRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if !called {
		t.Fatal("ValidateCredentials service was not called")
	}
}

func TestDataSource_PreviewResourcesUsesCandidateRequestAndTenant(t *testing.T) {
	dsSvc := &stubDataSourceService{
		previewResources: func(
			ctx context.Context,
			connectorType string,
			dataSourceID string,
			credentials map[string]interface{},
			settings map[string]interface{},
			parentID string,
			validateOnly bool,
		) ([]types.Resource, error) {
			if got, _ := ctx.Value(types.TenantIDContextKey).(uint64); got != 43 {
				t.Fatalf("tenant context = %d, want 43", got)
			}
			if connectorType != types.ConnectorTypeDingTalk ||
				dataSourceID != "" ||
				credentials["app_key"] != "candidate-app" ||
				settings["region"] != "candidate-region" ||
				parentID != "candidate-parent" ||
				validateOnly {
				t.Fatalf(
					"preview request = type:%q ds:%q credentials:%v settings:%v parent:%q validate:%v",
					connectorType,
					dataSourceID,
					credentials,
					settings,
					parentID,
					validateOnly,
				)
			}
			return []types.Resource{{ExternalID: "candidate-resource"}}, nil
		},
	}
	h := NewDataSourceHandler(dsSvc, &stubKBServiceForDS{})
	body := `{
		"type":"dingtalk",
		"credentials":{"app_key":"candidate-app"},
		"settings":{"region":"candidate-region"},
		"parent_id":"candidate-parent"
	}`
	req := httptest.NewRequest(
		http.MethodPost,
		"/datasource/preview-resources",
		bytes.NewBufferString(body),
	)
	req.Header.Set("Content-Type", "application/json")
	req = withDSCtx(req, 43)
	w := httptest.NewRecorder()

	newDataSourceTestRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("candidate-resource")) {
		t.Fatalf("preview response = %s", w.Body.String())
	}
}

func TestDataSource_PreviewResourcesAllowsOwnedStoredCredentialsAndValidateOnly(t *testing.T) {
	const (
		dataSourceID = "ds-preview-owned"
		kbID         = "kb-preview-owned"
	)
	dsSvc := &stubDataSourceService{
		getDataSource: func(_ context.Context, id string) (*types.DataSource, error) {
			return &types.DataSource{
				ID:              id,
				TenantID:        45,
				KnowledgeBaseID: kbID,
				Type:            types.ConnectorTypeRSS,
			}, nil
		},
		previewResources: func(
			ctx context.Context,
			connectorType string,
			gotDataSourceID string,
			credentials map[string]interface{},
			settings map[string]interface{},
			parentID string,
			validateOnly bool,
		) ([]types.Resource, error) {
			if got, _ := ctx.Value(types.TenantIDContextKey).(uint64); got != 45 {
				t.Fatalf("tenant context = %d, want 45", got)
			}
			if connectorType != types.ConnectorTypeRSS ||
				gotDataSourceID != dataSourceID ||
				credentials != nil ||
				settings["feed_urls"] != "https://candidate.example/feed.xml" ||
				parentID != "" ||
				!validateOnly {
				t.Fatalf(
					"stored preview request = type:%q ds:%q credentials:%v settings:%v parent:%q validate:%v",
					connectorType,
					gotDataSourceID,
					credentials,
					settings,
					parentID,
					validateOnly,
				)
			}
			return []types.Resource{}, nil
		},
	}
	kbSvc := &stubKBServiceForDS{
		getByID: func(_ context.Context, id string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: id, TenantID: 45}, nil
		},
	}
	h := NewDataSourceHandler(dsSvc, kbSvc)
	body := `{
		"type":"rss",
		"data_source_id":"ds-preview-owned",
		"credentials":null,
		"settings":{"feed_urls":"https://candidate.example/feed.xml"},
		"validate_only":true
	}`
	req := httptest.NewRequest(
		http.MethodPost,
		"/datasource/preview-resources",
		bytes.NewBufferString(body),
	)
	req.Header.Set("Content-Type", "application/json")
	req = withDSCtx(req, 45)
	w := httptest.NewRecorder()

	newDataSourceTestRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDataSource_ReconfigureForcesOwnedIdentityAndUsesOneServiceCall(t *testing.T) {
	const (
		dataSourceID = "ds-owned"
		kbID         = "kb-owned"
	)
	calls := 0
	dsSvc := &stubDataSourceService{
		getDataSource: func(_ context.Context, id string) (*types.DataSource, error) {
			return &types.DataSource{
				ID:              id,
				TenantID:        44,
				KnowledgeBaseID: kbID,
				Type:            types.ConnectorTypeDingTalk,
			}, nil
		},
		reconfigureDataSource: func(
			_ context.Context,
			ds *types.DataSource,
			credentials map[string]interface{},
		) (*types.DataSource, error) {
			calls++
			if ds.ID != dataSourceID ||
				ds.TenantID != 44 ||
				ds.KnowledgeBaseID != kbID ||
				ds.Type != types.ConnectorTypeDingTalk {
				t.Fatalf("owned identity was not enforced: %+v", ds)
			}
			if credentials["app_key"] != "candidate-app" {
				t.Fatalf("credentials = %v", credentials)
			}
			return ds, nil
		},
	}
	kbSvc := &stubKBServiceForDS{
		getByID: func(_ context.Context, id string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: id, TenantID: 44}, nil
		},
	}
	h := NewDataSourceHandler(dsSvc, kbSvc)
	body := `{
		"data_source":{
			"id":"attacker-id",
			"tenant_id":999,
			"knowledge_base_id":"attacker-kb",
			"type":"notion",
			"name":"Updated",
			"config":{"type":"dingtalk","resource_ids":["candidate-resource"]}
		},
		"credentials":{"app_key":"candidate-app"}
	}`
	req := httptest.NewRequest(
		http.MethodPut,
		"/datasource/"+dataSourceID+"/reconfigure",
		bytes.NewBufferString(body),
	)
	req.Header.Set("Content-Type", "application/json")
	req = withDSCtx(req, 44)
	w := httptest.NewRecorder()

	newDataSourceTestRouter(h).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if calls != 1 {
		t.Fatalf("ReconfigureDataSource calls = %d, want 1", calls)
	}
}
