package container

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	gormmysql "gorm.io/driver/mysql"
)

func TestBuildMilvusClientConfig_UsesDatabaseName(t *testing.T) {
	cfg := buildMilvusClientConfig(types.ConnectionConfig{
		Addr:     "milvus.example.com:19530",
		Username: "tester",
		Password: "secret",
		Database: "regdi_ram_haom1",
	})

	if cfg.Address != "milvus.example.com:19530" {
		t.Fatalf("expected address to be preserved, got %q", cfg.Address)
	}
	if cfg.Username != "tester" {
		t.Fatalf("expected username to be preserved, got %q", cfg.Username)
	}
	if cfg.Password != "secret" {
		t.Fatalf("expected password to be preserved, got %q", cfg.Password)
	}
	if cfg.DBName != "regdi_ram_haom1" {
		t.Fatalf("expected DBName to be propagated, got %q", cfg.DBName)
	}
	if len(cfg.DialOptions) != 1 {
		t.Fatalf("expected one dial option, got %d", len(cfg.DialOptions))
	}
}

func TestBuildMilvusClientConfig_DefaultsAddressWhenMissing(t *testing.T) {
	cfg := buildMilvusClientConfig(types.ConnectionConfig{})

	if cfg.Address != "localhost:19530" {
		t.Fatalf("expected default address localhost:19530, got %q", cfg.Address)
	}
	if cfg.DBName != "" {
		t.Fatalf("expected empty DBName by default, got %q", cfg.DBName)
	}
	if len(cfg.DialOptions) != 1 {
		t.Fatalf("expected one dial option, got %d", len(cfg.DialOptions))
	}
}

func TestCreateMySQLEngine_WithMockDB(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := types.VectorStore{
		ID:         "store-mysql",
		Name:       "mysql",
		EngineType: types.MySQLRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{
			Addr:     "mysql:3306",
			Database: "weknora",
			Username: "root",
			Password: "p@ss:word/with/slash",
		},
		IndexConfig: types.IndexConfig{CollectionPrefix: "tenant_embeddings"},
	}

	svc, err := createMySQLEngineWithDialector(store, gormmysql.New(gormmysql.Config{
		Conn:                      db,
		SkipInitializeWithVersion: true,
	}))
	require.NoError(t, err)
	require.Equal(t, types.MySQLRetrieverEngineType, svc.EngineType())
	require.NoError(t, mock.ExpectationsWereMet())
}
