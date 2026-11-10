package database

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestPostgresMCPOAuthTimesKeepTheInstant runs the versioned migrations against
// a real PostgreSQL; see TestPostgresMigrationsServeAgentHistory for how to
// start one. The application reads through a TimeZone=UTC session while token
// expiries come from the process clock, which runs in the deployment's local
// zone. A naive TIMESTAMP column drops that offset on write and returns a
// value shifted by it.
func TestPostgresMCPOAuthTimesKeepTheInstant(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MIGRATION_TEST_POSTGRES_DSN to run PostgreSQL migration tests")
	}
	chdirAndRestore(t, sqliteRepoRoot(t))
	require.NoError(t, RunMigrationsWithOptions(dsn, MigrationOptions{}))

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := gorm.Open(postgres.Open(dsn+sep+"TimeZone=UTC"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	serviceID := uuid.NewString()
	require.NoError(t, db.Exec(
		"INSERT INTO mcp_services (id, tenant_id, name, transport_type) VALUES (?, 7, 'tz', 'sse')",
		serviceID).Error)
	t.Cleanup(func() { db.Exec("DELETE FROM mcp_services WHERE id = ?", serviceID) })

	ctx := context.Background()
	repo := repository.NewMCPOAuthRepository(db)
	principal := types.Principal{Type: types.PrincipalAPIExternalUser, ID: "7:tz"}
	local := time.FixedZone("UTC+9", 9*60*60)
	expiresAt := time.Date(2026, 10, 12, 21, 3, 32, 0, local)

	require.NoError(t, repo.SaveTokenForPrincipal(ctx, &types.MCPOAuthToken{
		TenantID:      7,
		PrincipalType: principal.Type,
		PrincipalID:   principal.ID,
		ServiceID:     serviceID,
		AccessToken:   "token",
		TokenType:     "Bearer",
		ExpiresAt:     expiresAt,
	}))
	loaded, err := repo.GetTokenForPrincipal(ctx, 7, principal, serviceID)
	require.NoError(t, err)
	require.True(t, loaded.ExpiresAt.Equal(expiresAt), "expiry %s, want %s", loaded.ExpiresAt, expiresAt)

	// The column itself must keep the instant, so correctness does not rest on
	// every writer normalizing to UTC first.
	require.NoError(t, db.Exec(
		"UPDATE mcp_oauth_tokens SET expires_at = ?, updated_at = ? WHERE service_id = ?",
		expiresAt, expiresAt, serviceID).Error)
	loaded, err = repo.GetTokenForPrincipal(ctx, 7, principal, serviceID)
	require.NoError(t, err)
	require.True(t, loaded.ExpiresAt.Equal(expiresAt), "expiry %s, want %s", loaded.ExpiresAt, expiresAt)
	require.True(t, loaded.UpdatedAt.Equal(expiresAt), "updated_at %s, want %s", loaded.UpdatedAt, expiresAt)
}
