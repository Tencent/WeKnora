package mcpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type failingAskSessionService struct {
	imageAskSessionService
	partial bool
	failure string
}

func (s *failingAskSessionService) KnowledgeQA(ctx context.Context, _ *types.QARequest, bus *event.EventBus) error {
	return s.reply(ctx, bus, "knowledge")
}

func (s *failingAskSessionService) AgentQA(ctx context.Context, _ *types.QARequest, bus *event.EventBus) error {
	return s.reply(ctx, bus, "agent")
}

func (s *failingAskSessionService) reply(ctx context.Context, bus *event.EventBus, origin string) error {
	if s.partial {
		if err := bus.EmitAndWait(ctx, event.Event{
			Type: event.EventAgentReferences,
			Data: event.AgentReferencesData{References: []*types.SearchResult{{
				ID: "chunk-1", KnowledgeID: "doc-1", Content: "Source passage",
			}}},
		}); err != nil {
			return err
		}
		if err := bus.EmitAndWait(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: origin + " answer fragment", Done: false},
		}); err != nil {
			return err
		}
	}
	failure := errors.New(origin + " provider stream failed")
	switch s.failure {
	case "event":
		return bus.EmitAndWait(ctx, event.Event{
			Type: event.EventError, Data: event.ErrorData{Error: failure.Error()},
		})
	case "return":
		return failure
	default:
		return bus.EmitAndWait(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Done: true},
		})
	}
}

type recordingAskMessageService struct {
	imageAskMessageService
	updated *types.Message
}

func (s *recordingAskMessageService) UpdateMessage(_ context.Context, message *types.Message) error {
	saved := *message
	s.updated = &saved
	return nil
}

func TestAskReportsFailuresAfterPartialAnswer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []struct {
		name string
		mode string
	}{
		{name: "knowledge", mode: "quick-answer"},
		{name: "agent", mode: "smart-reasoning"},
	} {
		for _, tc := range []struct {
			name    string
			partial bool
			failure string
		}{
			{name: "event error after partial answer", partial: true, failure: "event"},
			{name: "returned error after partial answer", partial: true, failure: "return"},
			{name: "error without an answer", failure: "event"},
			{name: "normal completion", partial: true},
		} {
			t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
				sessions := &failingAskSessionService{partial: tc.partial, failure: tc.failure}
				messages := &recordingAskMessageService{}
				agent := &types.CustomAgent{
					ID: "agent-1", TenantID: 1,
					Config: types.CustomAgentConfig{AgentMode: mode.mode},
				}
				kbs := &stubKBService{kbs: map[string]*types.KnowledgeBase{
					"kb-1": {ID: "kb-1", TenantID: 1},
				}}
				agents := &stubAgentService{agents: map[string]*types.CustomAgent{agent.ID: agent}}
				srv := NewServer(kbs, nil, nil, nil, sessions, messages, agents, nil,
					nil, nil, nil, nil, nil, nil, nil, nil)
				ep := &types.MCPEndpoint{
					ID: "ep-1", TenantID: 1, Enabled: true, RateLimitPerMinute: 100,
					DefaultAgentID: agent.ID, KnowledgeBaseIDs: types.StringArray{"kb-1"},
					Tools: types.StringArray{types.MCPEndpointToolAsk},
				}
				router := gin.New()
				router.POST("/mcp/:endpoint_id", func(c *gin.Context) {
					ctx := mcpCallContext(1, ep)
					c.Request = c.Request.WithContext(ctx)
					c.Next()
				}, gin.WrapH(srv.Handler()))
				response := rpc(t, router, "tools/call", map[string]any{
					"name": "ask", "arguments": map[string]any{"question": "Explain the deployment procedure"},
				})
				require.Nil(t, response["error"], "execution failures are tool results, not protocol errors")
				result, ok := response["result"].(map[string]any)
				require.True(t, ok, "%#v", response)
				isError, _ := result["isError"].(bool)
				require.Equal(t, tc.failure != "", isError, "%#v", result)

				if tc.failure != "" {
					content := result["content"].([]any)
					text := content[0].(map[string]any)["text"].(string)
					require.Contains(t, text, mode.name+" provider stream failed")
				} else {
					structured := result["structuredContent"].(map[string]any)
					require.Equal(t, mode.name+" answer fragment", structured["answer"])
				}
				require.NotNil(t, messages.updated)
				require.True(t, messages.updated.IsCompleted, "errors still finalize the message")
				if tc.partial {
					require.Equal(t, mode.name+" answer fragment", messages.updated.Content)
					require.Len(t, messages.updated.KnowledgeReferences, 1)
				}
			})
		}
	}
}
