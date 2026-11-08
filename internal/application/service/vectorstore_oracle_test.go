package service

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateConnectionConfig_Oracle(t *testing.T) {
	ok := types.ConnectionConfig{Addr: "db:1521", Database: "FREEPDB1", Username: "u", Password: "p"}
	assert.NoError(t, validateConnectionConfig(types.OracleRetrieverEngineType, ok))

	for name, mutate := range map[string]func(*types.ConnectionConfig){
		"missing addr":     func(c *types.ConnectionConfig) { c.Addr = "" },
		"missing service":  func(c *types.ConnectionConfig) { c.Database = "" },
		"missing username": func(c *types.ConnectionConfig) { c.Username = "" },
		"missing password": func(c *types.ConnectionConfig) { c.Password = "" },
	} {
		c := ok
		mutate(&c)
		assert.Error(t, validateConnectionConfig(types.OracleRetrieverEngineType, c), name)
	}
}

func fakeWallet(content string) string {
	return base64.StdEncoding.EncodeToString([]byte(content))
}

func TestNormalizeWallet(t *testing.T) {
	defer utils.SetOracleWalletValidatorForTest(func([]byte) error { return nil })()

	cc := types.ConnectionConfig{Wallet: fakeWallet("sso")}
	require.NoError(t, normalizeWallet(types.OracleRetrieverEngineType, &cc))
	assert.Equal(t, fakeWallet("sso"), cc.Wallet)

	empty := types.ConnectionConfig{}
	assert.NoError(t, normalizeWallet(types.OracleRetrieverEngineType, &empty), "wallet is optional")

	assert.Error(t, normalizeWallet(types.QdrantRetrieverEngineType, &types.ConnectionConfig{Wallet: fakeWallet("x")}),
		"only oracle accepts a wallet")
	assert.Error(t, normalizeWallet(types.OracleRetrieverEngineType, &types.ConnectionConfig{Wallet: "%%%"}))
}

func TestUpdateStore_OracleWallet(t *testing.T) {
	defer utils.SetOracleWalletValidatorForTest(func([]byte) error { return nil })()
	ctx := context.Background()
	oracleStore := &types.VectorStore{
		ID: "vs-ora", TenantID: 1, Name: "ora", EngineType: types.OracleRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{Addr: "db:1522", Wallet: fakeWallet("old")},
	}
	qdrantStore := &types.VectorStore{ID: "vs-q", TenantID: 1, Name: "q", EngineType: types.QdrantRetrieverEngineType}
	repo := &mockVectorStoreRepo{stores: []*types.VectorStore{oracleStore, qdrantStore}}
	svc := NewVectorStoreService(repo, nil, nil, nil, nil)

	// No wallet in the update: the current one is kept.
	require.NoError(t, svc.UpdateStore(ctx, &types.VectorStore{ID: "vs-ora", TenantID: 1, Name: "renamed"}))
	assert.Equal(t, fakeWallet("old"), oracleStore.ConnectionConfig.Wallet)

	// A new wallet replaces it; other connection settings are untouched.
	require.NoError(t, svc.UpdateStore(ctx, &types.VectorStore{
		ID: "vs-ora", TenantID: 1, Name: "ora", ConnectionConfig: types.ConnectionConfig{Wallet: fakeWallet("new")},
	}))
	assert.Equal(t, fakeWallet("new"), oracleStore.ConnectionConfig.Wallet)
	assert.Equal(t, "db:1522", oracleStore.ConnectionConfig.Addr)

	err := svc.UpdateStore(ctx, &types.VectorStore{
		ID: "vs-q", TenantID: 1, Name: "q", ConnectionConfig: types.ConnectionConfig{Wallet: fakeWallet("x")},
	})
	assert.Error(t, err, "non-oracle stores reject a wallet")
}
