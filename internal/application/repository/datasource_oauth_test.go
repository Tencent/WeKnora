package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newDataSourceOAuthTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.DataSource{}, &types.DataSourceOAuthToken{}, &types.DataSourceItem{},
	))
	return db
}

func TestDataSourceOAuthRepositoryEncryptsAndScopesTokens(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	db := newDataSourceOAuthTestDB(t)
	ds := &types.DataSource{TenantID: 7, KnowledgeBaseID: "kb", Name: "OneDrive", Type: types.ConnectorTypeOneDrive}
	require.NoError(t, db.Create(ds).Error)

	repo := NewDataSourceOAuthRepository(db)
	token := &types.DataSourceOAuthToken{
		TenantID: 7, DataSourceID: ds.ID, Provider: "onedrive",
		AccessToken: "access-secret", RefreshToken: "refresh-secret", TokenType: "Bearer",
		ExpiresAt: time.Now().Add(time.Hour), ProviderAccountID: "account",
		AuthorizedDriveID: "drive", AuthorizedByUserID: "user", ConnectionVersion: 1,
	}
	require.NoError(t, repo.Save(context.Background(), token))

	var raw struct{ AccessToken, RefreshToken string }
	require.NoError(t, db.Raw(
		"SELECT access_token, refresh_token FROM data_source_oauth_tokens WHERE data_source_id = ?", ds.ID,
	).Scan(&raw).Error)
	require.NotEqual(t, "access-secret", raw.AccessToken)
	require.NotEqual(t, "refresh-secret", raw.RefreshToken)
	require.Contains(t, raw.AccessToken, "enc:v1:")

	got, err := repo.Get(context.Background(), 7, ds.ID)
	require.NoError(t, err)
	require.Equal(t, "access-secret", got.AccessToken)
	require.Equal(t, "refresh-secret", got.RefreshToken)

	otherTenant, err := repo.Get(context.Background(), 8, ds.ID)
	require.NoError(t, err)
	require.Nil(t, otherTenant)
}

func TestDataSourceOAuthRepositoryFailsClosedWithoutAESKey(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "")
	db := newDataSourceOAuthTestDB(t)
	ds := &types.DataSource{TenantID: 1, KnowledgeBaseID: "kb", Name: "OneDrive", Type: types.ConnectorTypeOneDrive}
	require.NoError(t, db.Create(ds).Error)

	err := NewDataSourceOAuthRepository(db).Save(context.Background(), &types.DataSourceOAuthToken{
		TenantID: 1, DataSourceID: ds.ID, Provider: "onedrive", AccessToken: "access",
		RefreshToken: "refresh", ExpiresAt: time.Now(), ProviderAccountID: "account",
		AuthorizedDriveID: "drive", AuthorizedByUserID: "user", ConnectionVersion: 1,
	})
	require.ErrorContains(t, err, "SYSTEM_AES_KEY")
}

func TestDataSourceOAuthRepositoryFailsClosedWithWrongAESKey(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	db := newDataSourceOAuthTestDB(t)
	ds := &types.DataSource{TenantID: 2, KnowledgeBaseID: "kb", Name: "OneDrive", Type: types.ConnectorTypeOneDrive}
	require.NoError(t, db.Create(ds).Error)
	repo := NewDataSourceOAuthRepository(db)
	require.NoError(t, repo.Save(context.Background(), &types.DataSourceOAuthToken{
		TenantID: 2, DataSourceID: ds.ID, Provider: "onedrive", AccessToken: "access",
		RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour), ProviderAccountID: "account",
		AuthorizedDriveID: "drive", AuthorizedByUserID: "user", ConnectionVersion: 1,
	}))

	t.Setenv("SYSTEM_AES_KEY", "abcdef0123456789abcdef0123456789")
	_, err := repo.Get(context.Background(), 2, ds.ID)
	require.Error(t, err)
}

func TestDataSourceItemRepositoryUpsertAndTenantIsolation(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	db := newDataSourceOAuthTestDB(t)
	ds := &types.DataSource{TenantID: 11, KnowledgeBaseID: "kb", Name: "OneDrive", Type: types.ConnectorTypeOneDrive}
	require.NoError(t, db.Create(ds).Error)
	repo := NewDataSourceItemRepository(db)

	item := &types.DataSourceItem{
		TenantID: 11, DataSourceID: ds.ID, ConnectionVersion: 1,
		DriveID: "drive", ItemID: "item", ParentItemID: "root", ItemType: "file",
		SelectedRootID: "root", ExternalID: "external", LastSeenGeneration: "g1",
	}
	require.NoError(t, repo.Upsert(context.Background(), item))
	item.ParentItemID = "folder-2"
	item.LastSeenGeneration = "g2"
	require.NoError(t, repo.Upsert(context.Background(), item))

	got, err := repo.Find(context.Background(), 11, ds.ID, 1, "drive", "item")
	require.NoError(t, err)
	require.Equal(t, "folder-2", got.ParentItemID)
	require.Equal(t, "g2", got.LastSeenGeneration)

	got, err = repo.Find(context.Background(), 12, ds.ID, 1, "drive", "item")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestRevokeAuthorizationInvalidatesConnectionStateAtomically(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	db := newDataSourceOAuthTestDB(t)
	ds := &types.DataSource{
		TenantID: 9, KnowledgeBaseID: "kb", Name: "OneDrive", Type: types.ConnectorTypeOneDrive,
		Status: types.DataSourceStatusActive, ConnectionVersion: 3,
		LastSyncCursor: types.JSON(`{"connector_cursor":{"delta_link":"secret"}}`),
	}
	require.NoError(t, db.Create(ds).Error)
	oauth := NewDataSourceOAuthRepository(db)
	require.NoError(t, oauth.Save(context.Background(), &types.DataSourceOAuthToken{
		TenantID: 9, DataSourceID: ds.ID, Provider: "onedrive", AccessToken: "access", RefreshToken: "refresh",
		ExpiresAt: time.Now().Add(time.Hour), ProviderAccountID: "account", AuthorizedDriveID: "drive",
		AuthorizedByUserID: "user", ConnectionVersion: 3,
	}))
	require.NoError(t, NewDataSourceItemRepository(db).Upsert(context.Background(), &types.DataSourceItem{
		TenantID: 9, DataSourceID: ds.ID, ConnectionVersion: 3, DriveID: "drive", ItemID: "item",
		ItemType: "file", ExternalID: "external",
	}))

	version, err := oauth.RevokeAuthorization(context.Background(), 9, ds.ID, 3, types.JSON(`{"type":"onedrive"}`))
	require.NoError(t, err)
	require.Equal(t, uint64(4), version)
	var stored types.DataSource
	require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
	require.Equal(t, uint64(4), stored.ConnectionVersion)
	require.Equal(t, types.DataSourceStatusPaused, stored.Status)
	require.Empty(t, stored.LastSyncCursor)
	config, err := stored.ParseConfig()
	require.NoError(t, err)
	require.Empty(t, config.ResourceIDs)
	token, err := oauth.Get(context.Background(), 9, ds.ID)
	require.NoError(t, err)
	require.Nil(t, token)
	item, err := NewDataSourceItemRepository(db).Find(context.Background(), 9, ds.ID, 3, "drive", "item")
	require.NoError(t, err)
	require.Nil(t, item)
}

func TestReplaceAuthorizationRotatesConnectionAndClearsOldSyncState(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	db := newDataSourceOAuthTestDB(t)
	configJSON, err := (&types.DataSourceConfig{
		Type: types.ConnectorTypeOneDrive, ResourceIDs: []string{"old-root"},
	}).ToJSON()
	require.NoError(t, err)
	ds := &types.DataSource{
		TenantID: 15, KnowledgeBaseID: "kb", Name: "OneDrive", Type: types.ConnectorTypeOneDrive,
		Status: types.DataSourceStatusActive, ConnectionVersion: 1, Config: configJSON,
		LastSyncCursor: types.JSON(`{"connector_cursor":{"delta_link":"old-secret"}}`),
	}
	require.NoError(t, db.Create(ds).Error)
	repo := NewDataSourceOAuthRepository(db)
	require.NoError(t, repo.Save(context.Background(), &types.DataSourceOAuthToken{
		TenantID: 15, DataSourceID: ds.ID, Provider: "onedrive", AccessToken: "old-access",
		RefreshToken: "old-refresh", ExpiresAt: time.Now().Add(time.Hour), ProviderAccountID: "old-account",
		AuthorizedDriveID: "old-drive", AuthorizedByUserID: "admin", ConnectionVersion: 1,
	}))
	require.NoError(t, NewDataSourceItemRepository(db).Upsert(context.Background(), &types.DataSourceItem{
		TenantID: 15, DataSourceID: ds.ID, ConnectionVersion: 1, DriveID: "old-drive", ItemID: "old-item",
		ItemType: "file", ExternalID: "old-external", Ingested: true,
	}))
	resetConfig, err := (&types.DataSourceConfig{Type: types.ConnectorTypeOneDrive}).ToJSON()
	require.NoError(t, err)

	version, err := repo.SaveAuthorization(context.Background(), &types.DataSourceOAuthToken{
		TenantID: 15, DataSourceID: ds.ID, Provider: "onedrive", AccessToken: "new-access",
		RefreshToken: "new-refresh", ExpiresAt: time.Now().Add(time.Hour), ProviderAccountID: "new-account",
		AuthorizedDriveID: "new-drive", AuthorizedByUserID: "admin",
	}, 1, true, resetConfig)
	require.NoError(t, err)
	require.Equal(t, uint64(2), version)

	var stored types.DataSource
	require.NoError(t, db.First(&stored, "id = ?", ds.ID).Error)
	require.Equal(t, uint64(2), stored.ConnectionVersion)
	require.Equal(t, types.DataSourceStatusConnecting, stored.Status)
	require.Empty(t, stored.LastSyncCursor)
	parsed, err := stored.ParseConfig()
	require.NoError(t, err)
	require.Empty(t, parsed.ResourceIDs)
	token, err := repo.Get(context.Background(), 15, ds.ID)
	require.NoError(t, err)
	require.Equal(t, "new-account", token.ProviderAccountID)
	require.Equal(t, uint64(2), token.ConnectionVersion)
	item, err := NewDataSourceItemRepository(db).Find(context.Background(), 15, ds.ID, 1, "old-drive", "old-item")
	require.NoError(t, err)
	require.Nil(t, item)
}
