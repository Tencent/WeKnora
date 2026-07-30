package container

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/database"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	gomysql "github.com/go-sql-driver/mysql"
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
	for _, want := range []string{"tcp(mysql:3306)", "/weknora", "charset=utf8mb4"} {
		if !strings.Contains(dsn, want) {
			t.Fatalf("dsn missing %q in %s", want, dsn)
		}
	}
	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if cfg.Loc.String() != "UTC" ||
		cfg.Params["time_zone"] != "'"+database.MySQLSessionTimeZone+"'" ||
		cfg.Params["sql_mode"] != "'"+database.MySQLSessionSQLMode+"'" {
		t.Fatalf("retriever DSN does not enforce the shared MySQL session contract: %#v", cfg)
	}
}

func TestMigrationStartupPolicyLimitsFailClosedChangeToMySQL(t *testing.T) {
	tests := []struct {
		driver          string
		autoRecoverEnv  string
		wantAutoRecover bool
		wantFailClosed  bool
	}{
		{driver: "mysql", wantAutoRecover: false, wantFailClosed: true},
		{driver: "mysql", autoRecoverEnv: "true", wantAutoRecover: false, wantFailClosed: true},
		{driver: "postgres", wantAutoRecover: true, wantFailClosed: false},
		{driver: "postgres", autoRecoverEnv: "false", wantAutoRecover: false, wantFailClosed: false},
		{driver: "sqlite", wantAutoRecover: true, wantFailClosed: false},
		{driver: "sqlite", autoRecoverEnv: "FALSE", wantAutoRecover: false, wantFailClosed: false},
	}
	for _, tt := range tests {
		t.Run(tt.driver+"/"+tt.autoRecoverEnv, func(t *testing.T) {
			autoRecover, failClosed := migrationStartupPolicy(tt.driver, tt.autoRecoverEnv)
			if autoRecover != tt.wantAutoRecover || failClosed != tt.wantFailClosed {
				t.Fatalf(
					"migrationStartupPolicy(%q, %q) = %v/%v, want %v/%v",
					tt.driver,
					tt.autoRecoverEnv,
					autoRecover,
					failClosed,
					tt.wantAutoRecover,
					tt.wantFailClosed,
				)
			}
		})
	}
}

func TestCreateMySQLEngineRejectsCanceledConnectionBeforeRegistration(t *testing.T) {
	store := types.VectorStore{
		EngineType: types.MySQLRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{
			Addr:     "127.0.0.1:1",
			Database: "weknora",
		},
		IndexConfig: types.IndexConfig{CollectionPrefix: "custom"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := createMySQLEngine(ctx, store); err == nil ||
		!strings.Contains(err.Error(), "connect to mysql retriever") ||
		!errors.Is(err, context.Canceled) {
		t.Fatalf("createMySQLEngine() error = %v, want eager connection failure", err)
	}
}

func TestCreateMySQLEngineRejectsIdentifierOverflowBeforeConnecting(t *testing.T) {
	store := types.VectorStore{
		EngineType: types.MySQLRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{
			Addr:     "unreachable.invalid:3306",
			Database: "weknora",
		},
		IndexConfig: types.IndexConfig{CollectionPrefix: strings.Repeat("a", 64)},
	}
	if _, err := createMySQLEngine(context.Background(), store); err == nil ||
		!strings.Contains(err.Error(), "MySQL") {
		t.Fatalf("createMySQLEngine() error = %v, want identifier validation", err)
	}
}

func TestCreateMySQLEngineIntegration(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MYSQL_TEST_DSN to run the real MySQL engine factory test")
	}
	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	store := types.VectorStore{
		EngineType: types.MySQLRetrieverEngineType,
		ConnectionConfig: types.ConnectionConfig{
			Addr:     cfg.Addr,
			Username: cfg.User,
			Password: cfg.Passwd,
			Database: cfg.DBName,
		},
		IndexConfig: types.IndexConfig{CollectionPrefix: "factory_test"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	engine, err := createMySQLEngine(ctx, store)
	if err != nil {
		t.Fatalf("createMySQLEngine() error = %v", err)
	}
	if engine.EngineType() != types.MySQLRetrieverEngineType {
		t.Fatalf("engine type = %q", engine.EngineType())
	}
}

type recordingStoreRegistry struct {
	registered []string
}

func (r *recordingStoreRegistry) RegisterWithStoreID(
	storeID string,
	_ interfaces.RetrieveEngineService,
) {
	r.registered = append(r.registered, storeID)
}

func (*recordingStoreRegistry) GetByStoreID(
	string,
) (interfaces.RetrieveEngineService, error) {
	return nil, errors.New("not registered")
}

func (*recordingStoreRegistry) UnregisterByStoreID(string) {}

func TestRegisterDBStoreEnginesUsesOneSharedStartupBudget(t *testing.T) {
	stores := []types.VectorStore{
		{ID: "slow", Name: "slow"},
		{ID: "must-not-start", Name: "must-not-start"},
	}
	registry := &recordingStoreRegistry{}
	callCount := 0
	started := time.Now()

	registerDBStoreEnginesWithinBudget(
		context.Background(),
		20*time.Millisecond,
		stores,
		registry,
		func(ctx context.Context, _ types.VectorStore) (interfaces.RetrieveEngineService, error) {
			callCount++
			<-ctx.Done()
			return nil, ctx.Err()
		},
	)

	if callCount != 1 {
		t.Fatalf("factory call count = %d, want 1 after shared budget expires", callCount)
	}
	if len(registry.registered) != 0 {
		t.Fatalf("registered stores = %v, want none", registry.registered)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("shared startup budget returned after %s, want under 3s", elapsed)
	}
}
