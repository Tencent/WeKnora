package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAssistantListAlwaysScopesOwner(t *testing.T) {
	for _, role := range []types.TenantRole{types.TenantRoleViewer, types.TenantRoleAdmin, types.TenantRoleOwner} {
		for _, source := range []string{"", "web", "api", "embed", "feishu"} {
			t.Run(string(role)+"/"+source, func(t *testing.T) {
				svc, db := newTestSessionService(t)
				require.NoError(t, db.AutoMigrate(&testListSessionsIMChannelSession{}))
				caller := "alice"
				owners := []string{"alice", "bob", ""}
				if source == "api" {
					caller = types.SessionOwnerAPIExternalUserPrefix + "1:alice"
					owners = []string{caller, types.SessionOwnerAPIExternalUserPrefix + "1:bob", ""}
				}
				for _, owner := range owners {
					for _, kind := range []string{"", types.SessionKindAssistant} {
						row := &types.Session{TenantID: 1, UserID: owner, Kind: kind}
						if source == "embed" {
							row.Description = types.EmbedSessionMarkerPrefix + "channel"
						}
						require.NoError(t, db.Create(row).Error)
						if source == "feishu" {
							require.NoError(t, db.Create(&testListSessionsIMChannelSession{
								SessionID: row.ID, Platform: source,
							}).Error)
						}
					}
				}
				ctx := context.WithValue(testSessionScopeContext(1, caller), types.TenantRoleContextKey, role)
				result, err := svc.ListSessions(ctx, &types.SessionListQuery{
					Kind: types.SessionKindAssistant, Source: source, UserID: "bob",
				})
				require.NoError(t, err)
				require.EqualValues(t, 1, result.Total)
				for _, row := range result.Data.([]*types.SessionListItem) {
					require.Equal(t, caller, row.UserID)
					require.Equal(t, types.SessionKindAssistant, row.Kind)
				}
				count, err := svc.CountSessionsBySource(ctx, &types.SessionListQuery{
					Kind: types.SessionKindAssistant, Source: source,
				})
				require.NoError(t, err)
				require.EqualValues(t, 1, count)
				if role != types.TenantRoleViewer || source == "" || source == "web" {
					normal, err := svc.ListSessions(ctx, &types.SessionListQuery{Source: source})
					require.NoError(t, err)
					for _, row := range normal.Data.([]*types.SessionListItem) {
						require.Empty(t, row.Kind)
					}
				}
			})
		}
	}
}

func TestAssistantListWithoutOwnerReturnsNothing(t *testing.T) {
	svc, db := newTestSessionService(t)
	require.NoError(t, db.AutoMigrate(&testListSessionsIMChannelSession{}))
	require.NoError(t, db.Create(&types.Session{TenantID: 1, Kind: types.SessionKindAssistant}).Error)
	result, err := svc.ListSessions(testSessionScopeContext(1, ""), &types.SessionListQuery{
		Kind: types.SessionKindAssistant, UserID: "injected",
	})
	require.NoError(t, err)
	require.Zero(t, result.Total)
	_, err = svc.ListSessions(testSessionScopeContext(1, "alice"), &types.SessionListQuery{Kind: "unknown"})
	require.Error(t, err)
}

func TestForkPreservesAssistantKind(t *testing.T) {
	svc, sessions, _ := newForkFixture(t, &fakeForkSandboxPort{}, []*types.Message{
		{ID: "first", SessionID: "src", Role: "user"},
	})
	sessions.source.Kind = types.SessionKindAssistant
	_, err := svc.Fork(context.Background(), 1, "u1", "src", "first", "")
	require.NoError(t, err)
	require.Equal(t, types.SessionKindAssistant, sessions.created.Kind)
}

func TestDeleteAllSessionsKeepsAssistantAndItsCleanupState(t *testing.T) {
	svc, db, _ := newSessionServiceForForkDeleteTest(t)
	cleanup := &sessionManagementCleanupState{}
	svc.webSearchStateRepo = cleanup
	normal := &types.Session{TenantID: 1, UserID: "alice"}
	assistant := &types.Session{TenantID: 1, UserID: "alice", Kind: types.SessionKindAssistant}
	require.NoError(t, db.Create(normal).Error)
	require.NoError(t, db.Create(assistant).Error)
	ctx := testSessionScopeContext(1, "alice")
	require.NoError(t, svc.DeleteAllSessions(ctx))
	require.Equal(t, []string{normal.ID}, cleanup.ids)
	var stored types.Session
	require.NoError(t, db.First(&stored, "id = ?", assistant.ID).Error)
	require.NoError(t, svc.DeleteSession(ctx, assistant.ID))
	require.Equal(t, []string{normal.ID, assistant.ID}, cleanup.ids)
}
