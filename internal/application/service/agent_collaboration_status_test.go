package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type statusStreams struct {
	interfaces.StreamManager
	sessions []string
	messages []string
	events   []interfaces.StreamEvent
}

func (s *statusStreams) AppendEvent(_ context.Context, session, message string, evt interfaces.StreamEvent) error {
	s.sessions = append(s.sessions, session)
	s.messages = append(s.messages, message)
	s.events = append(s.events, evt)
	return nil
}

func TestCollaborationStatusRoutesCurrentParentAndReplaysDurableState(t *testing.T) {
	s, _, _, ctx := collaborationService(t)
	streams := &statusStreams{}
	s.BindRecovery(nil, nil, streams)
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "group"}
	require.NoError(t, s.Runtime.Store.Ensure(ctx, scope))
	_, err := s.Runtime.Spawn(ctx, scope, "spawn", "worker", "a", "PRIVATE_CONFIG", "PRIVATE_QUERY")
	require.NoError(t, err)
	require.Len(t, streams.events, 1)
	require.Equal(t, "parent", streams.sessions[0])
	require.Equal(t, "group", streams.messages[0])
	require.Equal(t, "collaboration_status", string(streams.events[0].Type))
	old, err := s.Runtime.Store.Read(ctx, scope)
	require.NoError(t, err)
	require.NoError(
		t,
		s.Runtime.Store.Change(
			ctx,
			scope,
			func(g *collaboration.Group) error { g.ParentMessageID = "new-parent-message"; return nil },
		),
	)
	s.publishStatus(ctx, old)
	last := len(streams.events) - 1
	require.Equal(t, "new-parent-message", streams.messages[last], "late callbacks must use the current parent")
	require.Greater(t, streams.events[last].Data["revision"].(int64), old.Revision)
	data, err := json.Marshal(streams.events[last].Data)
	require.NoError(t, err)
	require.NotContains(t, string(data), "PRIVATE_")
	replayed := &statusStreams{}
	s.BindRecovery(nil, nil, replayed)
	s.replayStatus(ctx, &collaboration.Binding{Runtime: s.Runtime, Scope: scope})
	require.Len(t, replayed.events, 1)
	require.Equal(t, streams.events[last].Data, replayed.events[0].Data)
	require.Equal(t, "new-parent-message", replayed.messages[0])
}

func TestCollaborationHistoryRestoresTerminalTasksWithoutStream(t *testing.T) {
	s, _, _, ctx := collaborationService(t)
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: "parent", ID: "group"}
	require.NoError(t, s.Runtime.Store.Ensure(ctx, scope))
	_, err := s.Runtime.Spawn(ctx, scope, "spawn", "worker", "a", "PRIVATE_CONFIG", "PRIVATE_QUERY")
	require.NoError(t, err)
	require.NoError(
		t,
		s.Runtime.Store.Change(
			ctx,
			scope,
			func(g *collaboration.Group) error { g.ParentMessageID = "resumed-parent"; return nil },
		),
	)
	require.NoError(t, s.Runtime.Cancel(ctx, scope))
	rows, err := s.HistoryStatuses(ctx, "parent", []string{"group", "resumed-parent", "unrelated"})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, rows["group"], rows["resumed-parent"])
	var view collaboration.StatusSnapshot
	require.NoError(t, json.Unmarshal(rows["group"], &view))
	require.Equal(t, "cancelled", view.Tasks[0].Status)
	require.Positive(t, view.Revision)
	require.NotContains(t, string(rows["group"]), "PRIVATE_")
	for _, wrong := range []types.Caller{{TenantID: 7, UserID: "other"}, {TenantID: 8, UserID: "u"}} {
		rows, err = s.HistoryStatuses(types.WithCaller(ctx, wrong), "parent", []string{"group"})
		require.NoError(t, err)
		require.Empty(t, rows)
	}
}

func TestCollaborationStatusDisplaysPinnedTemplateNameOnly(t *testing.T) {
	g := &collaboration.Group{Scope: collaboration.Scope{ID: "group"}, Members: []*collaboration.Member{{
		ID:         "worker",
		Name:       "hotlist_reader",
		ConfigJSON: `{"agent":{"name":"热榜解读官","system_prompt":"PRIVATE_PROMPT","api_key":"PRIVATE_KEY"}}`,

		Runs: []*collaboration.Run{{ID: "run", State: "running"}},
	}}}
	view := collaborationStatusView(g)
	require.Equal(t, "hotlist_reader", view.Tasks[0].Name)
	require.Equal(t, "热榜解读官", view.Tasks[0].DisplayName)
	data, err := json.Marshal(view)
	require.NoError(t, err)
	require.NotContains(t, string(data), "PRIVATE_")
}
