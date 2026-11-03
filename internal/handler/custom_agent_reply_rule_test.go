package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type replyRuleAgentService struct {
	interfaces.CustomAgentService
	got *types.CustomAgent
}

func (s *replyRuleAgentService) CreateAgent(_ context.Context, a *types.CustomAgent) (*types.CustomAgent, error) {
	s.got = a
	return a, nil
}

func (s *replyRuleAgentService) UpdateAgent(
	_ context.Context, a *types.CustomAgent, _ *string,
) (*types.CustomAgent, error) {
	s.got = a
	return a, nil
}

func (s *replyRuleAgentService) GetAgentByID(_ context.Context, id string) (*types.CustomAgent, error) {
	return &types.CustomAgent{ID: id}, nil
}

func TestAgentReplyRuleCreateAndUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		for _, tc := range []struct {
			name    string
			enabled bool
			missing bool
			status  int
		}{
			{name: "legacy", missing: true},
			{name: "enabled", enabled: true},
			{name: "disabled"},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				svc := &replyRuleAgentService{}
				h := &CustomAgentHandler{service: svc}
				r := gin.New()
				r.Use(middleware.ErrorHandler())
				r.POST("/agents", h.CreateAgent)
				r.PUT("/agents/:id", h.UpdateAgent)
				config := map[string]any{}
				if !tc.missing {
					config["feishu_group_reply_rule"] = tc.enabled
				}
				body, err := json.Marshal(map[string]any{"name": "智能体名称", "config": config})
				require.NoError(t, err)
				path := "/agents"
				status := http.StatusCreated
				if method == http.MethodPut {
					path += "/agent-1"
					status = http.StatusOK
				}
				if tc.status != 0 {
					status = tc.status
				}
				req := httptest.NewRequest(method, path, strings.NewReader(string(body)))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)
				require.Equal(t, status, rec.Code, rec.Body.String())
				if status == http.StatusBadRequest {
					require.Nil(t, svc.got)
					return
				}
				require.Equal(t, tc.enabled, svc.got.Config.FeishuGroupReplyRule)
			})
		}
	}
}
