package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

type recordingResumer struct{ prompts []*types.AgentPrompt }

func (r *recordingResumer) ResumeSuspendedRun(_ *gin.Context, p *types.AgentPrompt, _ bool, _ *time.Time) {
	r.prompts = append(r.prompts, p)
}

func postAgentPrompt(
	h *MCPServiceHandler, promptID, body string, principal types.Principal,
) (*httptest.ResponseRecorder, *gin.Context) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/agent/prompts/"+promptID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req.WithContext(types.WithPrincipal(req.Context(), principal))
	c.Params = gin.Params{{Key: "prompt_id", Value: promptID}}
	c.Set(types.TenantIDContextKey.String(), uint64(1))
	h.ResolveAgentPrompt(c)
	return w, c
}

func appErrorCode(t *testing.T, c *gin.Context) int {
	t.Helper()
	require.NotEmpty(t, c.Errors)
	var appErr *errors.AppError
	require.ErrorAs(t, c.Errors.Last().Err, &appErr)
	return appErr.HTTPCode
}

func TestResolveAgentPrompt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gate := approval.NewGate(&config.Config{Agent: &config.AgentConfig{
		ToolApprovalTimeoutSeconds: 60, PromptHotWaitSeconds: 1,
	}}, nil, nil)
	resumer := &recordingResumer{}
	h := &MCPServiceHandler{toolApprovalGate: gate, runResumer: resumer}
	owner := types.Principal{Type: "user", ID: "u1"}

	ans, err := gate.AskUser(context.Background(), approval.AskRequest{
		TenantID: 1, UserID: owner.StorageID(), SessionID: "s1", AssistantMessageID: "m1", ToolCallID: "tc1",
		EventBus: event.NewEventBus(), Question: types.AskUserPromptRequest{Question: "color?"},
		Snapshot: []byte(`{"version":1}`), SnapshotVersion: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, ans.Suspended)
	id := ans.PromptID

	_, c := postAgentPrompt(h, id, `{"kind":"tool_approval","response":{"approved":true}}`, owner)
	assert.Equal(t, http.StatusNotFound, appErrorCode(t, c), "a mismatched kind hides the prompt")

	_, c = postAgentPrompt(h, id, `{"kind":"mcp_oauth","response":{"authorized":true}}`, owner)
	assert.Equal(t, http.StatusBadRequest, appErrorCode(t, c), "OAuth cannot be claimed by the user")

	_, c = postAgentPrompt(h, id, `{"kind":"ask_user","response":{}}`, owner)
	assert.Equal(t, http.StatusBadRequest, appErrorCode(t, c), "an empty answer is invalid")

	_, c = postAgentPrompt(h, id, `{"kind":"ask_user","response":{"text":"red"}}`,
		types.Principal{Type: "user", ID: "u2"})
	assert.Equal(t, http.StatusBadRequest, appErrorCode(t, c), "only the prompt's user may answer")
	assert.Empty(t, resumer.prompts)

	w, c := postAgentPrompt(h, id, `{"kind":"ask_user","response":{"text":"red"}}`, owner)
	require.Empty(t, c.Errors)
	assert.JSONEq(t, `{"success":true,"resumed":true}`, w.Body.String())
	require.Len(t, resumer.prompts, 1)
	assert.Equal(t, id, resumer.prompts[0].ID)

	_, c = postAgentPrompt(h, id, `{"kind":"ask_user","response":{"text":"blue"}}`, owner)
	assert.Equal(t, http.StatusBadRequest, appErrorCode(t, c), "a prompt is answered once")
}
