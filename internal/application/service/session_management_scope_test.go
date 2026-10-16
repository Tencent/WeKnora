package service

import (
	"context"
	"errors"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
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
			for _, target := range []struct {
				name        string
				ownerID     string
				description string
				imPlatform  string
			}{
				{"embed_owner", "embed_session:1:channel:visitor", types.EmbedSessionMarkerPrefix + "channel", ""},
				{"embed_mcp_owner", "mcp_endpoint:1:endpoint", types.EmbedSessionMarkerPrefix + "channel", ""},
				{"embed_api_owner", "api_tenant_key:1:7", types.EmbedSessionMarkerPrefix + "channel", ""},
				{"other-web-user", "other-web-user", types.EmbedSessionMarkerPrefix + "channel", ""},
				{"api", "api_tenant_key:1:7", "", ""},
				{"im", "im-user", "", "feishu"},
				{"maintenance", "other-web-user", types.SkillMaintenanceSessionMarker + "skill", ""},
			} {
				t.Run(operation+"/"+caller.name+"/"+target.name, func(t *testing.T) {
					svc, db := newTestSessionService(t)
					require.NoError(t, db.AutoMigrate(&testListSessionsIMChannelSession{}))
					svc.messageRepo = deleteForkMessageRepo{}
					svc.webSearchStateRepo = deleteForkWebSearchState{}
					ctx := context.WithValue(
						testSessionScopeContext(1, "admin-user"), types.TenantRoleContextKey, caller.role,
					)
					if caller.principal.Valid() {
						ctx = types.WithPrincipal(ctx, caller.principal)
					}
					row := &types.Session{
						TenantID: 1, UserID: target.ownerID, Title: "original",
						Description: target.description,
					}
					foreign := &types.Session{TenantID: 2, UserID: target.ownerID, Title: "foreign"}
					require.NoError(t, db.Create(row).Error)
					if target.imPlatform != "" {
						require.NoError(t, db.Create(&testListSessionsIMChannelSession{
							SessionID: row.ID, Platform: target.imPlatform,
						}).Error)
					}
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
							_, readErr := svc.GetSession(ctx, row.ID)
							require.NoError(t, readErr, "updated channel sessions must remain readable")
						}
						require.Equal(t, expectedTitle, stored.Title)
						require.Equal(t, row.Description, stored.Description)
					}
					require.Equal(t, target.ownerID, stored.UserID)
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
			require.NoError(t, db.AutoMigrate(&testListSessionsIMChannelSession{}))
			svc.messageRepo = deleteForkMessageRepo{}
			cleanup := &sessionManagementCleanupState{}
			svc.webSearchStateRepo = cleanup
			ctx := context.WithValue(testSessionScopeContext(1, "alice"), types.TenantRoleContextKey, role)
			rows := []types.Session{
				{TenantID: 1, UserID: "alice", Title: "own"},
				{TenantID: 1, UserID: "embed_session:1:channel:visitor", Title: "embed"},
				{TenantID: 2, UserID: "alice", Title: "foreign"},
				{TenantID: 1, UserID: "bob", Title: "private web"},
			}
			require.NoError(t, db.Create(&rows).Error)
			require.NoError(t, svc.BatchDeleteSessions(ctx, []string{
				rows[0].ID, rows[1].ID, rows[2].ID, rows[3].ID, "missing",
			}))
			expectedCleanup := []string{rows[0].ID}
			if role.HasPermission(types.TenantRoleAdmin) {
				expectedCleanup = append(expectedCleanup, rows[1].ID)
			}
			require.ElementsMatch(t, expectedCleanup, cleanup.ids)
			for i, row := range rows {
				var stored types.Session
				require.NoError(t, db.Unscoped().First(&stored, "id = ?", row.ID).Error)
				wantDeleted := i == 0 || (i == 1 && role.HasPermission(types.TenantRoleAdmin))
				require.Equal(t, wantDeleted, stored.DeletedAt.Valid, "session %s", row.Title)
			}
		})
	}
}

func TestSessionManagementRejectsOtherUsersWebSessions(t *testing.T) {
	for _, role := range []types.TenantRole{types.TenantRoleOwner, types.TenantRoleAdmin} {
		for _, operation := range []string{"update", "delete", "batch_delete"} {
			t.Run(string(role)+"/"+operation, func(t *testing.T) {
				svc, db := newTestSessionService(t)
				require.NoError(t, db.AutoMigrate(&testListSessionsIMChannelSession{}))
				svc.messageRepo = deleteForkMessageRepo{}
				cleanup := &sessionManagementCleanupState{}
				svc.webSearchStateRepo = cleanup
				ctx := context.WithValue(testSessionScopeContext(1, "alice"), types.TenantRoleContextKey, role)
				row := &types.Session{TenantID: 1, UserID: "bob", Title: "private web"}
				require.NoError(t, db.Create(row).Error)

				var err error
				switch operation {
				case "update":
					err = svc.UpdateSession(ctx, &types.Session{
						ID: row.ID, TenantID: 1, Title: "updated",
						Description: types.EmbedSessionMarkerPrefix + "planted",
					})
				case "delete":
					err = svc.DeleteSession(ctx, row.ID)
				case "batch_delete":
					err = svc.BatchDeleteSessions(ctx, []string{row.ID})
				}
				require.ErrorIs(t, err, apperrors.ErrSessionNotFound)
				require.Empty(t, cleanup.ids)
				var stored types.Session
				require.NoError(t, db.First(&stored, "id = ?", row.ID).Error)
				require.Equal(t, row.Title, stored.Title)
				require.Equal(t, row.Description, stored.Description)
			})
		}
	}
}

type sessionManagementCleanupState struct {
	interfaces.WebSearchStateService
	ids []string
}

func (s *sessionManagementCleanupState) DeleteWebSearchTempKBState(_ context.Context, id string) error {
	s.ids = append(s.ids, id)
	return nil
}

func TestSessionManagementPropagatesStorageErrorsBeforeCleanup(t *testing.T) {
	storageErr := errors.New("storage unavailable")
	for _, stage := range []string{"get", "get_by_id", "im_platform"} {
		for _, operation := range []string{"update", "delete", "batch_delete"} {
			t.Run(stage+"/"+operation, func(t *testing.T) {
				svc, db := newTestSessionService(t)
				rows := []types.Session{
					{TenantID: 1, UserID: "alice", Title: "own"},
					{TenantID: 1, UserID: "embed_session:1:channel:visitor", Title: "embed"},
				}
				require.NoError(t, db.Create(&rows).Error)
				svc.sessionRepo = sessionManagementErrorRepo{
					SessionRepository: svc.sessionRepo,
					targetID:          rows[1].ID, stage: stage, err: storageErr,
				}
				svc.messageRepo = deleteForkMessageRepo{}
				cleanup := &sessionManagementCleanupState{}
				svc.webSearchStateRepo = cleanup
				ctx := context.WithValue(
					testSessionScopeContext(1, "alice"), types.TenantRoleContextKey, types.TenantRoleAdmin,
				)

				var err error
				switch operation {
				case "update":
					err = svc.UpdateSession(ctx, &types.Session{ID: rows[1].ID, TenantID: 1, Title: "updated"})
				case "delete":
					err = svc.DeleteSession(ctx, rows[1].ID)
				case "batch_delete":
					err = svc.BatchDeleteSessions(ctx, []string{rows[0].ID, rows[1].ID})
				}
				require.ErrorIs(t, err, storageErr)
				require.Empty(t, cleanup.ids)
				for _, row := range rows {
					var stored types.Session
					require.NoError(t, db.First(&stored, "id = ?", row.ID).Error)
					require.Equal(t, row.Title, stored.Title)
				}
			})
		}
	}
}

type sessionManagementErrorRepo struct {
	interfaces.SessionRepository
	targetID string
	stage    string
	err      error
}

func (r sessionManagementErrorRepo) Get(
	ctx context.Context, tenantID uint64, userID, id string,
) (*types.Session, error) {
	if id == r.targetID && r.stage == "get" {
		return nil, r.err
	}
	return r.SessionRepository.Get(ctx, tenantID, userID, id)
}

func (r sessionManagementErrorRepo) GetByID(
	ctx context.Context, tenantID uint64, id string,
) (*types.Session, error) {
	if id == r.targetID && r.stage == "get_by_id" {
		return nil, r.err
	}
	return r.SessionRepository.GetByID(ctx, tenantID, id)
}

func (r sessionManagementErrorRepo) GetIMPlatform(ctx context.Context, tenantID uint64, id string) (string, error) {
	if id == r.targetID && r.stage == "im_platform" {
		return "", r.err
	}
	return r.SessionRepository.GetIMPlatform(ctx, tenantID, id)
}
