package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupOrganizationSQLiteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`
CREATE TABLE organizations (
    id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT, owner_id TEXT NOT NULL,
    owner_tenant_id INTEGER NOT NULL DEFAULT 0, invite_code TEXT,
    require_approval BOOLEAN DEFAULT 0, invite_code_expires_at DATETIME,
    invite_code_validity_days INTEGER NOT NULL DEFAULT 7, avatar TEXT DEFAULT '',
    searchable BOOLEAN NOT NULL DEFAULT 0, member_limit INTEGER NOT NULL DEFAULT 50,
    created_at DATETIME, updated_at DATETIME, deleted_at DATETIME
);
CREATE TABLE organization_join_requests (
    id TEXT PRIMARY KEY, organization_id TEXT NOT NULL, user_id TEXT NOT NULL,
    tenant_id INTEGER NOT NULL, status TEXT NOT NULL DEFAULT 'pending',
    requested_role TEXT NOT NULL DEFAULT 'viewer', request_type TEXT NOT NULL DEFAULT 'join',
    prev_role TEXT, message TEXT, reviewed_by TEXT, reviewed_at DATETIME,
    review_message TEXT, created_at DATETIME, updated_at DATETIME
);`).Error)
	return db
}

func TestListSearchableSQLiteSupportsCaseInsensitiveIDAndEscapedSearch(t *testing.T) {
	db := setupOrganizationSQLiteTestDB(t)
	repo := &organizationRepository{db: db}
	ctx := context.Background()
	orgs := []*types.Organization{
		{
			ID: "org-alpha", Name: "Data Platform", Description: "Shared search space", OwnerID: "owner",
			Searchable: true,
		},
		{ID: "org-beta", Name: "A_B testing", Description: "Literal wildcard", OwnerID: "owner", Searchable: true},
		{ID: "org-hidden", Name: "Data Platform", Description: "Hidden", OwnerID: "owner", Searchable: false},
	}
	for _, org := range orgs {
		require.NoError(t, repo.Create(ctx, org))
	}

	for _, tc := range []struct {
		query string
		ids   []string
	}{
		{query: "data", ids: []string{"org-alpha"}},
		{query: "ORG-ALP", ids: []string{"org-alpha"}},
		{query: "A_B", ids: []string{"org-beta"}},
		{query: "%", ids: nil},
	} {
		got, err := repo.ListSearchable(ctx, tc.query, 20)
		require.NoError(t, err, tc.query)
		var gotIDs []string
		for _, org := range got {
			gotIDs = append(gotIDs, org.ID)
		}
		assert.Equal(t, tc.ids, gotIDs, tc.query)
	}
}

func TestUpdateJoinRequestStatusSQLiteSetsReviewedAt(t *testing.T) {
	db := setupOrganizationSQLiteTestDB(t)
	repo := &organizationRepository{db: db}
	ctx := context.Background()
	require.NoError(t, db.Exec(`INSERT INTO organization_join_requests
        (id, organization_id, user_id, tenant_id, status, requested_role, request_type)
        VALUES ('req-1', 'org-1', 'user-1', 1, 'pending', 'viewer', 'join')`).Error)

	require.NoError(t, repo.UpdateJoinRequestStatus(
		ctx, "req-1", types.JoinRequestStatusApproved, "reviewer", "welcome",
	))
	var request types.OrganizationJoinRequest
	require.NoError(t, db.First(&request, "id = ?", "req-1").Error)
	assert.Equal(t, types.JoinRequestStatusApproved, request.Status)
	assert.Equal(t, "reviewer", request.ReviewedBy)
	assert.Equal(t, "welcome", request.ReviewMessage)
	assert.NotNil(t, request.ReviewedAt)
}
