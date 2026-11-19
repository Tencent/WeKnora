package mcpserver

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

type askProgressTestSession struct {
	notifications chan mcp.JSONRPCNotification
}

func (s *askProgressTestSession) Initialize() {}

func (s *askProgressTestSession) Initialized() bool { return true }

func (s *askProgressTestSession) NotificationChannel() chan<- mcp.JSONRPCNotification {
	return s.notifications
}

func (s *askProgressTestSession) SessionID() string { return "ask-progress-test" }

func newAskProgressTestFixture(t *testing.T, session *askProgressTestSession) (*resourceURLFixture, context.Context) {
	t.Helper()
	f := newResourceURLFixture(t)
	f.srv.sessionService = &imageAskSessionService{}
	f.srv.messageService = &imageAskMessageService{}
	f.srv.agentService = &stubAgentService{agents: map[string]*types.CustomAgent{
		types.BuiltinQuickAnswerID: {ID: types.BuiltinQuickAnswerID, IsBuiltin: true},
	}}
	f.srv.mcp = mcpserver.NewMCPServer("test", "1")
	return f, f.srv.mcp.WithContext(f.ctx, session)
}

func TestAskSendsProgressWhenRequestIncludesToken(t *testing.T) {
	session := &askProgressTestSession{notifications: make(chan mcp.JSONRPCNotification, 8)}
	f, ctx := newAskProgressTestFixture(t, session)
	_, err := f.srv.handleAsk(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Meta:      &mcp.Meta{ProgressToken: "progress-token"},
		Arguments: map[string]any{"question": "Explain the diagram"},
	}})
	require.NoError(t, err)

	select {
	case notification := <-session.notifications:
		require.Equal(t, string(mcp.MethodNotificationProgress), notification.Method)
		require.Equal(t, "progress-token", notification.Params.AdditionalFields["progressToken"])
		require.Equal(t, float64(0), notification.Params.AdditionalFields["progress"])
		require.NotEmpty(t, notification.Params.AdditionalFields["message"])
	default:
		t.Fatal("expected an initial MCP progress notification")
	}
	require.Empty(t, session.notifications)
}

func TestAskDoesNotSendProgressWithoutToken(t *testing.T) {
	session := &askProgressTestSession{notifications: make(chan mcp.JSONRPCNotification, 8)}
	f, ctx := newAskProgressTestFixture(t, session)
	result, err := f.srv.handleAsk(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Arguments: map[string]any{"question": "Explain the diagram"},
	}})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)
	require.Empty(t, session.notifications)
}

func TestAskProgressNotificationsArePeriodic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reports := make(chan float64, 8)
	stop := startAskProgress(ctx, 5*time.Millisecond, func(progress float64) {
		reports <- progress
	})
	defer stop()

	require.Equal(t, float64(0), <-reports)
	require.Equal(t, float64(1), <-reports)
	require.Equal(t, float64(2), <-reports)
}

func TestAskProgressIsBestEffort(t *testing.T) {
	session := &askProgressTestSession{notifications: make(chan mcp.JSONRPCNotification)}
	f, ctx := newAskProgressTestFixture(t, session)
	result, err := f.srv.handleAsk(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Meta:      &mcp.Meta{ProgressToken: "progress-token"},
		Arguments: map[string]any{"question": "Explain the diagram"},
	}})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError)
}

type timeoutAskSessionService struct {
	interfaces.SessionService
	canceled chan struct{}
}

func (s *timeoutAskSessionService) CreateSession(_ context.Context, session *types.Session) (*types.Session, error) {
	session.ID = "session-timeout"
	return session, nil
}

func (s *timeoutAskSessionService) SetSessionOwnerID(context.Context, uint64, string, string) error {
	return nil
}

func (s *timeoutAskSessionService) KnowledgeQA(ctx context.Context, _ *types.QARequest, _ *event.EventBus) error {
	<-ctx.Done()
	close(s.canceled)
	return ctx.Err()
}

func TestAskUsesConfiguredTimeout(t *testing.T) {
	t.Setenv("WEKNORA_MCP_ASK_TIMEOUT", "25ms")
	session := &askProgressTestSession{notifications: make(chan mcp.JSONRPCNotification, 1)}
	f, ctx := newAskProgressTestFixture(t, session)
	waiting := &timeoutAskSessionService{canceled: make(chan struct{})}
	f.srv.sessionService = waiting

	result, err := f.srv.handleAsk(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Arguments: map[string]any{"question": "Wait for the configured deadline"},
	}})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)
	require.Contains(t, result.Content[0].(mcp.TextContent).Text, "answer timed out")
	select {
	case <-waiting.canceled:
	case <-time.After(time.Second):
		t.Fatal("QA was not canceled by the configured timeout")
	}
}

func TestAskTimeoutFromEnv(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "default", want: defaultAskTimeout},
		{name: "Go duration", value: "10m", want: 10 * time.Minute},
		{name: "bare seconds", value: "600", want: 10 * time.Minute},
		{name: "invalid falls back", value: "not-a-duration", want: defaultAskTimeout},
		{name: "zero falls back", value: "0", want: defaultAskTimeout},
		{name: "negative falls back", value: "-1m", want: defaultAskTimeout},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WEKNORA_MCP_ASK_TIMEOUT", tt.value)
			require.Equal(t, tt.want, askTimeoutFromEnv())
		})
	}
}
