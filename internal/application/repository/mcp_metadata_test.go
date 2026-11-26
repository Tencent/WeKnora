package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMCPMetadataPersistsCompleteScopedSnapshotsAndRejectsOlderWrites(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.MCPService{}, &types.MCPMetadata{}))
	repo := &mcpServiceRepository{db: db}
	ctx := context.Background()
	service := &types.MCPService{TenantID: 1, ID: "svc", Enabled: true}
	require.NoError(t, repo.Create(ctx, service))
	newer := &types.MCPMetadata{
		TenantID:          1,
		ServiceID:         "svc",
		Principal:         "user:a",
		ConfigFingerprint: types.MCPConfigFingerprint(service),
		SyncedAt:          time.Now().UTC(),
		Instructions:      "Server instructions",
		Tools: []*types.MCPTool{
			{
				Name:        "查询",
				Description: "Full original description",
				InputSchema: json.RawMessage(
					`{"type":"object","oneOf":[{"required":["id"]}],` +
						`"properties":{"id":{"type":"string"}},"additionalProperties":false}`,
				),
			},
		},
	}
	_, err = repo.SaveMetadata(ctx, newer, service.Enabled)
	require.NoError(t, err)
	older := *newer
	older.SyncedAt = newer.SyncedAt.Add(-time.Minute)
	older.Tools = []*types.MCPTool{}
	winner, err := repo.SaveMetadata(ctx, &older, service.Enabled)
	require.NoError(t, err)
	require.Equal(t, newer.Tools, winner.Tools)
	got, err := repo.GetMetadata(ctx, 1, "svc", "user:a")
	require.NoError(t, err)
	require.Equal(t, newer.Tools, got.Tools)
	require.Equal(t, newer.Instructions, got.Instructions)
	for _, key := range []struct {
		tenant             uint64
		service, principal string
	}{{2, "svc", "user:a"}, {1, "svc", "user:b"}, {1, "another", "user:a"}} {
		missing, err := repo.GetMetadata(ctx, key.tenant, key.service, key.principal)
		require.NoError(t, err)
		require.Nil(t, missing)
	}
	foreign := *newer
	foreign.TenantID = 2
	_, err = repo.SaveMetadata(ctx, &foreign, service.Enabled)
	require.ErrorIs(t, err, types.ErrMCPMetadataConnectionChanged)
	missing, err := repo.GetMetadata(ctx, 2, service.ID, newer.Principal)
	require.NoError(t, err)
	require.Nil(t, missing)
	builtin := &types.MCPService{ID: "builtin", IsBuiltin: true, Enabled: true}
	require.NoError(t, repo.Create(ctx, builtin))
	foreign.ServiceID = builtin.ID
	foreign.ConfigFingerprint = types.MCPConfigFingerprint(builtin)
	shared, err := repo.SaveMetadata(ctx, &foreign, builtin.Enabled)
	require.NoError(t, err)
	require.Equal(t, foreign.TenantID, shared.TenantID)
	empty := *newer
	empty.SyncedAt = newer.SyncedAt.Add(time.Second)
	empty.Tools = []*types.MCPTool{}
	_, err = repo.SaveMetadata(ctx, &empty, service.Enabled)
	require.NoError(t, err)
	got, err = repo.GetMetadata(ctx, 1, "svc", "user:a")
	require.NoError(t, err)
	require.Empty(t, got.Tools, "successful empty directories retire removed tools")
}

func TestListMetadataSummariesCountsToolsWithoutReturningPayloads(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.MCPMetadata{}))
	repo := &mcpServiceRepository{db: db}
	ctx := context.Background()
	require.NoError(t, db.Create(&types.MCPMetadata{
		TenantID:          1,
		ServiceID:         "svc",
		Principal:         "",
		ConfigFingerprint: "fp",
		SyncedAt:          time.Now().UTC(),
		Tools: []*types.MCPTool{
			{Name: "a", Description: "secret-sized description"},
			{Name: "b"},
		},
	}).Error)
	require.NoError(t, db.Create(&types.MCPMetadata{
		TenantID:  1,
		ServiceID: "oauth",
		Principal: "user:a",
		SyncedAt:  time.Now().UTC(),
		Tools:     []*types.MCPTool{{Name: "only-a"}},
	}).Error)
	rows, err := repo.ListMetadataSummaries(ctx, 1, []string{"", "user:a"})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	byID := map[string]*types.MCPMetadataSummary{}
	for _, row := range rows {
		byID[row.ServiceID] = row
	}
	require.Equal(t, 2, byID["svc"].ToolCount)
	require.Equal(t, 1, byID["oauth"].ToolCount)
	other, err := repo.ListMetadataSummaries(ctx, 1, []string{"user:b"})
	require.NoError(t, err)
	require.Empty(t, other)
}

// SQLite serializes writes without row locks. Verify that the PostgreSQL
// dialect holds the service row while a matching directory is committed.
func TestMCPMetadataPostgresLocksServiceThroughReadback(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	repo := mcpServiceRepository{db: db}
	service := &types.MCPService{TenantID: 1, ID: "svc", Enabled: true}
	snapshot := &types.MCPMetadata{
		TenantID: 1, ServiceID: "svc", ConfigFingerprint: types.MCPConfigFingerprint(service),
	}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT .* FROM "mcp_services" .*FOR SHARE`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "enabled"}).AddRow("svc", 1, true))
	mock.ExpectExec(`INSERT INTO "mcp_metadata" .*ON CONFLICT`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT .* FROM "mcp_metadata"`).
		WillReturnRows(sqlmock.NewRows([]string{"config_fingerprint"}).AddRow(snapshot.ConfigFingerprint))
	mock.ExpectCommit()
	stored, err := repo.SaveMetadata(t.Context(), snapshot, true)
	require.NoError(t, err)
	require.False(t, stored.Stale)
	require.NoError(t, mock.ExpectationsWereMet())
}
