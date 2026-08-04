package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGetShareByAgentIDAndSourceForTenantDisambiguatesSource(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:agent-share-source?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	statements := []string{
		`CREATE TABLE agent_shares (
			id TEXT PRIMARY KEY, agent_id TEXT, organization_id TEXT,
			shared_by_user_id TEXT, source_tenant_id INTEGER, permission TEXT,
			created_at DATETIME, updated_at DATETIME, deleted_at DATETIME
		)`,
		`CREATE TABLE organization_tenant_members (organization_id TEXT, tenant_id INTEGER)`,
		`CREATE TABLE organizations (id TEXT PRIMARY KEY, deleted_at DATETIME)`,
		`CREATE TABLE custom_agents (id TEXT, tenant_id INTEGER, deleted_at DATETIME)`,
		`INSERT INTO organizations(id) VALUES ('org')`,
		`INSERT INTO organization_tenant_members(organization_id, tenant_id) VALUES ('org', 7)`,
		`INSERT INTO custom_agents(id, tenant_id) VALUES ('builtin-smart-reasoning', 42), ('builtin-smart-reasoning', 84)`,
		`INSERT INTO agent_shares(id, agent_id, organization_id, source_tenant_id) VALUES
			('share-42', 'builtin-smart-reasoning', 'org', 42),
			('share-84', 'builtin-smart-reasoning', 'org', 84)`,
	}
	for _, statement := range statements {
		require.NoError(t, db.Exec(statement).Error)
	}

	repo := &agentShareRepository{db: db}
	share, err := repo.GetShareByAgentIDAndSourceForTenant(
		context.Background(), 7, "builtin-smart-reasoning", 84,
	)
	require.NoError(t, err)
	require.Equal(t, "share-84", share.ID)
	require.Equal(t, uint64(84), share.SourceTenantID)

	_, err = repo.GetShareByAgentIDAndSourceForTenant(
		context.Background(), 8, "builtin-smart-reasoning", 84,
	)
	require.ErrorIs(t, err, ErrAgentShareNotFound)
}
