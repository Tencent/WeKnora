package container

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	esv7 "github.com/elastic/go-elasticsearch/v7"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/go-sql-driver/mysql" // 通过 database/sql 注册 mysql 驱动给 Doris 使用
	"github.com/milvus-io/milvus/client/v2/milvusclient"
	"github.com/qdrant/go-client/qdrant"
	"github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/auth"
	wgrpc "github.com/weaviate/weaviate-go-client/v5/weaviate/grpc"
	"google.golang.org/grpc"
	"gorm.io/gorm"

	dorisRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/doris"
	elasticsearchRepoV7 "github.com/Tencent/WeKnora/internal/application/repository/retriever/elasticsearch/v7"
	elasticsearchRepoV8 "github.com/Tencent/WeKnora/internal/application/repository/retriever/elasticsearch/v8"
	milvusRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/milvus"
	openSearchRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/opensearch"
	postgresRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/postgres"
	qdrantRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/qdrant"
	sqliteRetrieverRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/sqlite"
	tencentVectorDBRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/tencentvectordb"
	weaviateRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/weaviate"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
)

// NewEngineFactory returns an EngineFactory function closed over db, cfg, and
// an audit sink (built from the AuditLogService). Registered in dig and
// injected into VectorStoreService for dynamic registry updates. The
// EngineFactory type itself is unchanged — the audit sink is captured in the
// closure rather than added to the signature.
func NewEngineFactory(db *gorm.DB, cfg *config.Config, auditSvc interfaces.AuditLogService) interfaces.EngineFactory {
	sink := newAuditSinkAdapter(auditSvc)
	return func(ctx context.Context, store types.VectorStore) (interfaces.RetrieveEngineService, error) {
		return createEngineServiceFromStore(ctx, store, db, cfg, sink)
	}
}

// createEngineServiceFromStore creates a RetrieveEngineService from a VectorStore's config.
// This is the DB store counterpart of the env-based initialization in initRetrieveEngineRegistry.
// auditSink may be nil (audit becomes a no-op).
func createEngineServiceFromStore(
	ctx context.Context,
	store types.VectorStore,
	db *gorm.DB,
	cfg *config.Config,
	auditSink openSearchRepo.AuditSink,
) (interfaces.RetrieveEngineService, error) {
	switch store.EngineType {
	case types.PostgresRetrieverEngineType:
		return createPostgresEngine(store, db)
	case types.ElasticsearchRetrieverEngineType:
		return createElasticsearchEngine(store, cfg)
	case types.QdrantRetrieverEngineType:
		return createQdrantEngine(store)
	case types.MilvusRetrieverEngineType:
		return createMilvusEngine(ctx, store)
	case types.WeaviateRetrieverEngineType:
		return createWeaviateEngine(store)
	case types.DorisRetrieverEngineType:
		return createDorisEngine(store)
	case types.SQLiteRetrieverEngineType:
		return createSQLiteEngine(store, db)
	case types.TencentVectorDBRetrieverEngineType:
		return createTencentVectorDBEngine(store)
	case types.OpenSearchRetrieverEngineType:
		return createOpenSearchEngine(ctx, store, auditSink)
	default:
		return nil, fmt.Errorf("unsupported engine type: %s", store.EngineType)
	}
}

// createOpenSearchEngine builds an OpenSearch k-NN retrieve engine. Mirrors
// createElasticsearchV8Engine but uses the driver's TLS-hardened client
// constructor and injects the audit sink. NewRepository probes the cluster
// (version + k-NN plugin), so an unreachable cluster fails here at
// registration rather than on first query.
func createOpenSearchEngine(
	ctx context.Context, store types.VectorStore, auditSink openSearchRepo.AuditSink,
) (interfaces.RetrieveEngineService, error) {
	client, err := openSearchRepo.NewOpenSearchClient(&store.ConnectionConfig)
	if err != nil {
		return nil, fmt.Errorf("create opensearch client: %w", err)
	}
	// Env stores share the cluster without a per-store index prefix; DB stores
	// fold their (>=16-char) ID into the index name. NewRepository enforces the
	// length rule, so map env-store IDs to "".
	storeID := store.ID
	if types.IsEnvStoreID(storeID) {
		storeID = ""
	}
	repo, err := openSearchRepo.NewRepository(ctx, client, storeID, &store.IndexConfig,
		openSearchRepo.WithAuditSink(auditSink))
	if err != nil {
		return nil, fmt.Errorf("create opensearch repository: %w", err)
	}
	return retriever.NewKVHybridRetrieveEngine(repo, types.OpenSearchRetrieverEngineType), nil
}

func createPostgresEngine(store types.VectorStore, db *gorm.DB) (interfaces.RetrieveEngineService, error) {
	if store.ConnectionConfig.UseDefaultConnection {
		repo := postgresRepo.NewPostgresRetrieveEngineRepository(db)
		return retriever.NewKVHybridRetrieveEngine(repo, types.PostgresRetrieverEngineType), nil
	}
	// Phase 1: only UseDefaultConnection is supported.
	// Custom connections require connection pool management and migration handling.
	return nil, fmt.Errorf("custom postgres connections not yet supported; use use_default_connection=true")
}

func createSQLiteEngine(_ types.VectorStore, db *gorm.DB) (interfaces.RetrieveEngineService, error) {
	repo := sqliteRetrieverRepo.NewSQLiteRetrieveEngineRepository(db)
	return retriever.NewKVHybridRetrieveEngine(repo, types.SQLiteRetrieverEngineType), nil
}

func createElasticsearchEngine(store types.VectorStore, cfg *config.Config) (interfaces.RetrieveEngineService, error) {
	cc := store.ConnectionConfig
	// Version-based v7/v8 SDK selection.
	// Version is auto-detected by PR2's TestConnection and saved to connection_config.
	// Empty version defaults to v8 (latest SDK).
	if isESv7(cc.Version) {
		return createElasticsearchV7Engine(store, cfg)
	}
	return createElasticsearchV8Engine(store, cfg)
}

// isESv7 checks if the detected ES version is 7.x.
func isESv7(version string) bool {
	return strings.HasPrefix(version, "7.")
}

func createElasticsearchV8Engine(store types.VectorStore, cfg *config.Config) (interfaces.RetrieveEngineService, error) {
	cc := store.ConnectionConfig
	client, err := elasticsearch.NewTypedClient(elasticsearch.Config{
		Addresses: []string{cc.Addr},
		Username:  cc.Username,
		Password:  cc.Password,
	})
	if err != nil {
		return nil, fmt.Errorf("create elasticsearch v8 client: %w", err)
	}
	repo := elasticsearchRepoV8.NewElasticsearchEngineRepository(client, cfg, &store.IndexConfig)
	return retriever.NewKVHybridRetrieveEngine(repo, types.ElasticsearchRetrieverEngineType), nil
}

func createElasticsearchV7Engine(store types.VectorStore, cfg *config.Config) (interfaces.RetrieveEngineService, error) {
	cc := store.ConnectionConfig
	client, err := esv7.NewClient(esv7.Config{
		Addresses: []string{cc.Addr},
		Username:  cc.Username,
		Password:  cc.Password,
	})
	if err != nil {
		return nil, fmt.Errorf("create elasticsearch v7 client: %w", err)
	}
	repo := elasticsearchRepoV7.NewElasticsearchEngineRepository(client, cfg, &store.IndexConfig)
	return retriever.NewKVHybridRetrieveEngine(repo, types.ElasticsearchRetrieverEngineType), nil
}

func createQdrantEngine(store types.VectorStore) (interfaces.RetrieveEngineService, error) {
	cc := store.ConnectionConfig
	port := cc.Port
	if port == 0 {
		port = 6334
	}

	client, err := qdrant.NewClient(&qdrant.Config{
		Host:   cc.Host,
		Port:   port,
		APIKey: cc.APIKey,
		UseTLS: cc.UseTLS,
	})
	if err != nil {
		return nil, fmt.Errorf("create qdrant client: %w", err)
	}
	repo := qdrantRepo.NewQdrantRetrieveEngineRepository(client, &store.IndexConfig)
	return retriever.NewKVHybridRetrieveEngine(repo, types.QdrantRetrieverEngineType), nil
}

func createMilvusEngine(ctx context.Context, store types.VectorStore) (interfaces.RetrieveEngineService, error) {
	milvusCfg := buildMilvusClientConfig(store.ConnectionConfig)
	client, err := milvusclient.New(ctx, &milvusCfg)
	if err != nil {
		return nil, fmt.Errorf("create milvus client: %w", err)
	}
	repo := milvusRepo.NewMilvusRetrieveEngineRepository(client, &store.IndexConfig)
	return retriever.NewKVHybridRetrieveEngine(repo, types.MilvusRetrieverEngineType), nil
}

func buildMilvusClientConfig(cc types.ConnectionConfig) milvusclient.ClientConfig {
	addr := cc.Addr
	if addr == "" {
		addr = "localhost:19530"
	}

	milvusCfg := milvusclient.ClientConfig{
		Address:     addr,
		DialOptions: []grpc.DialOption{grpc.WithTimeout(5 * time.Second)},
	}
	if cc.Username != "" {
		milvusCfg.Username = cc.Username
	}
	if cc.Password != "" {
		milvusCfg.Password = cc.Password
	}
	if cc.Database != "" {
		milvusCfg.DBName = cc.Database
	}
	return milvusCfg
}

func createWeaviateEngine(store types.VectorStore) (interfaces.RetrieveEngineService, error) {
	cc := store.ConnectionConfig
	host := cc.Host
	if host == "" {
		host = "weaviate:8080"
	}
	grpcAddress := cc.GrpcAddress
	if grpcAddress == "" {
		grpcAddress = "weaviate:50051"
	}
	scheme := cc.Scheme
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
	// Unlike the env path (which checks WEAVIATE_AUTH_ENABLED), the factory uses
	// APIKey directly — if a user provides it, they intend to use it.
	if cc.APIKey != "" {
		weaviateCfg.AuthConfig = auth.ApiKey{Value: cc.APIKey}
	}

	client, err := weaviate.NewClient(weaviateCfg)
	if err != nil {
		return nil, fmt.Errorf("create weaviate client: %w", err)
	}
	repo := weaviateRepo.NewWeaviateRetrieveEngineRepository(client, &store.IndexConfig)
	return retriever.NewKVHybridRetrieveEngine(repo, types.WeaviateRetrieverEngineType), nil
}

// createDorisEngine 创建 Apache Doris 检索引擎服务。
//
// Doris 同时使用两个端口：
//   - MySQL 协议（默认 9030）走 database/sql 做主链路读写；
//   - HTTP（默认 FE 8030）走 Stream Load 做 partial update。
//
// Addr 字段承担 host:9030 的 MySQL 端点；HTTPPort + Addr 的 host 部分组成 HTTP base URL。
func createDorisEngine(store types.VectorStore) (interfaces.RetrieveEngineService, error) {
	cc := store.ConnectionConfig
	if cc.Addr == "" {
		return nil, fmt.Errorf("doris connection requires addr (host:port)")
	}
	if cc.Database == "" {
		return nil, fmt.Errorf("doris connection requires database")
	}

	mc := mysql.NewConfig()
	mc.User = cc.Username
	mc.Passwd = cc.Password
	mc.Net = "tcp"
	mc.Addr = cc.Addr
	mc.DBName = cc.Database
	mc.Params = map[string]string{"charset": "utf8mb4"}
	mc.ParseTime = true
	mc.Loc = time.Local
	db, err := sql.Open("mysql", mc.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("create doris client: %w", err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	httpPort := cc.HTTPPort
	if httpPort <= 0 {
		httpPort = 8030
	}
	httpBase := "http://" + hostFromAddr(cc.Addr) + ":" + strconv.Itoa(httpPort)

	repo := dorisRepo.NewDorisRetrieveEngineRepository(
		db, httpBase, cc.Username, cc.Password, cc.Database, &store.IndexConfig,
	)
	return retriever.NewKVHybridRetrieveEngine(repo, types.DorisRetrieverEngineType), nil
}

// hostFromAddr 从 "host:port" 中拆出 host 部分；Addr 没有冒号时整段当作 host。
func hostFromAddr(addr string) string {
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}

func createTencentVectorDBEngine(store types.VectorStore) (interfaces.RetrieveEngineService, error) {
	cc := store.ConnectionConfig
	client, err := tcvectordb.NewRpcClient(cc.Addr, cc.Username, cc.APIKey, &tcvectordb.ClientOption{
		ReadConsistency: tcvectordb.EventualConsistency,
		Timeout:         10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("create tencent vectordb client: %w", err)
	}
	repo := tencentVectorDBRepo.NewTencentVectorDBRetrieveEngineRepository(client, cc.Database, &store.IndexConfig)
	return retriever.NewKVHybridRetrieveEngine(repo, types.TencentVectorDBRetrieverEngineType), nil
}
