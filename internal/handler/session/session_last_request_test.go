package session

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type lastRequestSessionService struct {
	interfaces.SessionService
	knowledgeErr error
	agentErr     error
	updateErr    error
	updates      int
	state        *types.SessionLastRequestState
}

func (s *lastRequestSessionService) KnowledgeQA(
	_ context.Context, _ *types.QARequest, _ *event.EventBus,
) error {
	return s.knowledgeErr
}

func (s *lastRequestSessionService) AgentQA(
	_ context.Context, _ *types.QARequest, _ *event.EventBus,
) error {
	return s.agentErr
}

func (s *lastRequestSessionService) UpdateSessionLastRequestState(
	_ context.Context, _ string, state *types.SessionLastRequestState,
) error {
	s.updates++
	if s.updateErr != nil {
		return s.updateErr
	}
	s.state = state
	return nil
}

func TestSessionLastRequestKnowledgeQAFailureDoesNotPersist(t *testing.T) {
	svc := &lastRequestSessionService{knowledgeErr: errors.New("qa failed")}
	h := &Handler{sessionService: svc}
	err := h.executeQAServiceAndPersist(context.Background(), &qaRequestContext{
		sessionID: "session-1", assistantMessage: &types.Message{},
	}, qaModeNormal, event.NewEventBus())
	require.Error(t, err)
	assert.Zero(t, svc.updates)
}

func TestSessionLastRequestAgentQAFailureDoesNotPersist(t *testing.T) {
	svc := &lastRequestSessionService{agentErr: errors.New("agent failed")}
	h := &Handler{sessionService: svc}
	err := h.executeQAServiceAndPersist(context.Background(), &qaRequestContext{
		sessionID: "session-1", assistantMessage: &types.Message{},
	}, qaModeAgent, event.NewEventBus())
	require.Error(t, err)
	assert.Zero(t, svc.updates)
}

func TestSessionLastRequestSuccessfulQAPersists(t *testing.T) {
	for _, mode := range []qaMode{qaModeNormal, qaModeAgent} {
		t.Run(modeName(mode), func(t *testing.T) {
			svc := &lastRequestSessionService{}
			h := &Handler{sessionService: svc}
			err := h.executeQAServiceAndPersist(context.Background(), &qaRequestContext{
				sessionID: "session-1", folderIDs: []string{"folder-1"},
				assistantMessage: &types.Message{},
			}, mode, event.NewEventBus())
			require.NoError(t, err)
			assert.Equal(t, 1, svc.updates)
			require.NotNil(t, svc.state)
			assert.Equal(t, []string{"folder-1"}, svc.state.FolderIDs)
		})
	}
}

func TestSessionLastRequestWriteFailureKeepsPriorStateAndMainResult(t *testing.T) {
	prior := &types.SessionLastRequestState{FolderIDs: []string{"old-folder"}}
	svc := &lastRequestSessionService{updateErr: errors.New("write failed"), state: prior}
	h := &Handler{sessionService: svc}
	err := h.executeQAServiceAndPersist(context.Background(), &qaRequestContext{
		sessionID: "session-1", folderIDs: []string{"new-folder"},
		assistantMessage: &types.Message{},
	}, qaModeNormal, event.NewEventBus())
	require.NoError(t, err)
	assert.Same(t, prior, svc.state)
}

func modeName(mode qaMode) string {
	if mode == qaModeAgent {
		return "agent"
	}
	return "knowledge"
}
