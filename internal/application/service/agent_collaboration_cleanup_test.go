package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func createCleanupSession(t *testing.T, db *gorm.DB, id, kind string) {
	t.Helper()
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
		ID: id, TenantID: 7, UserID: "u", Kind: kind, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}).Error)
}

func createCleanupGroup(
	t *testing.T, s *AgentCollaboration, sessionID, id, status string, members []*collaboration.Member,
) collaboration.Scope {
	t.Helper()
	ctx := context.Background()
	scope := collaboration.Scope{TenantID: 7, UserID: "u", SessionID: sessionID, ID: id}
	require.NoError(t, s.Runtime.Store.Ensure(ctx, scope))
	require.NoError(t, s.Runtime.Store.Change(ctx, scope, func(g *collaboration.Group) error {
		g.Members = members
		for _, m := range members {
			g.NextSequence++
			g.Events = append(g.Events, collaboration.Event{
				Sequence: g.NextSequence, From: m.Name, RunID: m.Runs[0].ID, Kind: "succeeded",
				Content: `{"result":{"answer":"` + strings.Repeat("a", 8000) + `"}}`,
			})
		}
		g.Status = status
		return nil
	}))
	return scope
}

func childMember(sessionID string) *collaboration.Member {
	return &collaboration.Member{
		ID: "member-" + sessionID, Name: "child", SessionID: sessionID,
		Runs: []*collaboration.Run{{ID: "run-" + sessionID, State: "succeeded"}},
	}
}

func TestForgetSessionsDeletesChildConversationsAndRecords(t *testing.T) {
	s, _, _, ctx := collaborationService(t)
	createCleanupSession(t, s.db, "parent", "")
	createCleanupSession(t, s.db, "child", types.SessionKindAgentDelegated)
	createCleanupSession(t, s.db, "kept-child", types.SessionKindAgentDelegated)
	createCleanupGroup(t, s, "parent", "m1", "completed", []*collaboration.Member{childMember("child")})
	createCleanupGroup(t, s, "other", "m2", "completed", []*collaboration.Member{childMember("kept-child")})

	s.forgetSessions(ctx, 7, "u", []string{"parent"})

	var live []string
	require.NoError(t, s.db.Model(&types.Session{}).Where("kind = ?", types.SessionKindAgentDelegated).
		Pluck("id", &live).Error)
	require.Equal(t, []string{"kept-child"}, live)
	var groups []string
	require.NoError(t, s.db.Model(&collaboration.Record{}).Pluck("id", &groups).Error)
	require.Equal(t, []string{"m2"}, groups)
}

func TestPruneBoundsTerminalCollaborationStorage(t *testing.T) {
	s, _, _, ctx := collaborationService(t)
	createCleanupSession(t, s.db, "parent", "")
	createCleanupSession(t, s.db, "deleted", "")
	createCleanupGroup(t, s, "parent", "plain", "completed", nil)
	delegated := createCleanupGroup(t, s, "parent", "delegated", "completed",
		[]*collaboration.Member{childMember("child")})
	createCleanupGroup(t, s, "deleted", "orphan", "cancelled", []*collaboration.Member{childMember("child2")})
	createCleanupGroup(t, s, "parent", "recent", "completed", nil)
	require.NoError(t, s.db.Delete(&types.Session{ID: "deleted"}).Error)
	old := time.Now().Add(-2 * collaborationRetention).UTC()
	require.NoError(t, s.db.Model(&collaboration.Record{}).Where("id <> ?", "recent").
		Update("updated_at", old).Error)

	require.NoError(t, s.prune(ctx))

	var groups []string
	require.NoError(t, s.db.Model(&collaboration.Record{}).Order("id").Pluck("id", &groups).Error)
	require.Equal(t, []string{"delegated", "recent"}, groups,
		"undelegated and deleted-conversation records are removed; recent ones wait")
	g, err := s.Runtime.Store.Read(ctx, delegated)
	require.NoError(t, err)
	require.Equal(t, "succeeded", g.StatusView().Tasks[0].Status, "history cards survive compaction")
	require.Less(t, len(g.Events[0].Content), 1000)
}
