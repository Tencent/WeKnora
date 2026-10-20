package handler

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type collaborationPromptResumer struct {
	delegated bool
	calls     int
}

func (r *collaborationPromptResumer) ResumeSuspendedRun(*gin.Context, *types.AgentPrompt, bool, *time.Time) {
	r.calls++
}

func (r *collaborationPromptResumer) HandlesDelegatedPrompt(context.Context, *types.AgentPrompt) bool {
	return r.delegated
}

func TestDelegatedPromptAnswerDoesNotBypassDurableScheduler(t *testing.T) {
	for _, delegated := range []bool{true, false} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest("POST", "/agent/prompts/p", nil)
		resumer := &collaborationPromptResumer{delegated: delegated}
		handler := &MCPServiceHandler{runResumer: resumer}
		handler.finishResolve(ctx, "p", hitl.Resolution{Prompt: &types.AgentPrompt{ID: "p"}, Resume: true}, nil)
		require.Equal(t, 200, recorder.Code)
		if delegated {
			require.Zero(t, resumer.calls)
			require.Contains(t, recorder.Body.String(), `"resumed":false`)
		} else {
			require.Equal(t, 1, resumer.calls)
			require.Contains(t, recorder.Body.String(), `"resumed":true`)
		}
	}
}
