package service

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/Tencent/WeKnora/internal/errors"
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

// stubOracleConnection replaces the Oracle connection test for one test.
func stubOracleConnection(t *testing.T, fn func(context.Context, types.ConnectionConfig) (string, error)) {
	prev := oracleConnectionTester
	oracleConnectionTester = fn
	t.Cleanup(func() { oracleConnectionTester = prev })
}

func TestUpdateStore_OracleWallet(t *testing.T) {
	defer utils.SetOracleWalletValidatorForTest(func([]byte) error { return nil })()
	var tested []types.ConnectionConfig
	stubOracleConnection(t, func(_ context.Context, c types.ConnectionConfig) (string, error) {
		tested = append(tested, c)
		if c.Wallet == fakeWallet("bad") {
			return "", errors.NewBadRequestError("failed to connect to oracle")
		}
		return "23.26.0.0.0", nil
	})
	ctx := context.Background()
	oracleStore := &types.VectorStore{
		ID: "vs-ora", TenantID: 1, Name: "ora", EngineType: types.OracleRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{Addr: "db:1522", Wallet: fakeWallet("old")},
	}
	qdrantStore := &types.VectorStore{ID: "vs-q", TenantID: 1, Name: "q", EngineType: types.QdrantRetrieverEngineType}
	repo := &mockVectorStoreRepo{stores: []*types.VectorStore{oracleStore, qdrantStore}}
	svc := NewVectorStoreService(repo, nil, nil, nil, nil)

	// No wallet in the update: the current one is kept, nothing is tested.
	require.NoError(t, svc.UpdateStore(ctx, &types.VectorStore{ID: "vs-ora", TenantID: 1, Name: "renamed"}))
	assert.Equal(t, fakeWallet("old"), oracleStore.ConnectionConfig.Wallet)
	assert.Equal(t, "renamed", oracleStore.Name)
	assert.Empty(t, tested)

	// A wallet that fails the connection test is rejected before any write.
	err := svc.UpdateStore(ctx, &types.VectorStore{
		ID: "vs-ora", TenantID: 1, Name: "other", ConnectionConfig: types.ConnectionConfig{Wallet: fakeWallet("bad")},
	})
	var appErr *errors.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, errors.ErrBadRequest, appErr.Code)
	assert.Equal(t, fakeWallet("old"), oracleStore.ConnectionConfig.Wallet)
	assert.Equal(t, "renamed", oracleStore.Name, "the name is not half-applied")

	// A new wallet replaces it; other connection settings are untouched.
	require.NoError(t, svc.UpdateStore(ctx, &types.VectorStore{
		ID: "vs-ora", TenantID: 1, Name: "ora", ConnectionConfig: types.ConnectionConfig{Wallet: fakeWallet("new")},
	}))
	assert.Equal(t, fakeWallet("new"), oracleStore.ConnectionConfig.Wallet)
	assert.Equal(t, "db:1522", oracleStore.ConnectionConfig.Addr)
	assert.Equal(t, "23.26.0.0.0", oracleStore.ConnectionConfig.Version)
	require.Len(t, tested, 2)
	assert.Equal(t, "db:1522", tested[1].Addr, "the test runs on the existing config plus the new wallet")

	err = svc.UpdateStore(ctx, &types.VectorStore{
		ID: "vs-q", TenantID: 1, Name: "q", ConnectionConfig: types.ConnectionConfig{Wallet: fakeWallet("x")},
	})
	assert.Error(t, err, "non-oracle stores reject a wallet")
}

func TestOracleVersionSupported(t *testing.T) {
	for version, want := range map[string]bool{
		"19.24.0.0.0":  false, // no VECTOR type
		"23.4.0.24.05": false, // no DML on HNSW-indexed tables
		"23.5.0.24.07": false,
		"23.6.0.24.10": true,
		"23.7.0.25.01": true,
		"23.26.0.0.0":  true, // 26ai
		"26.1.0.0.0":   true,
		"23":           false,
		"unknown":      true, // undetectable version is not rejected
	} {
		assert.Equal(t, want, oracleVersionSupported(version), version)
	}
}
