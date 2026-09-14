package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type agentAvatarRepository struct {
	interfaces.CustomAgentRepository
	agent  *types.CustomAgent
	err    error
	writes int
}

func (r *agentAvatarRepository) GetAgentByID(context.Context, string, uint64) (*types.CustomAgent, error) {
	return r.agent, r.err
}

func (r *agentAvatarRepository) UpdateAgent(_ context.Context, a *types.CustomAgent) error {
	r.writes++
	r.agent = a
	return r.err
}

func (r *agentAvatarRepository) CreateAgent(_ context.Context, a *types.CustomAgent) error {
	r.writes++
	r.agent = a
	return r.err
}

func TestCustomAgentAvatarAndErrorResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, method, body, want string
		status, writes           int
		repoErr                  error
	}{
		{"omitted", "PUT", `{"name":"updated","config":{}}`, "🤖", 200, 1, nil},
		{"null", "PUT", `{"name":"updated","avatar":null}`, "🤖", 200, 1, nil},
		{"clear", "PUT", `{"name":"updated","avatar":""}`, "", 200, 1, nil},
		{"replace", "PUT", `{"name":"updated","avatar":"🐱"}`, "🐱", 200, 1, nil},
		{
			name: "64 unicode", method: "PUT",
			body: `{"name":"updated","avatar":"` + strings.Repeat("🤖", 64) + `"}`,
			want: strings.Repeat("🤖", 64), status: 200, writes: 1,
		},
		{"65 unicode update", "PUT", `{"name":"updated","avatar":"` + strings.Repeat("🤖", 65) + `"}`, "", 400, 0, nil},
		{"65 unicode create", "POST", `{"name":"updated","avatar":"` + strings.Repeat("🤖", 65) + `"}`, "", 400, 0, nil},
		{"config only still requires name", "PUT", `{"config":{}}`, "", 400, 0, nil},
		{"update hides database error", "PUT", `{"name":"updated"}`, "", 500, 0, errors.New("SQLSTATE secret_schema")},
		{"create hides database error", "POST", `{"name":"updated"}`, "", 500, 1, errors.New("SQLSTATE secret_schema")},
		{"get hides database error", "GET", ``, "", 500, 0, errors.New("SQLSTATE secret_schema")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &agentAvatarRepository{
				agent: &types.CustomAgent{ID: "custom-1", Name: "old", Avatar: "🤖", TenantID: 1},
				err:   tc.repoErr,
			}
			svc := service.NewCustomAgentService(repo, nil, nil, nil, nil, nil, nil)
			h := &CustomAgentHandler{service: svc}
			router := gin.New()
			router.Use(middleware.ErrorHandler())
			router.PUT("/agents/:id", h.UpdateAgent)
			router.POST("/agents", h.CreateAgent)
			router.GET("/agents/:id", h.GetAgent)
			path := "/agents/custom-1"
			if tc.method == http.MethodPost {
				path = "/agents"
			}
			req := httptest.NewRequest(tc.method, path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(context.WithValue(req.Context(), types.TenantIDContextKey, uint64(1)))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			require.Equal(t, tc.writes, repo.writes)
			if tc.status == http.StatusOK {
				var response struct {
					Data types.CustomAgent `json:"data"`
				}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
				require.Equal(t, tc.want, response.Data.Avatar)
				require.Equal(t, tc.want, repo.agent.Avatar)
			}
			if tc.repoErr != nil {
				require.NotContains(t, rec.Body.String(), "SQLSTATE")
				require.NotContains(t, rec.Body.String(), "secret_schema")
			}
		})
	}
}
