package container

import (
	"context"
	"fmt"
	"strings"
	"time"

	esv7 "github.com/elastic/go-elasticsearch/v7"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
	"github.com/qdrant/go-client/qdrant"
	"github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/auth"
	wgrpc "github.com/weaviate/weaviate-go-client/v5/weaviate/grpc"
	"google.golang.org/grpc"
	"gorm.io/gorm"

	elasticsearchRepoV7 "github.com/Tencent/WeKnora/internal/application/repository/retriever/elasticsearch/v7"
	elasticsearchRepoV8 "github.com/Tencent/WeKnora/internal/application/repository/retriever/elasticsearch/v8"
	milvusRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/milvus"
	postgresRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/postgres"
	qdrantRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/qdrant"
	sqliteRetrieverRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/sqlite"
	weaviateRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/weaviate"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// NewEngineFactory returns an EngineFactory function closed over db and cfg.
// Registered in dig and injected into VectorStoreService for dynamic registry updates.
func NewEngineFactory(db *gorm.DB, cfg *config.Config) interfaces.EngineFactory {
	return func(ctx context.Context, store types.VectorStore) (interfaces.RetrieveEngineService, error) {
		return createEngineServiceFromStore(ctx, store, db, cfg)
	}
}

// createEngineServiceFromStore creates a RetrieveEngineService from a VectorStore's config.
// This is the DB store counterpart of the env-based initialization in initRetrieveEngineRegistry.
func createEngineServiceFromStore(
	ctx context.Context,
	store types.VectorStore,
	db *gorm.DB,
	cfg *config.Config,
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
	case types.SQLiteRetrieverEngineType:
		return createSQLiteEngine(store, db)
	default:
		return nil, fmt.Errorf("unsupported engine type: %s", store.EngineType)
	}
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
	cc := store.ConnectionConfig
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
	// NOTE: Milvus DBName is not yet in ConnectionConfig.
	// Phase 1 limitation — only the default database is used.

	client, err := milvusclient.New(ctx, &milvusCfg)
	if err != nil {
		return nil, fmt.Errorf("create milvus client: %w", err)
	}
	repo := milvusRepo.NewMilvusRetrieveEngineRepository(client, &store.IndexConfig)
	return retriever.NewKVHybridRetrieveEngine(repo, types.MilvusRetrieverEngineType), nil
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
