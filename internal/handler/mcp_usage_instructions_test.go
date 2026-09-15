package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/models/invoke/invoketest"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type usageMCPService struct {
	interfaces.MCPServiceService
	interfaces.MCPMetadataService
	service  *types.MCPService
	snapshot *types.MCPMetadata
	err      error
	tenant   uint64
	updated  *types.MCPService
}

func (s *usageMCPService) GetMCPServiceByID(_ context.Context, tenant uint64, _ string) (*types.MCPService, error) {
	s.tenant = tenant
	return s.service, nil
}

func (s *usageMCPService) GetMCPMetadata(_ context.Context, tenant uint64, _ string) (*types.MCPMetadata, error) {
	s.tenant = tenant
	return s.snapshot, s.err
}

func (s *usageMCPService) ListMCPMetadataSummaries(
	context.Context, uint64, []*types.MCPService,
) (map[string]*types.MCPMetadataSummary, error) {
	return nil, nil
}

func (s *usageMCPService) UpdateMCPService(_ context.Context, service *types.MCPService, _ map[string]bool) error {
	s.updated = service
	return nil
}

type usagePolicyService struct {
	interfaces.MCPToolApprovalService
	rows []*types.MCPToolApproval
}

func (s *usagePolicyService) ListByService(context.Context, uint64, string) ([]*types.MCPToolApproval, error) {
	return s.rows, nil
}

// usageModelService stubs the ModelService surface the generation handler
// uses. The chat call itself goes through invoke.Chat, so the "model" is an
// invoketest.Fake: BuildModelConfig hands out the fake's config, and tests
// enqueue responses (or errors) on it.
type usageModelService struct {
	interfaces.ModelService
	models   []*types.Model
	fake     *invoketest.Fake
	selected string
}

func (s *usageModelService) ListModels(context.Context, types.ModelType) ([]*types.Model, error) {
	return s.models, nil
}

func (s *usageModelService) GetModelByID(_ context.Context, id string) (*types.Model, error) {
	s.selected = id
	return &types.Model{ID: id, Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive}, nil
}

func (s *usageModelService) BuildModelConfig(context.Context, *types.Model) (*invoke.ModelConfig, error) {
	return s.fake.Config(), nil
}

func usageHandlerFixture(t *testing.T) (*MCPServiceHandler, *usageMCPService, *usageModelService) {
	svc := &usageMCPService{
		service: &types.MCPService{ID: "svc", Name: "Logs", Headers: types.MCPHeaders{"Authorization": "secret"}},
		snapshot: &types.MCPMetadata{
			ServerName: "log-server", Instructions: "Query logs by module or ID",
			Tools: []*types.MCPTool{
				{Name: "get_log", Description: "Query logs using module and time range"},
				{Name: "delete_log", Description: "Delete logs"},
			},
		},
	}
	models := &usageModelService{
		models: []*types.Model{
			{ID: "first", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive},
			{ID: "default", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive, IsDefault: true},
		},
		fake: invoketest.New(t),
	}
	models.fake.EnqueueResponse(invoke.ChatResponse{
		Content:      "  查询指定模块和时间范围内的日志。  ",
		FinishReason: "stop",
	})
	return &MCPServiceHandler{mcpServiceService: svc, modelService: models, mcpToolApprovalService: &usagePolicyService{
		rows: []*types.MCPToolApproval{{ToolName: "delete_log", Enabled: false}},
	}}, svc, models
}

func usageRequest(h *MCPServiceHandler, method, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		c.Next()
	})
	r.POST("/:id", h.GenerateMCPUsageInstructions)
	r.PUT("/:id", h.UpdateMCPService)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/svc", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestMCPUsageGeneration(t *testing.T) {
	h, svc, models := usageHandlerFixture(t)
	w := usageRequest(h, http.MethodPost, `{"language":"en-US"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, uint64(7), svc.tenant)
	require.Nil(t, svc.updated, "generation must not persist the result")
	require.Equal(t, "default", models.selected)
	require.Contains(t, w.Body.String(), `"usage_instructions":"查询指定模块和时间范围内的日志。"`)
	calls := models.fake.Calls()
	require.Len(t, calls, 1)
	messages := calls[0].Opts.Messages
	require.Len(t, messages, 2)
	require.Equal(t, invoke.RoleSystem, messages[0].Role)
	require.Contains(t, messages[0].Text(), "untrusted reference data")
	require.Contains(t, messages[0].Text(), "Output language: English.")
	require.Contains(t, messages[1].Text(), "get_log")
	require.Contains(t, messages[1].Text(), "module and time range")
	require.NotContains(t, messages[1].Text(), "delete_log")
	require.NotContains(t, messages[1].Text(), "secret")
	require.Empty(t, calls[0].Opts.Tools)
	require.NotNil(t, calls[0].Opts.Thinking)
	require.False(t, *calls[0].Opts.Thinking)
	require.Equal(t, 512, calls[0].Opts.MaxCompletionTokens)
}

func TestMCPUsageGenerationRejectsUnavailableInputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*usageMCPService, *usageModelService)
		status int
	}{
		{"missing service", func(s *usageMCPService, _ *usageModelService) { s.service = nil }, 404},
		{"not synced", func(s *usageMCPService, _ *usageModelService) { s.snapshot = nil }, 400},
		{"stale", func(s *usageMCPService, _ *usageModelService) { s.snapshot.Stale = true }, 400},
		{"oauth principal required", func(s *usageMCPService, _ *usageModelService) {
			s.err = types.ErrMCPOAuthPrincipalRequired
		}, 401},
		{"no enabled tools", func(s *usageMCPService, _ *usageModelService) {
			s.snapshot.Tools = s.snapshot.Tools[1:]
		}, 400},
		{"no chat model", func(_ *usageMCPService, m *usageModelService) { m.models = nil }, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, svc, models := usageHandlerFixture(t)
			tc.change(svc, models)
			w := usageRequest(h, http.MethodPost, `{}`)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Empty(t, models.fake.Calls())
		})
	}
}

func TestMCPUsageGenerationRejectsInvalidOutput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result *invoke.ChatResponse
		err    error
	}{
		{"empty", &invoke.ChatResponse{Content: " \n "}, nil},
		{"too long", &invoke.ChatResponse{Content: strings.Repeat("中", 501)}, nil},
		{"truncated", &invoke.ChatResponse{Content: "partial", FinishReason: "length"}, nil},
		{"upstream failure", nil, errors.New("secret upstream URL")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, models := usageHandlerFixture(t)
			// Swap in a fresh fake: the fixture pre-queues a success response,
			// and these cases must drive the call with their own outcome.
			models.fake = invoketest.New(t)
			if tc.err != nil {
				models.fake.EnqueueError(http.StatusInternalServerError, "secret upstream URL")
			} else {
				models.fake.EnqueueResponse(*tc.result)
			}
			w := usageRequest(h, http.MethodPost, `{}`)
			require.Equal(t, http.StatusServiceUnavailable, w.Code)
			require.NotContains(t, w.Body.String(), "secret")
		})
	}
}

func TestMCPUsageInputBoundsAndUntrustedData(t *testing.T) {
	_, svc, _ := usageHandlerFixture(t)
	svc.snapshot.Instructions = strings.Repeat("中", 10000)
	svc.snapshot.Tools = nil
	for i := 0; i < 1000; i++ {
		svc.snapshot.Tools = append(svc.snapshot.Tools, &types.MCPTool{
			Name: "tool", Description: strings.Repeat("界", 5000),
		})
	}
	input, err := buildMCPUsageInput(svc.service, svc.snapshot, nil)
	require.NoError(t, err)
	require.True(t, json.Valid([]byte(input)))
	require.Less(t, utf8.RuneCountInString(input), 31000)
	require.Contains(t, input, "omitted_tools")
	require.NotContains(t, input, "secret")
}

func TestMCPUsageUpdateRequiresNonBlankString(t *testing.T) {
	for _, body := range []string{
		`{"usage_instructions":""}`, `{"usage_instructions":" \n "}`,
		`{"usage_instructions":null}`, `{"usage_instructions":123}`,
		`{"usage_instructions":"` + strings.Repeat("中", 16001) + `"}`,
	} {
		h, svc, _ := usageHandlerFixture(t)
		w := usageRequest(h, http.MethodPut, body)
		require.Equal(t, http.StatusBadRequest, w.Code, body[:min(len(body), 80)])
		require.Nil(t, svc.updated)
	}
	for _, body := range []string{`{"usage_instructions":"  查询日志  "}`, `{"name":"Logs"}`} {
		h, svc, _ := usageHandlerFixture(t)
		w := usageRequest(h, http.MethodPut, body)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.NotNil(t, svc.updated)
		if strings.Contains(body, "usage_instructions") {
			require.Equal(t, "查询日志", svc.updated.UsageInstructions)
		}
	}
}
