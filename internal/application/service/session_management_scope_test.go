package service

import (
	"context"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSessionManagementScope(t *testing.T) {
	operations := []string{"update", "delete", "batch_delete"}
	callers := []struct {
		name      string
		role      types.TenantRole
		principal types.Principal
		allowed   bool
	}{
		{name: "owner", role: types.TenantRoleOwner, allowed: true},
		{name: "admin", role: types.TenantRoleAdmin, allowed: true},
		{name: "contributor", role: types.TenantRoleContributor},
		{name: "viewer", role: types.TenantRoleViewer},
		{name: "missing_role"},
		{
			name: "embed_admin", role: types.TenantRoleAdmin,
			principal: types.Principal{Type: types.PrincipalEmbedSession, ID: "1:channel:other"},
		},
		{
			name: "api_admin", role: types.TenantRoleAdmin,
			principal: types.Principal{Type: types.PrincipalAPIExternalUser, ID: "1:other"},
		},
		{
			name: "mcp_admin", role: types.TenantRoleAdmin,
			principal: types.Principal{Type: types.PrincipalMCPEndpoint, ID: "1:other"},
		},
	}
	for _, operation := range operations {
		for _, caller := range callers {
			for _, ownerID := range []string{
				"embed_session:1:channel:visitor", "mcp_endpoint:1:endpoint", "api_tenant_key:1:7", "other-web-user",
			} {
				t.Run(operation+"/"+caller.name+"/"+ownerID, func(t *testing.T) {
					svc, db := newTestSessionService(t)
					svc.messageRepo = deleteForkMessageRepo{}
					svc.webSearchStateRepo = deleteForkWebSearchState{}
					ctx := context.WithValue(testSessionScopeContext(1, "admin-user"), types.TenantRoleContextKey, caller.role)
					if caller.principal.Valid() {
						ctx = types.WithPrincipal(ctx, caller.principal)
					}
					row := &types.Session{
						TenantID: 1, UserID: ownerID, Title: "original",
						Description: types.EmbedSessionMarkerPrefix + "channel",
					}
					foreign := &types.Session{TenantID: 2, UserID: ownerID, Title: "foreign"}
					require.NoError(t, db.Create(row).Error)
					require.NoError(t, db.Create(foreign).Error)

					manage := func(id string) error {
						switch operation {
						case "update":
							return svc.UpdateSession(ctx, &types.Session{
								ID: id, TenantID: 1, Title: "updated", Description: row.Description,
							})
						case "delete":
							return svc.DeleteSession(ctx, id)
						default:
							return svc.BatchDeleteSessions(ctx, []string{id})
						}
					}
					require.ErrorIs(t, manage(foreign.ID), apperrors.ErrSessionNotFound)
					err := manage(row.ID)
					if caller.allowed {
						require.NoError(t, err)
					} else {
						require.ErrorIs(t, err, apperrors.ErrSessionNotFound)
					}
					var stored types.Session
					err = db.First(&stored, "id = ?", row.ID).Error
					if caller.allowed && operation != "update" {
						require.ErrorIs(t, err, gorm.ErrRecordNotFound)
						require.NoError(t, db.Unscoped().First(&stored, "id = ?", row.ID).Error)
						require.True(t, stored.DeletedAt.Valid)
					} else {
						require.NoError(t, err)
						expectedTitle := "original"
						if caller.allowed {
							expectedTitle = "updated"
						}
						require.Equal(t, expectedTitle, stored.Title)
						require.Equal(t, row.Description, stored.Description)
					}
					require.Equal(t, ownerID, stored.UserID)
					var foreignStored types.Session
					require.NoError(t, db.First(&foreignStored, "id = ?", foreign.ID).Error)
					require.Equal(t, "foreign", foreignStored.Title)
					own := &types.Session{TenantID: 1, UserID: types.SessionOwnerIDFromContext(ctx), Title: "own"}
					require.NoError(t, db.Create(own).Error)
					require.NoError(t, manage(own.ID), "callers must still be able to manage their own sessions")
				})
			}
		}
	}
}

func TestBatchDeleteSessionsManagementScopeMixedIDs(t *testing.T) {
	for _, role := range []types.TenantRole{types.TenantRoleOwner, types.TenantRoleAdmin, types.TenantRoleContributor} {
		t.Run(string(role), func(t *testing.T) {
			svc, db := newTestSessionService(t)
			svc.messageRepo = deleteForkMessageRepo{}
			svc.webSearchStateRepo = deleteForkWebSearchState{}
			ctx := context.WithValue(testSessionScopeContext(1, "alice"), types.TenantRoleContextKey, role)
			rows := []types.Session{
				{TenantID: 1, UserID: "alice", Title: "own"},
				{TenantID: 1, UserID: "embed_session:1:channel:visitor", Title: "embed"},
				{TenantID: 2, UserID: "alice", Title: "foreign"},
			}
			require.NoError(t, db.Create(&rows).Error)
			require.NoError(t, svc.BatchDeleteSessions(ctx, []string{rows[0].ID, rows[1].ID, rows[2].ID, "missing"}))
			for i, row := range rows {
				var stored types.Session
				require.NoError(t, db.Unscoped().First(&stored, "id = ?", row.ID).Error)
				wantDeleted := i == 0 || (i == 1 && role.HasPermission(types.TenantRoleAdmin))
				require.Equal(t, wantDeleted, stored.DeletedAt.Valid, "session %s", row.Title)
			}
		})
	}
}
