package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	_ "github.com/go-sql-driver/mysql"   // MySQL driver for database/sql, used by Doris connection test
	_ "github.com/jackc/pgx/v5/stdlib"   // pgx driver for database/sql
	"github.com/qdrant/go-client/qdrant"
	"github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/auth"
	wgrpc "github.com/weaviate/weaviate-go-client/v5/weaviate/grpc"
)

const connectionTestTimeout = 10 * time.Second

// TestConnection tests connectivity to a vector database.
// Returns the detected server version on success (e.g., "7.10.1"), empty string if unknown.
func (s *vectorStoreService) TestConnection(
	ctx context.Context,
	engineType types.RetrieverEngineType,
	config types.ConnectionConfig,
) (string, error) {
	switch engineType {
	case types.ElasticsearchRetrieverEngineType:
		return testElasticsearchConnection(ctx, config)
	case types.PostgresRetrieverEngineType:
		return testPostgresConnection(ctx, config)
	case types.QdrantRetrieverEngineType:
		return testQdrantConnection(ctx, config)
	case types.MilvusRetrieverEngineType:
		return testMilvusConnection(ctx, config)
	case types.WeaviateRetrieverEngineType:
		return testWeaviateConnection(ctx, config)
	case types.DorisRetrieverEngineType:
		return testDorisConnection(ctx, config)
	case types.SQLiteRetrieverEngineType:
		// SQLite is file-based, no remote connection to test
		return "", nil
	default:
		return "", errors.NewBadRequestError(
			fmt.Sprintf("connection test not supported for engine type: %s", engineType))
	}
}

func testElasticsearchConnection(ctx context.Context, config types.ConnectionConfig) (string, error) {
	// Use plain HTTP GET to the root endpoint instead of the go-elasticsearch SDK.
	// The v8 SDK's TypedClient performs a product check that rejects ES7 servers,
	// so we use a raw HTTP request to support both v7 and v8.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, config.Addr, nil)
	if err != nil {
		return "", errors.NewBadRequestError("failed to create elasticsearch request: invalid address")
	}
	if config.Username != "" {
		req.SetBasicAuth(config.Username, config.Password)
	}

	client := &http.Client{Timeout: connectionTestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		logger.Warnf(ctx, "Elasticsearch connection test failed: %v", err)
		return "", errors.NewBadRequestError("failed to connect to elasticsearch: connection refused or authentication failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Warnf(ctx, "Elasticsearch connection test returned status %d", resp.StatusCode)
		return "", errors.NewBadRequestError("failed to connect to elasticsearch: authentication failed or server error")
	}

	// Parse version from response: {"version": {"number": "7.10.1"}, ...}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", nil // connected but version unknown
	}

	var esInfo struct {
		Version struct {
			Number string `json:"number"`
		} `json:"version"`
	}
	if err := json.Unmarshal(body, &esInfo); err != nil {
		return "", nil // connected but version unparseable
	}

	return esInfo.Version.Number, nil
}

func testPostgresConnection(ctx context.Context, config types.ConnectionConfig) (string, error) {
	testCtx, cancel := context.WithTimeout(ctx, connectionTestTimeout)
	defer cancel()

	if config.UseDefaultConnection {
		// Using the default app DB connection — always reachable if the app is running.
		// Cannot query version without a DB handle; return empty.
		return "", nil
	}

	db, err := sql.Open("pgx", config.Addr)
	if err != nil {
		return "", errors.NewBadRequestError("failed to create postgres connection: invalid configuration")
	}
	defer db.Close()

	if err := db.PingContext(testCtx); err != nil {
		logger.Warnf(ctx, "Postgres connection test failed: %v", err)
		return "", errors.NewBadRequestError("failed to connect to postgres: connection refused or authentication failed")
	}

	// Detect version
	var version string
	if err := db.QueryRowContext(testCtx, "SHOW server_version").Scan(&version); err != nil {
		logger.Warnf(ctx, "Postgres version detection failed: %v", err)
		return "", nil // connected but version unknown
	}

	return version, nil
}

func testQdrantConnection(ctx context.Context, config types.ConnectionConfig) (string, error) {
	testCtx, cancel := context.WithTimeout(ctx, connectionTestTimeout)
	defer cancel()

	port := config.Port
	if port == 0 {
		port = 6334
	}

	client, err := qdrant.NewClient(&qdrant.Config{
		Host:   config.Host,
		Port:   port,
		APIKey: config.APIKey,
		UseTLS: config.UseTLS,
	})
	if err != nil {
		return "", errors.NewBadRequestError("failed to create qdrant client: invalid configuration")
	}
	defer client.Close()

	result, err := client.HealthCheck(testCtx)
	if err != nil {
		logger.Warnf(ctx, "Qdrant connection test failed: %v", err)
		return "", errors.NewBadRequestError("failed to connect to qdrant: connection refused or authentication failed")
	}

	return result.GetVersion(), nil
}

func testMilvusConnection(ctx context.Context, config types.ConnectionConfig) (string, error) {
	// Use TCP dial instead of the Milvus SDK to avoid protobuf namespace conflict
	// between milvus-proto and qdrant-client (both register "common.proto").
	// A TCP dial is sufficient for connectivity verification; the Milvus SDK client
	// creation in container.go (PR 3) will validate full gRPC connectivity.
	// Version detection is not possible with TCP dial alone.
	testCtx, cancel := context.WithTimeout(ctx, connectionTestTimeout)
	defer cancel()

	addr := config.Addr
	if addr == "" {
		addr = "localhost:19530"
	}

	conn, err := (&net.Dialer{}).DialContext(testCtx, "tcp", addr)
	if err != nil {
		logger.Warnf(ctx, "Milvus connection test failed: %v", err)
		return "", errors.NewBadRequestError("failed to connect to milvus: connection refused or server unreachable")
	}
	defer conn.Close()

	return "", nil
}

func testWeaviateConnection(ctx context.Context, config types.ConnectionConfig) (string, error) {
	testCtx, cancel := context.WithTimeout(ctx, connectionTestTimeout)
	defer cancel()

	host := config.Host
	if host == "" {
		host = "weaviate:8080"
	}
	grpcAddress := config.GrpcAddress
	if grpcAddress == "" {
		grpcAddress = "weaviate:50051"
	}
	scheme := config.Scheme
	if scheme == "" {
		scheme = "http"
	}

	weaviateCfg := weaviate.Config{
		Host: host,
		GrpcConfig: &wgrpc.Config{
			Host: grpcAddress,
		},
		Scheme: scheme,
	}
	if config.APIKey != "" {
		weaviateCfg.AuthConfig = auth.ApiKey{Value: config.APIKey}
	}

	// Weaviate Go client v5 does not expose a Close() method;
	// it uses HTTP + gRPC transports that are managed internally.
	client, err := weaviate.NewClient(weaviateCfg)
	if err != nil {
		logger.Warnf(ctx, "Weaviate connection test failed: %v", err)
		return "", errors.NewBadRequestError("failed to create weaviate client: invalid configuration")
	}

	isReady, err := client.Misc().ReadyChecker().Do(testCtx)
	if err != nil || !isReady {
		logger.Warnf(ctx, "Weaviate connection test failed: ready=%v, err=%v", isReady, err)
		return "", errors.NewBadRequestError("failed to connect to weaviate: server not ready or authentication failed")
	}

	// Detect version via /v1/meta
	meta, err := client.Misc().MetaGetter().Do(testCtx)
	if err != nil || meta == nil {
		return "", nil // connected but version unknown
	}

	return meta.Version, nil
}

// testDorisConnection 通过 MySQL 协议（database/sql + go-sql-driver）
// Ping Doris FE 并查询 @@version。
//
// Doris 4.1 的 @@version 形如 "5.7.99 Doris-4.1.0"——前半段是兼容性表达式，
// 后半段才是真正的 Doris 版本号。这里直接返回原样字符串，由调用方按需展示。
func testDorisConnection(ctx context.Context, config types.ConnectionConfig) (string, error) {
	testCtx, cancel := context.WithTimeout(ctx, connectionTestTimeout)
	defer cancel()

	if config.Addr == "" {
		return "", errors.NewBadRequestError("failed to create doris connection: addr is required")
	}

	// Database 不强制要求；Ping 时无明确库则用 information_schema（任何 MySQL 兼容服务都有）。
	database := config.Database
	if database == "" {
		database = "information_schema"
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?timeout=5s",
		config.Username, config.Password, config.Addr, database)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return "", errors.NewBadRequestError("failed to create doris connection: invalid configuration")
	}
	defer db.Close()

	if err := db.PingContext(testCtx); err != nil {
		logger.Warnf(ctx, "Doris connection test failed: %v", err)
		return "", errors.NewBadRequestError("failed to connect to doris: connection refused or authentication failed")
	}

	var version string
	if err := db.QueryRowContext(testCtx, "SELECT @@version").Scan(&version); err != nil {
		logger.Warnf(ctx, "Doris version detection failed: %v", err)
		return "", nil
	}
	return version, nil
}
