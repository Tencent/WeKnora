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
	"github.com/Tencent/WeKnora/internal/models/chat"
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

type usageChatBase interface{ chat.Chat }

type usageChatModel struct {
	usageChatBase
	messages []chat.Message
	options  *chat.ChatOptions
	result   *types.ChatResponse
	err      error
}

func (m *usageChatModel) Chat(
	_ context.Context, messages []chat.Message, opts *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.messages, m.options = messages, opts
	return m.result, m.err
}

type usageModelService struct {
	interfaces.ModelService
	models   []*types.Model
	chat     *usageChatModel
	selected string
}

func (s *usageModelService) ListModels(context.Context) ([]*types.Model, error) { return s.models, nil }
func (s *usageModelService) GetChatModel(_ context.Context, id string) (chat.Chat, error) {
	s.selected = id
	return s.chat, nil
}

func usageHandlerFixture() (*MCPServiceHandler, *usageMCPService, *usageModelService) {
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
		chat: &usageChatModel{result: &types.ChatResponse{
			Content: "  查询指定模块和时间范围内的日志。  ", FinishReason: "stop",
		}},
	}
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
	h, svc, models := usageHandlerFixture()
	w := usageRequest(h, http.MethodPost, `{"language":"en-US"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, uint64(7), svc.tenant)
	require.Nil(t, svc.updated, "generation must not persist the result")
	require.Equal(t, "default", models.selected)
	require.Contains(t, w.Body.String(), `"usage_instructions":"查询指定模块和时间范围内的日志。"`)
	require.Equal(t, "system", models.chat.messages[0].Role)
	require.Contains(t, models.chat.messages[0].Content, "untrusted reference data")
	require.Contains(t, models.chat.messages[0].Content, "Output language: English.")
	require.Contains(t, models.chat.messages[1].Content, "get_log")
	require.Contains(t, models.chat.messages[1].Content, "module and time range")
	require.NotContains(t, models.chat.messages[1].Content, "delete_log")
	require.NotContains(t, models.chat.messages[1].Content, "secret")
	require.Empty(t, models.chat.options.Tools)
	require.False(t, *models.chat.options.Thinking)
	require.Equal(t, 512, models.chat.options.MaxTokens)
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
			h, svc, models := usageHandlerFixture()
			tc.change(svc, models)
			w := usageRequest(h, http.MethodPost, `{}`)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Empty(t, models.chat.messages)
		})
	}
}

func TestMCPUsageGenerationRejectsInvalidOutput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result *types.ChatResponse
		err    error
	}{
		{"empty", &types.ChatResponse{Content: " \n "}, nil},
		{"too long", &types.ChatResponse{Content: strings.Repeat("中", 501)}, nil},
		{"truncated", &types.ChatResponse{Content: "partial", FinishReason: "length"}, nil},
		{"upstream failure", nil, errors.New("secret upstream URL")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, models := usageHandlerFixture()
			models.chat.result, models.chat.err = tc.result, tc.err
			w := usageRequest(h, http.MethodPost, `{}`)
			require.Equal(t, http.StatusServiceUnavailable, w.Code)
			require.NotContains(t, w.Body.String(), "secret")
		})
	}
}

func TestMCPUsageInputBoundsAndUntrustedData(t *testing.T) {
	_, svc, _ := usageHandlerFixture()
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
		h, svc, _ := usageHandlerFixture()
		w := usageRequest(h, http.MethodPut, body)
		require.Equal(t, http.StatusBadRequest, w.Code, body[:min(len(body), 80)])
		require.Nil(t, svc.updated)
	}
	for _, body := range []string{`{"usage_instructions":"  查询日志  "}`, `{"name":"Logs"}`} {
		h, svc, _ := usageHandlerFixture()
		w := usageRequest(h, http.MethodPut, body)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.NotNil(t, svc.updated)
		if strings.Contains(body, "usage_instructions") {
			require.Equal(t, "查询日志", svc.updated.UsageInstructions)
		}
	}
}
