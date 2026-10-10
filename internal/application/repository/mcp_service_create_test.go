package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCreateMCPServicePreservesEnabledState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
	}{
		{name: "disabled", enabled: false},
		{name: "enabled", enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			require.NoError(t, db.AutoMigrate(&types.MCPService{}))
			repo := &mcpServiceRepository{db: db}
			service := &types.MCPService{
				TenantID:      1,
				Name:          "test",
				Enabled:       tc.enabled,
				TransportType: types.MCPTransportSSE,
			}
			require.NoError(t, repo.Create(context.Background(), service))
			require.Equal(t, tc.enabled, service.Enabled)
			stored, err := repo.GetByID(context.Background(), 1, service.ID)
			require.NoError(t, err)
			require.Equal(t, tc.enabled, stored.Enabled)
		})
	}
}
