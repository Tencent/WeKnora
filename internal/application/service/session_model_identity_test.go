package service

import (
	"context"
	"errors"
	"testing"
	"time"

	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type identityChat = chat.Chat

type sessionIdentityModel struct {
	identityChat
	seen chan string
	err  error
}

func (m *sessionIdentityModel) Chat(
	ctx context.Context, _ []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	id, _ := types.SessionIDFromContext(ctx)
	m.seen <- id
	return nil, m.err
}

func TestGenerateTitlePropagatesBusinessSession(t *testing.T) {
	for _, async := range []bool{false, true} {
		name := "sync"
		if async {
			name = "async"
		}
		t.Run(name, func(t *testing.T) {
			model := &sessionIdentityModel{seen: make(chan string, 1), err: errors.New("stop after model call")}
			svc := &sessionService{
				cfg:          &config.Config{Conversation: &config.ConversationConfig{}},
				modelService: &stubModelService{chatModel: model},
			}
			session := &types.Session{ID: "title-session"}
			ctx := types.WithSessionID(context.Background(), "parent-session")
			if async {
				svc.GenerateTitleAsync(ctx, session, "hello", "model", nil)
			} else {
				_, err := svc.GenerateTitle(ctx, session, []types.Message{{Role: "user", Content: "hello"}}, "model")
				require.ErrorIs(t, err, model.err)
			}
			select {
			case id := <-model.seen:
				require.Equal(t, session.ID, id)
			case <-time.After(5 * time.Second):
				t.Fatal("title generation did not reach the model")
			}
		})
	}
}

type sessionIdentityPlugin struct{ seen []string }

func (p *sessionIdentityPlugin) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHAT_COMPLETION}
}

func (p *sessionIdentityPlugin) OnEvent(
	ctx context.Context, _ types.EventType, _ *types.ChatManage, next func() *chatpipeline.PluginError,
) *chatpipeline.PluginError {
	id, _ := types.SessionIDFromContext(ctx)
	p.seen = append(p.seen, id)
	return next()
}

func TestKnowledgeQAByEventPropagatesBusinessSession(t *testing.T) {
	plugin := &sessionIdentityPlugin{}
	events := chatpipeline.NewEventManager()
	events.Register(plugin)
	svc := &sessionService{eventManager: events}
	ctx := context.Background()
	for _, id := range []string{"session-a", "session-a", "session-b", ""} {
		cm := &types.ChatManage{}
		cm.SessionID = id
		require.NoError(t, svc.KnowledgeQAByEvent(ctx, cm, plugin.ActivationEvents()))
	}
	require.Equal(t, []string{"session-a", "session-a", "session-b", ""}, plugin.seen)
}
