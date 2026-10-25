//go:build mysqlintegration

package browserskill

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestMySQLBrowserAuthorizationTimes(t *testing.T) {
	db := testutil.MySQL(t)
	s := &Store{db: db}
	ctx := context.Background()
	now := time.Now().UTC()
	// Each connection must reject zero dates, as the supported Compose deployment does.
	var mode string
	require.NoError(t, db.Raw("SELECT @@session.sql_mode").Scan(&mode).Error)
	require.Contains(t, mode, "STRICT_TRANS_TABLES")
	require.Contains(t, mode, "NO_ZERO_DATE")
	t.Run("first pairing and rotation", func(t *testing.T) {
		scope := Scope{1, "pair-user"}
		require.NoError(t, s.createPair(ctx, PairingRecord{
			ScopeKey: scope.key(), TokenHash: "pair", Tenant: scope.Tenant, User: scope.User,
			ExpiresAt: now.Add(pairingLifetime),
		}))
		device := DeviceRecord{
			ID: "paired", Label: "Chrome", TokenHash: "device", CreatedAt: now, LastSeenAt: now,
			ExpiresAt: now.Add(deviceLifetime), RenewAfter: now.Add(renewalInterval),
		}
		created, err := s.exchange(ctx, "pair", device)
		require.NoError(t, err)
		_, err = s.exchange(ctx, "pair", device)
		require.ErrorIs(t, err, ErrAuthorization)
		saved, err := s.authenticate(ctx, "device")
		require.NoError(t, err)
		require.True(t, saved.PreviousUntil.Before(now))
		require.True(t, saved.LeaseUntil.Before(now))
		require.Equal(t, created.ID, saved.ID)
		_, err = s.renew(ctx, "device", "rotated")
		require.NoError(t, err)
		require.NoError(t, s.revoke(ctx, scope))
		_, err = s.authenticate(ctx, "rotated")
		require.ErrorIs(t, err, ErrAuthorization)
	})
	for _, operation := range []string{"release", "releaseOwner", "revoke"} {
		t.Run(operation, func(t *testing.T) {
			scope := Scope{1, operation}
			device := DeviceRecord{
				ScopeKey: scope.key(), ID: operation, Tenant: scope.Tenant, User: scope.User,
				TokenHash: operation, Label: "Chrome", CreatedAt: now, LastSeenAt: now,
				ExpiresAt: now.Add(deviceLifetime), RenewAfter: now.Add(renewalInterval),
				PreviousUntil: now.Add(-time.Hour), LeaseUntil: now.Add(-time.Hour),
			}
			require.NoError(t, db.Create(&device).Error)
			require.NoError(t, s.claim(ctx, &device, operation, "http://node", "lease"))
			switch operation {
			case "release":
				require.NoError(t, s.release(ctx, device.ID, "stale-lease"))
				saved, err := s.account(ctx, scope)
				require.NoError(t, err)
				require.Equal(t, operation, saved.Owner)
				require.NoError(t, s.release(ctx, device.ID, "lease"))
			case "releaseOwner":
				require.NoError(t, s.releaseOwner(ctx, operation))
			case "revoke":
				require.NoError(t, s.revoke(ctx, scope))
			}
			saved, err := s.account(ctx, scope)
			require.NoError(t, err)
			require.Empty(t, saved.Owner)
			require.Empty(t, saved.OwnerURL)
			require.True(t, saved.LeaseUntil.Before(now))
			if operation == "revoke" {
				_, err = s.authenticate(ctx, device.TokenHash)
				require.ErrorIs(t, err, ErrAuthorization)
			} else {
				require.NoError(t, s.claim(ctx, &device, "new-owner", "http://new", "new-lease"))
			}
		})
	}
}
