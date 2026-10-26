package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAgentGrantRepositoryLifecycle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.AgentGrant{}))

	ctx := context.Background()
	repo := NewAgentGrantRepository(db)
	scope := types.GrantScope{Scope: types.AgentGrantScopeSession, ScopeID: "root"}
	save := func(user, label string) {
		require.NoError(t, repo.SaveGrant(ctx, &types.AgentGrant{
			TenantID: 7, UserID: user, Scope: scope.Scope, ScopeID: scope.ScopeID,
			GrantKey: "mcp:svc/write#abc", Label: label,
		}))
	}
	save("u1", "first")
	save("u2", "other user")

	found, err := repo.FindGrant(ctx, 7, "u1", scope, "mcp:svc/write#abc")
	require.NoError(t, err)
	require.NotNil(t, found)
	missing, err := repo.FindGrant(ctx, 8, "u1", scope, "mcp:svc/write#abc")
	require.NoError(t, err)
	assert.Nil(t, missing, "grants do not cross tenants")

	used := time.Now()
	require.NoError(t, repo.TouchGrant(ctx, found.ID, used))
	listed, err := repo.ListGrants(ctx, 7, "u1", scope.Scope, scope.ScopeID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.NotNil(t, listed[0].LastUsedAt)

	revoked, err := repo.RevokeGrant(ctx, 7, "u2", found.ID, time.Now())
	require.NoError(t, err)
	assert.False(t, revoked, "only the owner revokes a grant")
	revoked, err = repo.RevokeGrant(ctx, 7, "u1", found.ID, time.Now())
	require.NoError(t, err)
	assert.True(t, revoked)
	listed, err = repo.ListGrants(ctx, 7, "u1", scope.Scope, scope.ScopeID)
	require.NoError(t, err)
	assert.Empty(t, listed)

	save("u1", "again")
	again, err := repo.FindGrant(ctx, 7, "u1", scope, "mcp:svc/write#abc")
	require.NoError(t, err)
	require.NotNil(t, again)
	assert.Equal(t, found.ID, again.ID, "approving again revives the same row")
	assert.Nil(t, again.RevokedAt)
	assert.Nil(t, again.LastUsedAt)
	assert.Equal(t, "again", again.Label)

	require.NoError(t, repo.DeleteScopes(ctx, 7, scope.Scope, []string{"root"}))
	gone, err := repo.FindGrant(ctx, 7, "u1", scope, "mcp:svc/write#abc")
	require.NoError(t, err)
	assert.Nil(t, gone)
}
