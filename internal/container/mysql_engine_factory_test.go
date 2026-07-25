package container

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestSplitMySQLAddr(t *testing.T) {
	host, port := splitMySQLAddr("mysql:3307")
	if host != "mysql" || port != 3307 {
		t.Fatalf("splitMySQLAddr(mysql:3307) = %q,%d", host, port)
	}

	host, port = splitMySQLAddr("mysql")
	if host != "mysql" || port != 3306 {
		t.Fatalf("splitMySQLAddr(mysql) = %q,%d", host, port)
	}
}

func TestBuildMySQLRetrieverDSNIncludesSafeOptions(t *testing.T) {
	dsn := buildMySQLRetrieverDSN("mysql", 3306, "user:name", "p@ss/word:1", "weknora")
	for _, want := range []string{
		"tcp(mysql:3306)",
		"/weknora",
		"interpolateParams=true",
		"charset=utf8mb4",
	} {
		if !strings.Contains(dsn, want) {
			t.Fatalf("dsn missing %q in %s", want, dsn)
		}
	}
}

func TestCreateMySQLEngineAcceptsDefaults(t *testing.T) {
	store := types.VectorStore{
		EngineType: types.MySQLRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{
			Addr:     "mysql:3306",
			Database: "weknora",
		},
		IndexConfig: types.IndexConfig{CollectionPrefix: "custom"},
	}

	engine, err := createMySQLEngine(store)
	if err != nil {
		t.Fatalf("createMySQLEngine() error = %v", err)
	}
	if engine.EngineType() != types.MySQLRetrieverEngineType {
		t.Fatalf("engine type = %q", engine.EngineType())
	}
}
