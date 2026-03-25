package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const testAESKey = "01234567890123456789012345678901" // 32 bytes

// setupTestDB creates an in-memory SQLite database with tenant table.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Tenant{}))
	return db
}

// insertTenantRaw inserts a tenant row with the given api_key value directly,
// bypassing GORM hooks, to simulate an already-encrypted row in the DB.
func insertTenantRaw(t *testing.T, db *gorm.DB, id uint64, apiKey string) {
	t.Helper()
	now := time.Now()
	err := db.Exec(
		"INSERT INTO tenants (id, name, api_key, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		id, "test-tenant", apiKey, "active", now, now,
	).Error
	require.NoError(t, err)
}

// readAPIKeyRaw reads the raw api_key value from the database, bypassing GORM hooks.
func readAPIKeyRaw(t *testing.T, db *gorm.DB, id uint64) string {
	t.Helper()
	var apiKey string
	err := db.Raw("SELECT api_key FROM tenants WHERE id = ?", id).Scan(&apiKey).Error
	require.NoError(t, err)
	return apiKey
}

func TestUpdateTenant_PreservesEncryptedAPIKey(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", testAESKey)

	db := setupTestDB(t)
	key := utils.GetAESKey()
	require.NotNil(t, key)

	// Encrypt a known api_key and insert it directly into the DB.
	originalPlaintext := "sk-my-secret-api-key"
	encrypted, err := utils.EncryptAESGCM(originalPlaintext, key)
	require.NoError(t, err)
	insertTenantRaw(t, db, 1, encrypted)

	// Verify the raw DB value is encrypted.
	rawBefore := readAPIKeyRaw(t, db, 1)
	assert.True(t, isEncrypted(rawBefore), "api_key should be encrypted in DB before update")

	// Simulate what happens in the application:
	// 1. Load tenant (AfterFind decrypts api_key)
	var tenant types.Tenant
	require.NoError(t, db.First(&tenant, 1).Error)
	assert.Equal(t, originalPlaintext, tenant.APIKey, "AfterFind should decrypt api_key")

	// 2. Modify a non-key field (simulating a config update via UpdateTenantKV)
	tenant.Description = "updated description"

	// 3. Save via repository's UpdateTenant
	repo := NewTenantRepository(db)
	require.NoError(t, repo.UpdateTenant(context.Background(), &tenant))

	// 4. Verify: raw DB value must still be encrypted AND unchanged (no unnecessary re-encryption)
	rawAfter := readAPIKeyRaw(t, db, 1)
	assert.True(t, isEncrypted(rawAfter), "api_key must remain encrypted in DB after update")
	assert.Equal(t, rawBefore, rawAfter, "api_key column should not be touched when only other fields change")

	// 5. Verify: the in-memory struct should still have the decrypted value
	assert.Equal(t, originalPlaintext, tenant.APIKey, "caller's struct should retain decrypted value")

	// 6. Verify: round-trip — re-read from DB and decrypt
	var reloaded types.Tenant
	require.NoError(t, db.First(&reloaded, 1).Error)
	assert.Equal(t, originalPlaintext, reloaded.APIKey, "re-loaded api_key should decrypt correctly")
	assert.Equal(t, "updated description", reloaded.Description, "description should be updated")
}

func TestUpdateTenant_PreEncryptedAPIKeyIsWritten(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", testAESKey)

	db := setupTestDB(t)
	key := utils.GetAESKey()
	require.NotNil(t, key)

	// Insert a tenant with an initial encrypted api_key.
	initialEncrypted, err := utils.EncryptAESGCM("sk-old-key", key)
	require.NoError(t, err)
	insertTenantRaw(t, db, 4, initialEncrypted)

	// Simulate CreateTenant / UpdateAPIKey path:
	// The service layer manually encrypts BEFORE calling repo.UpdateTenant.
	newEncrypted, err := utils.EncryptAESGCM("sk-new-key", key)
	require.NoError(t, err)

	tenant := &types.Tenant{ID: 4, APIKey: newEncrypted}
	repo := NewTenantRepository(db)
	require.NoError(t, repo.UpdateTenant(context.Background(), tenant))

	// The pre-encrypted value should be written to DB as-is.
	rawAfter := readAPIKeyRaw(t, db, 4)
	assert.Equal(t, newEncrypted, rawAfter, "pre-encrypted api_key should be written to DB")

	// Round-trip: decrypt should yield the new key.
	var reloaded types.Tenant
	require.NoError(t, db.First(&reloaded, 4).Error)
	assert.Equal(t, "sk-new-key", reloaded.APIKey)
}

func TestUpdateTenant_LegacyPlaintextNotOverwritten(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", testAESKey)

	db := setupTestDB(t)

	// Insert a tenant with a plaintext api_key (legacy row, pre-encryption era).
	insertTenantRaw(t, db, 2, "sk-legacy-plaintext-key")

	// Load tenant — AfterFind returns plaintext as-is (no enc:v1: prefix).
	var tenant types.Tenant
	require.NoError(t, db.First(&tenant, 2).Error)
	assert.Equal(t, "sk-legacy-plaintext-key", tenant.APIKey)

	// Update a non-key field via repository.
	tenant.Description = "migrated"
	repo := NewTenantRepository(db)
	require.NoError(t, repo.UpdateTenant(context.Background(), &tenant))

	// Legacy plaintext should NOT be overwritten — the column should remain untouched.
	rawAfter := readAPIKeyRaw(t, db, 2)
	assert.Equal(t, "sk-legacy-plaintext-key", rawAfter, "legacy plaintext api_key should remain untouched")
}

func TestUpdateTenant_NoEncryptionWithoutAESKey(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "")

	db := setupTestDB(t)
	insertTenantRaw(t, db, 3, "sk-no-encryption")

	var tenant types.Tenant
	require.NoError(t, db.First(&tenant, 3).Error)
	assert.Equal(t, "sk-no-encryption", tenant.APIKey)

	tenant.Description = "no key env"
	repo := NewTenantRepository(db)
	require.NoError(t, repo.UpdateTenant(context.Background(), &tenant))

	// Without SYSTEM_AES_KEY, api_key should remain as-is (no guard needed).
	rawAfter := readAPIKeyRaw(t, db, 3)
	assert.Equal(t, "sk-no-encryption", rawAfter)
}

func isEncrypted(s string) bool {
	return len(s) > len(utils.EncPrefix) && s[:len(utils.EncPrefix)] == utils.EncPrefix
}
