package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type childTestStreams struct {
	interfaces.StreamManager
	events   []interfaces.StreamEvent
	sessions []string
	live     string
	resets   int
}

func (s *childTestStreams) AppendEvent(_ context.Context, session, _ string, e interfaces.StreamEvent) error {
	s.events = append(s.events, e)
	s.sessions = append(s.sessions, session)
	return nil
}

func (s *childTestStreams) SetLiveRun(_ context.Context, session, _, _ string) error {
	s.live = session
	return nil
}

func (s *childTestStreams) ClearLiveRun(context.Context, string, string) error {
	s.live = ""
	return nil
}

func (s *childTestStreams) ResetEvents(context.Context, string, string) error {
	s.resets++
	s.events = nil
	return nil
}

func TestChildStreamsAreIndependentAndCompleteAfterPersistence(t *testing.T) {
	for _, startupFailure := range []bool{false, true} {
		name := "success"
		if startupFailure {
			name = "startup_failure"
		}
		t.Run(name, func(t *testing.T) { testChildStream(t, startupFailure) })
	}
}

func testChildStream(t *testing.T, startupFailure bool) {
	s, _, messages, ctx := collaborationService(t)
	streams := &childTestStreams{}
	s.streams = streams
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "group"}
	require.NoError(t, s.Runtime.Store.Ensure(ctx, scope))
	config, err := s.resolve(ctx, "a", types.AssistantAccessReadOnly)
	require.NoError(t, err)
	_, err = s.Runtime.Spawn(ctx, scope, "spawn", "worker", "a", config, "test")
	require.NoError(t, err)
	var work collaboration.Work
	require.NoError(t, s.Runtime.Store.Change(ctx, scope, func(g *collaboration.Group) error {
		m := g.Members[0]
		r := m.Runs[0]
		r.State, r.Owner, r.Lease = "running", "owner", time.Now().Add(time.Minute)
		work = collaboration.Work{Scope: scope, Member: *m, Run: *r}
		return nil
	}))
	artifact := types.MessageArtifact{URL: "resource://abcdefghijklmnopqrstuv", FileName: "report.md", FileType: ".md"}
	completionReceived := false
	s.ChildEvents = func(_ context.Context,
		session *types.Session,
		answer *types.Message,
		bus *event.EventBus,
	) func(context.Context) error {
		bus.On(event.EventAgentThought, func(ctx context.Context, _ event.Event) error {
			require.Empty(t, messages.saved, "tokens must be observable before final persistence")
			return streams.AppendEvent(
				ctx,
				session.ID,
				answer.ID,
				interfaces.StreamEvent{Type: "thinking", Content: "live thought"},
			)
		})
		bus.On(event.EventAgentComplete, func(_ context.Context, e event.Event) error {
			completionReceived = true
			answer.Content += e.Data.(event.AgentCompleteData).FinalAnswer
			answer.Artifacts = types.MessageArtifacts{artifact}
			return nil
		})
		return func(ctx context.Context) error {
			require.True(t, completionReceived)
			require.Len(t, messages.saved, 1)
			if startupFailure {
				require.Equal(t, "executor unavailable", messages.saved[0].Content)
			} else {
				require.Equal(t, "final once", messages.saved[0].Content)
			}
			return streams.AppendEvent(ctx, session.ID, answer.ID, interfaces.StreamEvent{Type: "complete"})
		}
	}
	s.Bind(&collaborationSessions{run: func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		require.Equal(t, req.Session.ID, streams.live)
		require.NoError(t, bus.Emit(ctx, event.Event{Type: event.EventAgentThought}))
		if startupFailure {
			return errors.New("executor unavailable")
		}
		return bus.Emit(
			ctx,
			event.Event{
				Type: event.EventAgentComplete,
				Data: event.AgentCompleteData{MessageID: req.AssistantMessageID, FinalAnswer: "final once"},
			},
		)
	}})
	result, err := s.run(ctx, work)
	if startupFailure {
		require.EqualError(t, err, "executor unavailable")
		require.Equal(t, "executor unavailable", result.Answer)
	} else {
		require.NoError(t, err)
		require.Equal(t, "final once", result.Answer)
	}
	require.Equal(t, types.MessageArtifacts{artifact}, result.Artifacts)
	require.Empty(t, streams.live)
	require.Len(t, streams.events, 3)
	require.Equal(t, types.ResponseTypeAgentQuery, streams.events[0].Type)
	for _, session := range streams.sessions {
		require.Equal(t, work.Member.SessionID, session)
		require.NotEqual(t, "parent", session)
	}
}
