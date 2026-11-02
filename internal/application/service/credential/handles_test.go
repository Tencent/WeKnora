package credential

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func newTestHandles(t *testing.T) (*HandleStore, *Vault, *miniredis.Miniredis) {
	t.Helper()
	vault, _ := newTestVault(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewHandleStore(rdb, vault), vault, mr
}

func onceHandle(owner Owner, session string) NewHandle {
	return NewHandle{Owner: owner, SessionID: session, Domains: []string{"a.com"}, Fields: []types.SecretField{
		{Name: "username", Type: types.SecretFieldText, Value: "alice"},
		{Name: "password", Type: types.SecretFieldPassword, Value: "sentinel-pw"},
	}}
}

func TestHandleStoreOnceRoundTrip(t *testing.T) {
	store, _, mr := newTestHandles(t)
	ctx := context.Background()
	h1, err := store.Create(ctx, onceHandle(alice, "s1"))
	require.NoError(t, err)
	h2, err := store.Create(ctx, onceHandle(alice, "s1"))
	require.NoError(t, err)
	assert.Equal(t, "cred_1", h1)
	assert.Equal(t, "cred_2", h2)

	for _, key := range mr.Keys() {
		v, _ := mr.Get(key)
		requireNoSecret(t, "sentinel-pw", v)
	}

	got, err := store.Resolve(ctx, alice.TenantID, alice.UserID, "s1", h1)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.com"}, got.Domains)
	require.Len(t, got.Fields, 2)
	assert.Equal(t, "sentinel-pw", got.Fields[1].Value)

	_, err = store.Resolve(ctx, alice.TenantID, alice.UserID, "s2", h1)
	assert.ErrorIs(t, err, types.ErrSecretHandleNotFound, "another session")
	_, err = store.Resolve(ctx, bob.TenantID, bob.UserID, "s1", h1)
	assert.ErrorIs(t, err, types.ErrSecretHandleNotFound, "another user")
	_, err = store.Resolve(ctx, alice.TenantID, alice.UserID, "s1", "cred_1:x")
	assert.ErrorIs(t, err, types.ErrSecretHandleNotFound, "malformed handle")

	require.NoError(t, store.Complete(ctx, alice.TenantID, alice.UserID, "s1", h1, ""))
	_, err = store.Resolve(ctx, alice.TenantID, alice.UserID, "s1", h1)
	assert.ErrorIs(t, err, types.ErrSecretHandleNotFound, "a completed handle is gone")

	mr.FastForward(HandleTTL + time.Second)
	_, err = store.Resolve(ctx, alice.TenantID, alice.UserID, "s1", h2)
	assert.ErrorIs(t, err, types.ErrSecretHandleNotFound, "expired")
}

func TestHandleStoreVaultSource(t *testing.T) {
	store, vault, _ := newTestHandles(t)
	ctx := context.Background()
	item, err := vault.Create(ctx, alice, LoginInput{Site: "a.com", Username: "alice", Password: "vault-pw"})
	require.NoError(t, err)
	h, err := store.Create(ctx, NewHandle{Owner: alice, SessionID: "s1", Domains: []string{"a.com"},
		VaultItemID: item.ID, Fields: []types.SecretField{
			{Name: "username", Type: types.SecretFieldText, Value: "alice"},
			{Name: "password", Type: types.SecretFieldPassword},
		}})
	require.NoError(t, err)
	got, err := store.Resolve(ctx, alice.TenantID, alice.UserID, "s1", h)
	require.NoError(t, err)
	assert.Equal(t, "vault-pw", got.Fields[1].Value)
	assert.Equal(t, item.ID, got.VaultItemID)

	require.NoError(t, store.Complete(ctx, alice.TenantID, alice.UserID, "s1", h, item.ID))
	touched, err := vault.Get(ctx, alice, item.ID)
	require.NoError(t, err)
	assert.NotNil(t, touched.LastUsedAt)

	h2, err := store.Create(ctx, NewHandle{Owner: alice, SessionID: "s1", Domains: []string{"a.com"},
		VaultItemID: item.ID, Fields: []types.SecretField{{Name: "password", Type: types.SecretFieldPassword}}})
	require.NoError(t, err)
	require.NoError(t, vault.Delete(ctx, alice, item.ID))
	_, err = store.Resolve(ctx, alice.TenantID, alice.UserID, "s1", h2)
	assert.ErrorIs(t, err, types.ErrCredentialUnavailable)
}

func TestHandleStoreRefusesWithoutKey(t *testing.T) {
	store, _, _ := newTestHandles(t)
	t.Setenv("SYSTEM_AES_KEY", "")
	_, err := store.Create(context.Background(), onceHandle(alice, "s1"))
	require.Error(t, err)
}

func TestNewHandleStoreNeedsRedis(t *testing.T) {
	vault, _ := newTestVault(t)
	assert.Nil(t, NewHandleStore(nil, vault))
}
