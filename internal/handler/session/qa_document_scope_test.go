package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type qaDocumentScopeSession struct {
	interfaces.SessionService
	checked []string
}

func (s *qaDocumentScopeSession) GetOwnedSession(context.Context, string) (*types.Session, error) {
	return &types.Session{ID: "session", TenantID: 7}, nil
}

func (s *qaDocumentScopeSession) AuthorizeRestrictedAgentDocuments(_ context.Context, _ uint64, ids []string) error {
	s.checked = ids
	if len(ids) == 0 {
		return apperrors.NewBadRequestError("empty list")
	}
	return nil
}

func TestDocumentScopedAgentRequestCannotCombineWholeLibrary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, scope string
		mode        qaMode
		wantCode    int
	}{
		{"files", `"knowledge_ids":["allowed"]`, qaModeAgent, 0},
		{"same-library union", `"knowledge_ids":["allowed"],"knowledge_base_ids":["hr"]`, qaModeAgent, 400},
		{"empty whitelist", `"knowledge_ids":[]`, qaModeAgent, 400},
		{
			"file mention cannot replace empty list",
			`"knowledge_ids":[],"mentioned_items":[{"type":"file","id":"allowed"}]`, qaModeAgent, 400,
		},
		{
			"file mention alone retains old behavior",
			`"mentioned_items":[{"type":"file","id":"allowed"}]`, qaModeAgent, 403,
		},
		{"non-agent route", `"knowledge_ids":["allowed"]`, qaModeNormal, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &qaDocumentScopeSession{}
			h := &Handler{sessionService: service, customAgentService: &resolveOwnAgentStub{
				agent: &types.CustomAgent{
					ID: "agent", TenantID: 7,
					Config: types.CustomAgentConfig{AgentMode: types.AgentModeSmartReasoning},
				},
			}}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Params = gin.Params{{Key: "session_id", Value: "session"}}
			req := httptest.NewRequest(http.MethodPost, "/agent-chat/session",
				strings.NewReader(`{"query":"question","agent_id":"agent",`+tc.scope+`}`))
			req.Header.Set("Content-Type", "application/json")
			ctx.Request = req.WithContext(types.WithTenantAPIKeyScope(req.Context(), types.TenantAPIKeyScope{
				KeyID: 11, KnowledgeBaseIDs: []string{"hr"},
			}))
			parsed, _, err := h.parseQARequest(ctx, tc.mode)
			if tc.wantCode != 0 {
				var appErr *apperrors.AppError
				require.ErrorAs(t, err, &appErr)
				require.Equal(t, tc.wantCode, appErr.HTTPCode)
				require.Nil(t, parsed)
				return
			}
			require.NoError(t, err)
			require.Equal(t, []string{"allowed"}, service.checked)
			require.Equal(t, service.checked, parsed.knowledgeIDs)
			require.Empty(t, parsed.knowledgeBaseIDs)
			require.EqualValues(t, 11, parsed.assistantMessage.ExecutionContext.DocumentScopeAPIKeyID)
		})
	}
}
