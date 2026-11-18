package credential

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

const testAESKey = "kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk"

var (
	alice = Owner{TenantID: 1, UserID: "web_user:alice"}
	bob   = Owner{TenantID: 1, UserID: "web_user:bob"}
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.CredentialVaultItem{}))
	require.NoError(t, db.Exec(`CREATE UNIQUE INDEX uq_credential_vault_items_owner_site_user
		ON credential_vault_items(tenant_id, user_id, site, username) WHERE deleted_at IS NULL`).Error)
	return db
}

func newTestVault(t *testing.T) (*Vault, *gorm.DB) {
	t.Helper()
	t.Setenv("SYSTEM_AES_KEY", testAESKey)
	db := newTestDB(t)
	return NewVault(repository.NewCredentialVaultRepository(db)), db
}

func httpStatus(t *testing.T, err error) int {
	t.Helper()
	var appErr *apperrors.AppError
	require.True(t, errors.As(err, &appErr), "want an AppError, got %v", err)
	return appErr.HTTPCode
}

func requireNoSecret(t *testing.T, secret string, blobs ...string) {
	t.Helper()
	for _, b := range blobs {
		require.False(t, strings.Contains(b, secret), "secret leaked into %q", b)
	}
}
