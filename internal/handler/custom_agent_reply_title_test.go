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

type replyTitleAgentService struct {
	interfaces.CustomAgentService
	got *types.CustomAgent
}

func (s *replyTitleAgentService) CreateAgent(_ context.Context, a *types.CustomAgent) (*types.CustomAgent, error) {
	s.got = a
	return a, nil
}

func (s *replyTitleAgentService) UpdateAgent(
	_ context.Context, a *types.CustomAgent, _ *string,
) (*types.CustomAgent, error) {
	s.got = a
	return a, nil
}

func (s *replyTitleAgentService) GetAgentByID(_ context.Context, id string) (*types.CustomAgent, error) {
	return &types.CustomAgent{ID: id}, nil
}

func TestAgentReplyTitleCreateAndUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		for _, tc := range []struct {
			name, title string
			missing     bool
			status      int
		}{
			{name: "legacy", missing: true},
			{name: "custom", title: "Network assistant"},
			{name: "follow_name", title: ""},
			{name: "unicode_boundary", title: strings.Repeat("界", 100)},
			{name: "too_long", title: strings.Repeat("界", 101), status: http.StatusBadRequest},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				svc := &replyTitleAgentService{}
				h := &CustomAgentHandler{service: svc}
				r := gin.New()
				r.Use(middleware.ErrorHandler())
				r.POST("/agents", h.CreateAgent)
				r.PUT("/agents/:id", h.UpdateAgent)
				config := map[string]any{}
				if !tc.missing {
					config["feishu_reply_title"] = tc.title
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
				require.NotNil(t, svc.got.Config.FeishuReplyTitle)
				want := tc.title
				if tc.missing {
					want = types.DefaultFeishuReplyTitle
				}
				require.Equal(t, want, *svc.got.Config.FeishuReplyTitle)
			})
		}
	}
}
