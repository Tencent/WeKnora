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

func TestScheduledPromptAnswerReattachesWithoutStartingChatRun(t *testing.T) {
	for _, suspended := range []bool{true, false} {
		t.Run(map[bool]string{true: "suspended", false: "still_running"}[suspended], func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest("POST", "/agent/prompts/p", nil)
			resumer := &collaborationPromptResumer{delegated: true}
			handler := &MCPServiceHandler{runResumer: resumer}
			queued := 0
			handler.SetScheduledPromptRouter(func(_ context.Context, prompt *types.AgentPrompt) bool {
				require.Equal(t, "p", prompt.ID)
				queued++
				return true
			})
			handler.finishResolve(ctx, "p", hitl.Resolution{
				Prompt: &types.AgentPrompt{ID: "p"}, Resume: suspended,
			}, nil)
			require.Equal(t, 200, recorder.Code)
			require.Equal(t, 1, queued)
			require.Zero(t, resumer.calls, "the scheduler must retain ownership of automation runs")
			if suspended {
				require.JSONEq(t, `{"success":true,"resumed":true}`, recorder.Body.String())
			} else {
				require.JSONEq(t, `{"success":true,"resumed":false}`, recorder.Body.String())
			}
		})
	}
}
