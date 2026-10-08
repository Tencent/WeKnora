package browserskill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "browser.db")+"?_busy_timeout=5000&_journal_mode=WAL"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	migration, err := os.ReadFile("../../migrations/sqlite/000014_browser_authorization.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(migration)).Error)
	return &Store{db: db}
}

func redeemTestPair(ctx context.Context, t *testing.T, m *Manager, link string) string {
	t.Helper()
	parts := strings.Split(link, "#")
	if _, err := m.store.authenticate(ctx, tokenHash(parts[1])); err == nil {
		return link
	}
	token := randomToken()
	now := time.Now()
	record, err := m.store.exchange(
		ctx,
		tokenHash(parts[1]),
		DeviceRecord{
			ID:         randomID(),
			Label:      "Test Chrome",
			TokenHash:  tokenHash(token),
			CreatedAt:  now,
			LastSeenAt: now,
			ExpiresAt:  now.Add(deviceLifetime),
			RenewAfter: now.Add(renewalInterval),
		},
	)
	require.NoError(t, err)
	m.disconnectScope(record.scope())
	return parts[0] + "#" + token
}

func TestPairingIsSingleUseAndRotationIsRetryable(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	scope := Scope{1, "alice"}
	now := time.Now()
	pair, device, next := randomToken(), randomToken(), randomToken()
	require.NoError(
		t,
		s.createPair(
			ctx,
			PairingRecord{scope.key(), tokenHash(pair), scope.Tenant, scope.User, now.Add(pairingLifetime)},
		),
	)
	r := DeviceRecord{
		ID:         randomID(),
		Label:      "Chrome",
		TokenHash:  tokenHash(device),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(deviceLifetime),
		RenewAfter: now.Add(renewalInterval),
	}
	created, err := s.exchange(ctx, tokenHash(pair), r)
	require.NoError(t, err)
	_, err = s.exchange(ctx, tokenHash(pair), r)
	require.ErrorIs(t, err, ErrAuthorization)
	_, err = s.authenticate(ctx, tokenHash(pair))
	require.ErrorIs(t, err, ErrAuthorization)
	authorized, err := s.authenticate(ctx, tokenHash(device))
	require.NoError(t, err)
	require.Equal(t, scope, authorized.scope())
	rotated, err := s.renew(ctx, tokenHash(device), tokenHash(next))
	require.NoError(t, err)
	require.Equal(t, created.ID, rotated.ID)
	require.NoError(
		t,
		s.db.Model(&DeviceRecord{}).Where("id = ?", created.ID).Update("previous_until", now.Add(-time.Minute)).Error,
	)
	// Lost response, followed by browser/server restart and retry with persisted candidate.
	retryStore := &Store{db: s.db}
	retried, err := retryStore.renew(ctx, tokenHash(device), tokenHash(next))
	require.NoError(t, err)
	require.Equal(t, rotated.TokenHash, retried.TokenHash)
	_, err = s.authenticate(ctx, tokenHash(device))
	require.ErrorIs(t, err, ErrAuthorization)
	require.NoError(t, s.revoke(ctx, scope))
	_, err = s.authenticate(ctx, tokenHash(next))
	require.ErrorIs(t, err, ErrAuthorization)
	var saved DeviceRecord
	require.NoError(t, s.db.First(&saved).Error)
	require.NotContains(t, saved.TokenHash, device)
	require.Len(t, saved.TokenHash, 64)
}

func TestExpiredPairingAndLeaseFencing(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	scope := Scope{1, "alice"}
	now := time.Now()
	require.NoError(
		t,
		s.createPair(
			ctx,
			PairingRecord{scope.key(), tokenHash("expired"), scope.Tenant, scope.User, now.Add(-time.Minute)},
		),
	)
	_, err := s.exchange(ctx, tokenHash("expired"), DeviceRecord{})
	require.ErrorIs(t, err, ErrAuthorization)
	r := DeviceRecord{
		ScopeKey:  scope.key(),
		ID:        randomID(),
		TokenHash: tokenHash(randomToken()),
		Tenant:    scope.Tenant,
		User:      scope.User,
		ExpiresAt: now.Add(deviceLifetime),
	}
	require.NoError(t, s.db.Create(&r).Error)
	require.NoError(t, s.claim(ctx, &r, "node-a", "http://node-a", "lease-a"))
	require.ErrorIs(t, s.claim(ctx, &r, "node-b", "http://node-b", "lease-b"), ErrLeaseHeld)
	require.NoError(
		t,
		s.db.Model(&DeviceRecord{}).Where("id = ?", r.ID).Update("lease_until", now.Add(-time.Second)).Error,
	)
	require.NoError(t, s.claim(ctx, &r, "node-b", "http://node-b", "lease-b"))
	_, err = s.heartbeat(ctx, r.ID, "lease-a")
	require.Error(t, err)
	require.NoError(t, s.release(ctx, r.ID, "lease-a"))
	got, err := s.account(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, "node-b", got.Owner)
}

func TestSuspendedMembershipInvalidatesDevice(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.checkMember = true
	require.NoError(
		t,
		s.db.Exec(
			`CREATE TABLE users (id TEXT PRIMARY KEY,is_active BOOLEAN,deleted_at DATETIME,
 can_access_all_tenants BOOLEAN DEFAULT 0,tenant_id BIGINT DEFAULT 1)`,
		).Error,
	)
	require.NoError(
		t,
		s.db.Exec(`CREATE TABLE tenant_members (user_id TEXT,tenant_id BIGINT,status TEXT,deleted_at DATETIME)`).Error,
	)
	require.NoError(t, s.db.Exec(`INSERT INTO users(id,is_active) VALUES ('alice',1)`).Error)
	require.NoError(
		t,
		s.db.Exec(`INSERT INTO tenant_members(user_id,tenant_id,status) VALUES ('alice',1,'active')`).Error,
	)
	scope := Scope{1, "alice"}
	token := randomToken()
	r := DeviceRecord{
		ScopeKey:  scope.key(),
		ID:        randomID(),
		TokenHash: tokenHash(token),
		Tenant:    1,
		User:      "alice",
		ExpiresAt: time.Now().Add(deviceLifetime),
	}
	pair := randomToken()
	require.NoError(
		t,
		s.createPair(
			ctx,
			PairingRecord{scope.key(), tokenHash(pair), scope.Tenant, scope.User, time.Now().Add(pairingLifetime)},
		),
	)
	_, err := s.exchange(ctx, tokenHash(pair), r)
	require.NoError(t, err)
	_, err = s.authenticate(ctx, tokenHash(token))
	require.NoError(t, err)
	require.NoError(t, s.db.Exec(`UPDATE tenant_members SET status='suspended'`).Error)
	_, err = s.authenticate(ctx, tokenHash(token))
	require.ErrorIs(t, err, ErrAuthorization)
}

func TestCrossTenantAdministratorMembership(t *testing.T) {
	s := testStore(t)
	s.checkMember = true
	ctx := context.Background()
	require.NoError(
		t,
		s.db.Exec(
			`CREATE TABLE users (id TEXT PRIMARY KEY,is_active BOOLEAN,deleted_at DATETIME,
 can_access_all_tenants BOOLEAN,tenant_id BIGINT)`,
		).Error,
	)
	require.NoError(
		t,
		s.db.Exec(`CREATE TABLE tenant_members (user_id TEXT,tenant_id BIGINT,status TEXT,deleted_at DATETIME)`).Error,
	)
	require.NoError(t, s.db.Exec(`INSERT INTO users VALUES ('admin',1,NULL,1,1)`).Error)
	require.NoError(t, s.member(ctx, Scope{2, "admin"}))
	require.ErrorIs(t, s.member(ctx, Scope{1, "admin"}), ErrAuthorization)
	require.NoError(t, s.db.Exec(`UPDATE users SET can_access_all_tenants=0`).Error)
	require.ErrorIs(t, s.member(ctx, Scope{2, "admin"}), ErrAuthorization)
}
