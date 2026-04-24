// Package container implements dependency injection container setup
// Provides centralized configuration for services, repositories, and handlers
// This package is responsible for wiring up all dependencies and ensuring proper lifecycle management
package container

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/duckdb/duckdb-go/v2"
	esv7 "github.com/elastic/go-elasticsearch/v7"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/panjf2000/ants/v2"
	"github.com/qdrant/go-client/qdrant"
	"github.com/redis/go-redis/v9"
	"go.uber.org/dig"
	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	memoryRepo "github.com/Tencent/WeKnora/internal/application/repository/memory/neo4j"
	elasticsearchRepoV7 "github.com/Tencent/WeKnora/internal/application/repository/retriever/elasticsearch/v7"
	elasticsearchRepoV8 "github.com/Tencent/WeKnora/internal/application/repository/retriever/elasticsearch/v8"
	milvusRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/milvus"
	neo4jRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/neo4j"
	postgresRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/postgres"
	qdrantRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/qdrant"
	sqliteRetrieverRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/sqlite"
	weaviateRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/weaviate"
	"github.com/Tencent/WeKnora/internal/application/service"
	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/application/service/llmcontext"
	memoryService "github.com/Tencent/WeKnora/internal/application/service/memory"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/database"
	"github.com/Tencent/WeKnora/internal/datasource"
	feishuConnector "github.com/Tencent/WeKnora/internal/datasource/connector/feishu"
	notionConnector "github.com/Tencent/WeKnora/internal/datasource/connector/notion"
	yuqueConnector "github.com/Tencent/WeKnora/internal/datasource/connector/yuque"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/handler/session"
	imPkg "github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/im/dingtalk"
	"github.com/Tencent/WeKnora/internal/im/feishu"
	"github.com/Tencent/WeKnora/internal/im/mattermost"
	"github.com/Tencent/WeKnora/internal/im/slack"
	"github.com/Tencent/WeKnora/internal/im/telegram"
	"github.com/Tencent/WeKnora/internal/im/wechat"
	"github.com/Tencent/WeKnora/internal/im/wecom"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	infra_web_search "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/router"
	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/tracing"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	slackpkg "github.com/slack-go/slack"
	"github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/auth"
	wgrpc "github.com/weaviate/weaviate-go-client/v5/weaviate/grpc"
)

// BuildContainer constructs the dependency injection container
// Registers all components, services, repositories and handlers needed by the application
// Creates a fully configured application container with proper dependency resolution
// Parameters:
//   - container: Base dig container to add dependencies to
//
// Returns:
//   - Configured container with all application dependencies registered
func BuildContainer(container *dig.Container) *dig.Container {
	ctx := context.Background()
	logger.Debugf(ctx, "[Container] Starting container initialization...")

	// Register resource cleaner for proper cleanup of resources
	must(container.Provide(NewResourceCleaner, dig.As(new(interfaces.ResourceCleaner))))

	// Core infrastructure configuration
	logger.Debugf(ctx, "[Container] Registering core infrastructure...")
	must(container.Provide(config.LoadConfig))
	must(container.Provide(initTracer))
	must(container.Provide(initLangfuse))
	must(container.Provide(initDatabase))
	must(container.Provide(initFileService))
	must(container.Provide(initRedisClient))
	must(container.Provide(initAntsPool))
	must(container.Provide(initContextStorage))

	// Register tracer cleanup handler (tracer needs to be available for cleanup registration)
	must(container.Invoke(registerTracerCleanup))
	must(container.Invoke(registerLangfuseCleanup))

	// Register goroutine pool cleanup handler
	must(container.Invoke(registerPoolCleanup))

	// Initialize retrieval engine registry for search capabilities
	logger.Debugf(ctx, "[Container] Registering retrieval engine registry...")
	must(container.Provide(initRetrieveEngineRegistry))

	// External service clients
	logger.Debugf(ctx, "[Container] Registering external service clients...")
	must(container.Provide(initDocReaderClient))
	must(container.Provide(docparser.NewImageResolver))
	must(container.Provide(initOllamaService))
	must(container.Provide(initNeo4jClient))
	must(container.Provide(stream.NewStreamManager))
	logger.Debugf(ctx, "[Container] Initializing DuckDB...")
	must(container.Provide(NewDuckDB))
	logger.Debugf(ctx, "[Container] DuckDB registered")

	// Data repositories layer
	logger.Debugf(ctx, "[Container] Registering repositories...")
	must(container.Provide(repository.NewTenantRepository))
	must(container.Provide(repository.NewKnowledgeBaseRepository))
	must(container.Provide(repository.NewKnowledgeRepository))
	must(container.Provide(repository.NewChunkRepository))
	must(container.Provide(repository.NewKnowledgeTagRepository))
	must(container.Provide(repository.NewSessionRepository))
	must(container.Provide(repository.NewMessageRepository))
	must(container.Provide(repository.NewModelRepository))
	must(container.Provide(repository.NewUserRepository))
	must(container.Provide(repository.NewAuthTokenRepository))
	must(container.Provide(neo4jRepo.NewNeo4jRepository))
	must(container.Provide(memoryRepo.NewMemoryRepository))
	must(container.Provide(repository.NewMCPServiceRepository))
	must(container.Provide(repository.NewCustomAgentRepository))
	must(container.Provide(repository.NewOrganizationRepository))
	must(container.Provide(repository.NewKBShareRepository))
	must(container.Provide(repository.NewAgentShareRepository))
	must(container.Provide(repository.NewTenantDisabledSharedAgentRepository))
	must(container.Provide(service.NewWebSearchStateService))
	must(container.Provide(repository.NewDataSourceRepository))
	must(container.Provide(repository.NewSyncLogRepository))
	must(container.Provide(repository.NewWikiPageRepository))

	// MCP manager for managing MCP client connections
	logger.Debugf(ctx, "[Container] Registering MCP manager...")
	must(container.Provide(mcp.NewMCPManager))

	// Business service layer
	logger.Debugf(ctx, "[Container] Registering business services...")
	must(container.Provide(service.NewTenantService))
	must(container.Provide(service.NewKnowledgeBaseService))
	must(container.Provide(service.NewOrganizationService))
	must(container.Provide(service.NewKBShareService)) // KBShareService must be registered before KnowledgeService and KnowledgeTagService
	must(container.Provide(service.NewAgentShareService))
	must(container.Provide(service.NewKnowledgeService))
	must(container.Provide(service.NewChunkService))
	must(container.Provide(service.NewKnowledgeTagService))
	must(container.Provide(embedding.NewBatchEmbedder))
	must(container.Provide(service.NewModelService))
	must(container.Provide(service.NewDatasetService))
	must(container.Provide(service.NewEvaluationService))
	must(container.Provide(service.NewUserService))
	must(container.Provide(service.NewWeKnoraCloudService))

	// Extract services - register individual extracters with names
	must(container.Provide(service.NewChunkExtractService, dig.Name("chunkExtractor")))
	must(container.Provide(service.NewDataTableSummaryService, dig.Name("dataTableSummary")))
	must(container.Provide(service.NewImageMultimodalService, dig.Name("imageMultimodal")))
	must(container.Provide(service.NewKnowledgePostProcessService, dig.Name("knowledgePostProcess")))

	must(container.Provide(service.NewMessageService))
	must(container.Provide(service.NewMCPServiceService))
	must(container.Provide(service.NewCustomAgentService))
	must(container.Provide(memoryService.NewMemoryService))
	must(container.Provide(service.NewWikiPageService))
	must(container.Provide(service.NewWikiIngestService, dig.Name("wikiIngest")))
	must(container.Provide(service.NewWikiLintService))

	// Web search service (needed by AgentService)
	logger.Debugf(ctx, "[Container] Registering web search registry and providers...")
	must(container.Provide(infra_web_search.NewRegistry))
	must(container.Invoke(registerWebSearchProviders))
	must(container.Provide(repository.NewWebSearchProviderRepository))
	must(container.Provide(repository.NewVectorStoreRepository))
	must(container.Provide(service.NewWebSearchService))
	must(container.Provide(service.NewWebSearchProviderService))
	must(container.Provide(NewEngineFactory))
	// StoreRegistry: same instance as RetrieveEngineRegistry, exposed as StoreRegistry interface.
	// NewRetrieveEngineRegistry always returns *retriever.RetrieveEngineRegistry which implements both.
	must(container.Provide(func(r interfaces.RetrieveEngineRegistry) (interfaces.StoreRegistry, error) {
		sr, ok := r.(*retriever.RetrieveEngineRegistry)
		if !ok {
			return nil, fmt.Errorf("registry does not implement StoreRegistry")
		}
		return sr, nil
	}))
	must(container.Provide(service.NewVectorStoreService))

	// Agent service layer (requires event bus, web search service)
	// SessionService is passed as parameter to CreateAgentEngine method when creating AgentService
	logger.Debugf(ctx, "[Container] Registering event bus and agent service...")
	must(container.Provide(event.NewEventBus))
	must(container.Provide(service.NewAgentService))

	// Session service (depends on agent service)
	// SessionService is created after AgentService and passes itself to AgentService.CreateAgentEngine when needed
	logger.Debugf(ctx, "[Container] Registering session service...")
	must(container.Provide(service.NewSessionService))

	logger.Debugf(ctx, "[Container] Registering task enqueuer...")
	redisAvailable := os.Getenv("REDIS_ADDR") != ""
	if redisAvailable {
		must(container.Provide(router.NewAsyncqClient, dig.As(new(interfaces.TaskEnqueuer))))
		must(container.Provide(router.NewAsynqServer))
	} else {
		syncExec := router.NewSyncTaskExecutor()
		must(container.Provide(func() interfaces.TaskEnqueuer { return syncExec }))
		must(container.Provide(func() *router.SyncTaskExecutor { return syncExec }))
	}

	// Chat pipeline components for processing chat requests
	logger.Debugf(ctx, "[Container] Registering chat pipeline plugins...")

	// Data source sync framework
	logger.Debugf(ctx, "[Container] Registering data source sync framework...")
	must(container.Provide(initConnectorRegistry))
	must(container.Provide(datasource.NewScheduler))
	must(container.Provide(service.NewDataSourceService))
	must(container.Invoke(startDataSourceScheduler))
	logger.Debugf(ctx, "[Container] Data source sync framework registered")
	must(container.Provide(chatpipeline.NewEventManager))
	must(container.Invoke(chatpipeline.NewPluginSearch))
	must(container.Invoke(chatpipeline.NewPluginRerank))
	must(container.Invoke(chatpipeline.NewPluginWebFetch))
	must(container.Invoke(chatpipeline.NewPluginMerge))
	must(container.Invoke(chatpipeline.NewPluginDataAnalysis))
	must(container.Invoke(chatpipeline.NewPluginIntoChatMessage))
	must(container.Invoke(chatpipeline.NewPluginChatCompletion))
	must(container.Invoke(chatpipeline.NewPluginChatCompletionStream))
	must(container.Invoke(chatpipeline.NewPluginFilterTopK))
	must(container.Invoke(chatpipeline.NewPluginQueryUnderstand))
	must(container.Invoke(chatpipeline.NewPluginLoadHistory))
	must(container.Invoke(chatpipeline.NewPluginExtractEntity))
	must(container.Invoke(chatpipeline.NewPluginSearchEntity))
	must(container.Invoke(chatpipeline.NewPluginSearchParallel))
	must(container.Invoke(chatpipeline.NewPluginWikiBoost))
	must(container.Invoke(chatpipeline.NewMemoryPlugin))
	logger.Debugf(ctx, "[Container] Chat pipeline plugins registered")

	// HTTP handlers layer
	logger.Debugf(ctx, "[Container] Registering HTTP handlers...")
	must(container.Provide(handler.NewTenantHandler))
	must(container.Provide(handler.NewKnowledgeBaseHandler))
	must(container.Provide(handler.NewKnowledgeHandler))
	must(container.Provide(handler.NewChunkHandler))
	must(container.Provide(handler.NewFAQHandler))
	must(container.Provide(handler.NewTagHandler))
	must(container.Provide(session.NewHandler))
	must(container.Provide(handler.NewMessageHandler))
	must(container.Provide(handler.NewModelHandler))
	must(container.Provide(handler.NewEvaluationHandler))
	must(container.Provide(handler.NewInitializationHandler))
	must(container.Provide(handler.NewAuthHandler))
	must(container.Provide(handler.NewSystemHandler))
	must(container.Provide(handler.NewMCPServiceHandler))
	must(container.Provide(handler.NewWebSearchHandler))
	must(container.Provide(handler.NewWebSearchProviderHandler))
	must(container.Provide(handler.NewVectorStoreHandler))
	must(container.Provide(handler.NewCustomAgentHandler))
	must(container.Provide(service.NewSkillService))
	must(container.Provide(handler.NewSkillHandler))
	must(container.Provide(handler.NewOrganizationHandler))

	// Data source handler
	must(container.Provide(handler.NewDataSourceHandler))
	// Wiki page handler
	must(container.Provide(handler.NewWikiPageHandler))
	// IM integration
	logger.Debugf(ctx, "[Container] Registering IM integration...")
	must(container.Provide(imPkg.NewService))
	must(container.Invoke(registerIMAdapterFactories))
	must(container.Provide(handler.NewIMHandler))
	must(container.Provide(handler.NewWeKnoraCloudHandler))
	logger.Debugf(ctx, "[Container] HTTP handlers registered")

	// Router configuration
	logger.Debugf(ctx, "[Container] Registering router and starting task server...")
	must(container.Provide(router.NewRouter))
	if redisAvailable {
		must(container.Invoke(router.RunAsynqServer))
	} else {
		must(container.Invoke(router.RegisterSyncHandlers))
	}

	logger.Infof(ctx, "[Container] Container initialization completed successfully")
	return container
}

// must is a helper function for error handling
// Panics if the error is not nil, useful for configuration steps that must succeed
// Parameters:
//   - err: Error to check
func must(err error) {
	if err != nil {
		panic(err)
	}
}

// initTracer initializes OpenTelemetry tracer
// Sets up distributed tracing for observability across the application
// Parameters:
//   - None
//
// Returns:
//   - Configured tracer instance
//   - Error if initialization fails
func initTracer() (*tracing.Tracer, error) {
	return tracing.InitTracer()
}

// initLangfuse initializes the Langfuse ingestion client.
// Configuration is read from LANGFUSE_* environment variables (see
// docs/langfuse.md). Returns a disabled manager if credentials are absent —
// never an error — so deployments that don't use Langfuse are unaffected.
func initLangfuse() (*langfuse.Manager, error) {
	cfg := langfuse.LoadConfigFromEnv()
	return langfuse.Init(cfg)
}

func initRedisClient() (*redis.Client, error) {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		logger.Infof(context.Background(), "[Redis] No REDIS_ADDR configured, Redis disabled (Lite mode)")
		return nil, nil
	}
	db, err := strconv.Atoi(os.Getenv("REDIS_DB"))
	if err != nil {
		db = 0
	}

	client := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Username: os.Getenv("REDIS_USERNAME"),
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       db,
	})

	_, err = client.Ping(context.Background()).Result()
	if err != nil {
		return nil, fmt.Errorf("连接Redis失败: %w", err)
	}

	return client, nil
}

func initContextStorage(redisClient *redis.Client) (llmcontext.ContextStorage, error) {
	if redisClient == nil {
		logger.Infof(context.Background(), "[ContextStorage] Redis not available, using in-memory storage")
		return llmcontext.NewMemoryStorage(), nil
	}
	storage, err := llmcontext.NewRedisStorage(redisClient, 24*time.Hour, "context:")
	if err != nil {
		return nil, err
	}
	return storage, nil
}

// initDatabase initializes database connection
// Creates and configures database connection based on environment configuration
// Supports multiple database backends (PostgreSQL)
// Parameters:
//   - cfg: Application configuration
//
// Returns:
//   - Configured database connection
//   - Error if connection fails
func initDatabase(cfg *config.Config) (*gorm.DB, error) {
	var dialector gorm.Dialector
	var migrateDSN string
	var sqliteDBPath string
	switch os.Getenv("DB_DRIVER") {
	case "postgres":
		// DSN for GORM (key-value format)
		gormDSN := fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s TimeZone=UTC",
			os.Getenv("DB_HOST"),
			os.Getenv("DB_PORT"),
			os.Getenv("DB_USER"),
			os.Getenv("DB_PASSWORD"),
			os.Getenv("DB_NAME"),
			"disable",
		)
		dialector = postgres.Open(gormDSN)

		// DSN for golang-migrate (URL format)
		// URL-encode password to handle special characters like !@#
		dbPassword := os.Getenv("DB_PASSWORD")
		encodedPassword := url.QueryEscape(dbPassword)

		// Check if postgres is in RETRIEVE_DRIVER to determine skip_embedding
		retrieveDriver := strings.Split(os.Getenv("RETRIEVE_DRIVER"), ",")
		skipEmbedding := "true"
		if slices.Contains(retrieveDriver, "postgres") {
			skipEmbedding = "false"
		}
		logger.Infof(context.Background(), "Skip embedding: %s", skipEmbedding)

		migrateDSN = fmt.Sprintf(
			"postgres://%s:%s@%s:%s/%s?sslmode=disable&options=-c%%20app.skip_embedding=%s",
			os.Getenv("DB_USER"),
			encodedPassword, // Use encoded password
			os.Getenv("DB_HOST"),
			os.Getenv("DB_PORT"),
			os.Getenv("DB_NAME"),
			skipEmbedding,
		)

		// Debug log (don't log password)
		logger.Infof(context.Background(), "DB Config: user=%s host=%s port=%s dbname=%s",
			os.Getenv("DB_USER"),
			os.Getenv("DB_HOST"),
			os.Getenv("DB_PORT"),
			os.Getenv("DB_NAME"),
		)
	case "sqlite":
		dbPath := os.Getenv("DB_PATH")
		if dbPath == "" {
			dbPath = "./data/weknora.db"
		}
		if dir := filepath.Dir(dbPath); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("failed to create SQLite data directory %s: %w", dir, err)
			}
		}
		sqlite_vec.Auto()
		dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
		dialector = sqlite.Open(dsn)
		sqliteDBPath = dbPath
		migrateDSN = "sqlite3://" + dbPath
		logger.Infof(context.Background(), "DB Config: driver=sqlite path=%s", dbPath)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", os.Getenv("DB_DRIVER"))
	}
	db, err := gorm.Open(dialector, &gorm.Config{
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
	})
	if err != nil {
		return nil, err
	}

	if os.Getenv("DB_DRIVER") == "sqlite" {
		sqlDB, err := db.DB()
		if err != nil {
			return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
		}
		if err := sqlDB.Ping(); err != nil {
			return nil, fmt.Errorf("failed to ping SQLite database: %w", err)
		}
	}

	// Run database migrations automatically (optional, can be disabled via env var)
	// To disable auto-migration, set AUTO_MIGRATE=false
	// To enable auto-recovery from dirty state, set AUTO_RECOVER_DIRTY=true
	if os.Getenv("AUTO_MIGRATE") != "false" {
		logger.Infof(context.Background(), "Running database migrations...")

		autoRecover := os.Getenv("AUTO_RECOVER_DIRTY") != "false"
		migrationOpts := database.MigrationOptions{
			AutoRecoverDirty: autoRecover,
			SQLiteDBPath:     sqliteDBPath,
		}

		// Run base migrations (all versioned migrations including embeddings)
		// The embeddings migration will be conditionally executed based on skip_embedding parameter in DSN
		if err := database.RunMigrationsWithOptions(migrateDSN, migrationOpts); err != nil {
			// Log warning but don't fail startup - migrations might be handled externally
			logger.Warnf(context.Background(), "Database migration failed: %v", err)
			logger.Warnf(
				context.Background(),
				"Continuing with application startup. Please run migrations manually if needed.",
			)
		}

		// Post-migration: resolve __pending_env__ storage provider markers for historical KBs.
		// The SQL migration marks KBs that have documents but no provider with "__pending_env__";
		// we replace that with the actual STORAGE_TYPE from the environment.
		resolveStorageProviderPending(db)
	} else {
		logger.Infof(context.Background(), "Auto-migration is disabled (AUTO_MIGRATE=false)")
	}

	// Get underlying SQL DB object
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	// Configure connection pool parameters
	if os.Getenv("DB_DRIVER") == "sqlite" {
		// SQLite only supports one concurrent writer even in WAL mode.
		// Limiting to a single open connection serialises all DB access and
		// prevents "database is locked" errors from concurrent goroutines.
		sqlDB.SetMaxOpenConns(1)
	} else {
		sqlDB.SetMaxIdleConns(10)
	}
	sqlDB.SetConnMaxLifetime(time.Duration(10) * time.Minute)

	return db, nil
}

// resolveStorageProviderPending replaces the "__pending_env__" sentinel in
// knowledge_bases.storage_provider_config with the actual STORAGE_TYPE from the environment.
// This runs once after SQL migrations to bind historical KBs to their real storage provider.
func resolveStorageProviderPending(db *gorm.DB) {
	storageType := strings.TrimSpace(os.Getenv("STORAGE_TYPE"))
	if storageType == "" {
		storageType = "local"
	}
	storageType = strings.ToLower(storageType)

	result := db.Exec(
		`UPDATE knowledge_bases SET storage_provider_config = ? WHERE storage_provider_config IS NOT NULL AND storage_provider_config->>'provider' = '__pending_env__'`,
		fmt.Sprintf(`{"provider":"%s"}`, storageType),
	)
	if result.Error != nil {
		logger.Warnf(context.Background(), "Failed to resolve __pending_env__ storage providers: %v", result.Error)
	} else if result.RowsAffected > 0 {
		logger.Infof(context.Background(), "Resolved %d knowledge bases with __pending_env__ storage provider → %s", result.RowsAffected, storageType)
	}

	// Sync PostgreSQL sequences with actual MAX values to prevent duplicate key
	// errors. The old code assigned seq_id via SELECT MAX()+1 in application
	// code, which could push values past the DB sequence counter.
	syncSequences(db)

	// Reset any pending tasks left over from previous aborted runs (Lite App mode)
	resetPendingTasks(db)
}

// syncSequences ensures PostgreSQL sequences for auto-increment columns (seq_id)
// are at least as high as the current MAX value in each table. This is needed
// because older code assigned seq_id via application-level MAX()+1, which could
// advance values past the DB sequence counter and cause duplicate key errors.
func syncSequences(db *gorm.DB) {
	if db.Dialector.Name() != "postgres" {
		return
	}
	pairs := [][2]string{
		{"chunks", "chunks_seq_id_seq"},
		{"knowledge_tags", "knowledge_tags_seq_id_seq"},
	}
	for _, p := range pairs {
		table, seq := p[0], p[1]
		sql := fmt.Sprintf(
			`SELECT setval('%s', GREATEST(nextval('%s'), (SELECT COALESCE(MAX(seq_id), 0) FROM %s)))`,
			seq, seq, table,
		)
		if err := db.Exec(sql).Error; err != nil {
			logger.Warnf(context.Background(), "Failed to sync sequence %s: %v", seq, err)
		} else {
			logger.Infof(context.Background(), "Synced sequence %s with table %s", seq, table)
		}
	}
}

// resetPendingTasks resets the state of any knowledge items or sync logs stuck in processing
// due to an unexpected application restart when using in-memory queues (Lite mode).
func resetPendingTasks(db *gorm.DB) {
	if os.Getenv("REDIS_ADDR") != "" {
		return // Distributed queue (Asynq) will handle its own retries
	}

	// 1. Reset knowledge parsing tasks
	result := db.Model(&types.Knowledge{}).
		Where("parse_status IN ?", []string{types.ParseStatusPending, types.ParseStatusProcessing, types.ParseStatusDeleting}).
		Updates(map[string]interface{}{
			"parse_status":  types.ParseStatusFailed,
			"error_message": "Task interrupted due to application restart",
		})
	if result.Error != nil {
		logger.Warnf(context.Background(), "Failed to reset pending knowledge tasks: %v", result.Error)
	} else if result.RowsAffected > 0 {
		logger.Infof(context.Background(), "Reset %d stuck knowledge parsing tasks to failed state", result.RowsAffected)
	}

	// 2. Reset knowledge summary tasks
	resultSummary := db.Model(&types.Knowledge{}).
		Where("summary_status IN ?", []string{types.SummaryStatusPending, types.SummaryStatusProcessing}).
		Updates(map[string]interface{}{
			"summary_status": types.SummaryStatusFailed,
		})
	if resultSummary.Error != nil {
		logger.Warnf(context.Background(), "Failed to reset pending summary tasks: %v", resultSummary.Error)
	} else if resultSummary.RowsAffected > 0 {
		logger.Infof(context.Background(), "Reset %d stuck summary generation tasks to failed state", resultSummary.RowsAffected)
	}

	// 3. Reset data source sync tasks
	resultSync := db.Model(&types.SyncLog{}).
		Where("status IN ?", []string{types.SyncLogStatusRunning, "pending"}).
		Updates(map[string]interface{}{
			"status":        types.SyncLogStatusFailed,
			"error_message": "Sync interrupted due to application restart",
			"end_time":      time.Now(),
		})
	if resultSync.Error != nil {
		logger.Warnf(context.Background(), "Failed to reset pending data source sync tasks: %v", resultSync.Error)
	} else if resultSync.RowsAffected > 0 {
		logger.Infof(context.Background(), "Reset %d stuck data source sync tasks to failed state", resultSync.RowsAffected)
	}
}

// initFileService initializes file storage service
// Creates the appropriate file storage service based on configuration
// Supports multiple storage backends (MinIO, COS, local filesystem)
// Parameters:
//   - cfg: Application configuration
//
// Returns:
//   - Configured file service implementation
//   - Error if initialization fails
func initFileService(cfg *config.Config) (interfaces.FileService, error) {
	storageType := strings.TrimSpace(os.Getenv("STORAGE_TYPE"))
	if storageType == "" {
		storageType = "local"
	}
	switch storageType {
	case "minio":
		if os.Getenv("MINIO_ENDPOINT") == "" ||
			os.Getenv("MINIO_ACCESS_KEY_ID") == "" ||
			os.Getenv("MINIO_SECRET_ACCESS_KEY") == "" ||
			os.Getenv("MINIO_BUCKET_NAME") == "" {
			return nil, fmt.Errorf("missing MinIO configuration")
		}
		return file.NewMinioFileService(
			os.Getenv("MINIO_ENDPOINT"),
			os.Getenv("MINIO_ACCESS_KEY_ID"),
			os.Getenv("MINIO_SECRET_ACCESS_KEY"),
			os.Getenv("MINIO_BUCKET_NAME"),
			strings.EqualFold(os.Getenv("MINIO_USE_SSL"), "true"),
		)
	case "cos":
		if os.Getenv("COS_BUCKET_NAME") == "" ||
			os.Getenv("COS_REGION") == "" ||
			os.Getenv("COS_SECRET_ID") == "" ||
			os.Getenv("COS_SECRET_KEY") == "" ||
			os.Getenv("COS_PATH_PREFIX") == "" {
			return nil, fmt.Errorf("missing COS configuration")
		}
		return file.NewCosFileServiceWithTempBucket(
			os.Getenv("COS_BUCKET_NAME"),
			os.Getenv("COS_REGION"),
			os.Getenv("COS_SECRET_ID"),
			os.Getenv("COS_SECRET_KEY"),
			os.Getenv("COS_PATH_PREFIX"),
			os.Getenv("COS_TEMP_BUCKET_NAME"),
			os.Getenv("COS_TEMP_REGION"),
		)
	case "tos":
		if os.Getenv("TOS_ENDPOINT") == "" ||
			os.Getenv("TOS_REGION") == "" ||
			os.Getenv("TOS_ACCESS_KEY") == "" ||
			os.Getenv("TOS_SECRET_KEY") == "" ||
			os.Getenv("TOS_BUCKET_NAME") == "" {
			return nil, fmt.Errorf("missing TOS configuration")
		}
		return file.NewTosFileServiceWithTempBucket(
			os.Getenv("TOS_ENDPOINT"),
			os.Getenv("TOS_REGION"),
			os.Getenv("TOS_ACCESS_KEY"),
			os.Getenv("TOS_SECRET_KEY"),
			os.Getenv("TOS_BUCKET_NAME"),
			os.Getenv("TOS_PATH_PREFIX"),
			os.Getenv("TOS_TEMP_BUCKET_NAME"), // 可选：临时桶名称（桶需配置生命周期规则自动过期）
			os.Getenv("TOS_TEMP_REGION"),      // 可选：临时桶 region，默认与主桶相同
		)
	case "s3":
		if os.Getenv("S3_ENDPOINT") == "" ||
			os.Getenv("S3_REGION") == "" ||
			os.Getenv("S3_ACCESS_KEY") == "" ||
			os.Getenv("S3_SECRET_KEY") == "" ||
			os.Getenv("S3_BUCKET_NAME") == "" {
			return nil, fmt.Errorf("missing S3 configuration")
		}
		pathPrefix := os.Getenv("S3_PATH_PREFIX")
		if pathPrefix == "" {
			pathPrefix = "weknora/"
		}
		return file.NewS3FileService(
			os.Getenv("S3_ENDPOINT"),
			os.Getenv("S3_ACCESS_KEY"),
			os.Getenv("S3_SECRET_KEY"),
			os.Getenv("S3_BUCKET_NAME"),
			os.Getenv("S3_REGION"),
			pathPrefix,
		)
	case "oss":
		if os.Getenv("OSS_ENDPOINT") == "" ||
			os.Getenv("OSS_REGION") == "" ||
			os.Getenv("OSS_ACCESS_KEY") == "" ||
			os.Getenv("OSS_SECRET_KEY") == "" ||
			os.Getenv("OSS_BUCKET_NAME") == "" {
			return nil, fmt.Errorf("missing OSS configuration")
		}
		pathPrefix := os.Getenv("OSS_PATH_PREFIX")
		if pathPrefix == "" {
			pathPrefix = "weknora/"
		}
		return file.NewOssFileServiceWithTempBucket(
			os.Getenv("OSS_ENDPOINT"),
			os.Getenv("OSS_REGION"),
			os.Getenv("OSS_ACCESS_KEY"),
			os.Getenv("OSS_SECRET_KEY"),
			os.Getenv("OSS_BUCKET_NAME"),
			pathPrefix,
			os.Getenv("OSS_TEMP_BUCKET_NAME"),
			os.Getenv("OSS_TEMP_REGION"),
		)
	case "local":
		baseDir := os.Getenv("LOCAL_STORAGE_BASE_DIR")
		if baseDir == "" {
			baseDir = "/data/files"
		}
		return file.NewLocalFileService(baseDir), nil
	case "dummy":
		return file.NewDummyFileService(), nil
	default:
		return nil, fmt.Errorf("unsupported storage type: %s", storageType)
	}
}

// initRetrieveEngineRegistry initializes the retrieval engine registry
// Sets up and configures various search engine backends based on configuration
// Supports multiple retrieval engines (PostgreSQL, ElasticsearchV7, ElasticsearchV8)
// Parameters:
//   - db: Database connection
//   - cfg: Application configuration
//
// Returns:
//   - Configured retrieval engine registry
//   - Error if initialization fails
func initRetrieveEngineRegistry(db *gorm.DB, cfg *config.Config) (interfaces.RetrieveEngineRegistry, error) {
	registry := retriever.NewRetrieveEngineRegistry()
	retrieveDriver := strings.Split(os.Getenv("RETRIEVE_DRIVER"), ",")
	log := logger.GetLogger(context.Background())

	if slices.Contains(retrieveDriver, "postgres") {
		postgresRepo := postgresRepo.NewPostgresRetrieveEngineRepository(db)
		if err := registry.Register(
			retriever.NewKVHybridRetrieveEngine(postgresRepo, types.PostgresRetrieverEngineType),
		); err != nil {
			log.Errorf("Register postgres retrieve engine failed: %v", err)
		} else {
			log.Infof("Register postgres retrieve engine success")
		}
	}
	if slices.Contains(retrieveDriver, "sqlite") {
		sqliteRepo := sqliteRetrieverRepo.NewSQLiteRetrieveEngineRepository(db)
		if err := registry.Register(
			retriever.NewKVHybridRetrieveEngine(sqliteRepo, types.SQLiteRetrieverEngineType),
		); err != nil {
			log.Errorf("Register sqlite retrieve engine failed: %v", err)
		} else {
			log.Infof("Register sqlite retrieve engine success")
		}
	}
	if slices.Contains(retrieveDriver, "elasticsearch_v8") {
		client, err := elasticsearch.NewTypedClient(elasticsearch.Config{
			Addresses: []string{os.Getenv("ELASTICSEARCH_ADDR")},
			Username:  os.Getenv("ELASTICSEARCH_USERNAME"),
			Password:  os.Getenv("ELASTICSEARCH_PASSWORD"),
		})
		if err != nil {
			log.Errorf("Create elasticsearch_v8 client failed: %v", err)
		} else {
			elasticsearchRepo := elasticsearchRepoV8.NewElasticsearchEngineRepository(client, cfg, nil)
			if err := registry.Register(
				retriever.NewKVHybridRetrieveEngine(
					elasticsearchRepo, types.ElasticsearchRetrieverEngineType,
				),
			); err != nil {
				log.Errorf("Register elasticsearch_v8 retrieve engine failed: %v", err)
			} else {
				log.Infof("Register elasticsearch_v8 retrieve engine success")
			}
		}
	}

	if slices.Contains(retrieveDriver, "elasticsearch_v7") {
		client, err := esv7.NewClient(esv7.Config{
			Addresses: []string{os.Getenv("ELASTICSEARCH_ADDR")},
			Username:  os.Getenv("ELASTICSEARCH_USERNAME"),
			Password:  os.Getenv("ELASTICSEARCH_PASSWORD"),
		})
		if err != nil {
			log.Errorf("Create elasticsearch_v7 client failed: %v", err)
		} else {
			elasticsearchRepo := elasticsearchRepoV7.NewElasticsearchEngineRepository(client, cfg, nil)
			if err := registry.Register(
				retriever.NewKVHybridRetrieveEngine(
					elasticsearchRepo, types.ElasticsearchRetrieverEngineType,
				),
			); err != nil {
				log.Errorf("Register elasticsearch_v7 retrieve engine failed: %v", err)
			} else {
				log.Infof("Register elasticsearch_v7 retrieve engine success")
			}
		}
	}

	if slices.Contains(retrieveDriver, "qdrant") {
		qdrantHost := os.Getenv("QDRANT_HOST")
		if qdrantHost == "" {
			qdrantHost = "localhost"
		}

		qdrantPort := 6334 // Default port
		if portStr := os.Getenv("QDRANT_PORT"); portStr != "" {
			if port, err := strconv.Atoi(portStr); err == nil {
				qdrantPort = port
			}
		}

		// API key for authentication (optional)
		qdrantAPIKey := os.Getenv("QDRANT_API_KEY")

		// TLS configuration (optional, defaults to false)
		// Enable TLS unless explicitly set to "false" or "0" (case insensitive)
		qdrantUseTLS := false
		if useTLSStr := os.Getenv("QDRANT_USE_TLS"); useTLSStr != "" {
			useTLSLower := strings.ToLower(strings.TrimSpace(useTLSStr))
			qdrantUseTLS = useTLSLower != "false" && useTLSLower != "0"
		}

		log.Infof("Connecting to Qdrant at %s:%d (TLS: %v)", qdrantHost, qdrantPort, qdrantUseTLS)

		client, err := qdrant.NewClient(&qdrant.Config{
			Host:   qdrantHost,
			Port:   qdrantPort,
			APIKey: qdrantAPIKey,
			UseTLS: qdrantUseTLS,
		})
		if err != nil {
			log.Errorf("Create qdrant client failed: %v", err)
		} else {
			qdrantRepository := qdrantRepo.NewQdrantRetrieveEngineRepository(client, nil)
			if err := registry.Register(
				retriever.NewKVHybridRetrieveEngine(
					qdrantRepository, types.QdrantRetrieverEngineType,
				),
			); err != nil {
				log.Errorf("Register qdrant retrieve engine failed: %v", err)
			} else {
				log.Infof("Register qdrant retrieve engine success")
			}
		}
	}
	if slices.Contains(retrieveDriver, "weaviate") {
		weaviateHost := os.Getenv("WEAVIATE_HOST")
		if weaviateHost == "" {
			// Docker compose default (service name inside network)
			weaviateHost = "weaviate:8080"
		}
		weaviateGrpcAddress := os.Getenv("WEAVIATE_GRPC_ADDRESS")
		if weaviateGrpcAddress == "" {
			weaviateGrpcAddress = "weaviate:50051"
		}
		weaviateScheme := os.Getenv("WEAVIATE_SCHEME")
		if weaviateScheme == "" {
			weaviateScheme = "http"
		}
		var authConfig auth.Config
		if strings.EqualFold(strings.TrimSpace(os.Getenv("WEAVIATE_AUTH_ENABLED")), "true") {
			if apiKey := strings.TrimSpace(os.Getenv("WEAVIATE_API_KEY")); apiKey != "" {
				authConfig = auth.ApiKey{Value: apiKey}
			}
		}
		weaviateClient, err := weaviate.NewClient(weaviate.Config{
			Host: weaviateHost,
			GrpcConfig: &wgrpc.Config{
				Host: weaviateGrpcAddress,
			},
			Scheme:     weaviateScheme,
			AuthConfig: authConfig,
		})
		if err != nil {
			log.Errorf("Create weaviate client failed: %v", err)
		} else {
			weaviateRepository := weaviateRepo.NewWeaviateRetrieveEngineRepository(weaviateClient, nil)
			if err := registry.Register(
				retriever.NewKVHybridRetrieveEngine(
					weaviateRepository, types.WeaviateRetrieverEngineType,
				),
			); err != nil {
				log.Errorf("Register weaviate retrieve engine failed: %v", err)
			} else {
				log.Infof("Register weaviate retrieve engine success")
			}
		}
	}
	if slices.Contains(retrieveDriver, "milvus") {
		milvusCfg := milvusclient.ClientConfig{
			DialOptions: []grpc.DialOption{grpc.WithTimeout(5 * time.Second)},
		}
		milvusAddress := os.Getenv("MILVUS_ADDRESS")
		if milvusAddress == "" {
			milvusAddress = "localhost:19530"
		}
		milvusCfg.Address = milvusAddress
		milvusUsername := os.Getenv("MILVUS_USERNAME")
		if milvusUsername != "" {
			milvusCfg.Username = milvusUsername
		}
		milvusPassword := os.Getenv("MILVUS_PASSWORD")
		if milvusPassword != "" {
			milvusCfg.Password = milvusPassword
		}
		milvusDBName := os.Getenv("MILVUS_DB_NAME")
		if milvusDBName != "" {
			milvusCfg.DBName = milvusDBName
		}
		milvusCli, err := milvusclient.New(context.Background(), &milvusCfg)
		if err != nil {
			log.Errorf("Create milvus client failed: %v", err)
		} else {
			milvusRepository := milvusRepo.NewMilvusRetrieveEngineRepository(milvusCli, nil)
			if err := registry.Register(
				retriever.NewKVHybridRetrieveEngine(
					milvusRepository, types.MilvusRetrieverEngineType,
				),
			); err != nil {
				log.Errorf("Register milvus retrieve engine failed: %v", err)
			} else {
				log.Infof("Register milvus retrieve engine success")
			}
		}
	}
	// ─── DB store registration (byStoreID) ───
	if storeReg, ok := registry.(*retriever.RetrieveEngineRegistry); ok {
		loadDBStoresIntoRegistry(storeReg, db, cfg)
	}

	return registry, nil
}

// loadDBStoresIntoRegistry loads VectorStore records from DB and registers them
// in the registry's byStoreID map. Failures are logged and skipped (non-fatal).
func loadDBStoresIntoRegistry(storeRegistry interfaces.StoreRegistry, db *gorm.DB, cfg *config.Config) {
	ctx := context.Background()
	log := logger.GetLogger(ctx)

	var stores []types.VectorStore
	// GORM soft delete automatically adds "deleted_at IS NULL" condition
	if err := db.Find(&stores).Error; err != nil {
		log.Warnf("Failed to load vector stores from DB: %v", err)
		return
	}

	if len(stores) == 0 {
		return
	}

	log.Infof("Loading %d vector store(s) from database", len(stores))
	for _, store := range stores {
		svc, err := createEngineServiceFromStore(ctx, store, db, cfg)
		if err != nil {
			log.Errorf("Failed to create engine for store %s (%s): %v", store.ID, store.Name, err)
			continue
		}
		storeRegistry.RegisterWithStoreID(store.ID, svc)
		log.Infof("Registered DB vector store: id=%s, name=%s, engine=%s", store.ID, store.Name, store.EngineType)
	}
}

// initAntsPool initializes the goroutine pool
// Creates a managed goroutine pool for concurrent task execution
// Parameters:
//   - cfg: Application configuration
//
// Returns:
//   - Configured goroutine pool
//   - Error if initialization fails
func initAntsPool(cfg *config.Config) (*ants.Pool, error) {
	// Default to 5 if not specified in config
	poolSize := os.Getenv("CONCURRENCY_POOL_SIZE")
	if poolSize == "" {
		poolSize = "5"
	}
	poolSizeInt, err := strconv.Atoi(poolSize)
	if err != nil {
		return nil, err
	}
	// Set up the pool with pre-allocation for better performance
	return ants.NewPool(poolSizeInt, ants.WithPreAlloc(true))
}

// registerPoolCleanup registers the goroutine pool for cleanup
// Ensures proper cleanup of the goroutine pool when application shuts down
// Parameters:
//   - pool: Goroutine pool
//   - cleaner: Resource cleaner
func registerPoolCleanup(pool *ants.Pool, cleaner interfaces.ResourceCleaner) {
	cleaner.RegisterWithName("AntsPool", func() error {
		pool.Release()
		return nil
	})
}

// registerTracerCleanup registers the tracer for cleanup
// Ensures proper cleanup of the tracer when application shuts down
// Parameters:
//   - tracer: Tracer instance
//   - cleaner: Resource cleaner
func registerTracerCleanup(tracer *tracing.Tracer, cleaner interfaces.ResourceCleaner) {
	// Register the cleanup function - actual context will be provided during cleanup
	cleaner.RegisterWithName("Tracer", func() error {
		// Create context for cleanup with longer timeout for tracer shutdown
		return tracer.Cleanup(context.Background())
	})
}

// registerLangfuseCleanup ensures buffered Langfuse events are flushed on
// shutdown. A 5-second timeout matches other external-service cleanups and
// balances data durability against a slow remote endpoint holding up exit.
func registerLangfuseCleanup(mgr *langfuse.Manager, cleaner interfaces.ResourceCleaner) {
	if mgr == nil {
		return
	}
	cleaner.RegisterWithName("Langfuse", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return mgr.Shutdown(ctx)
	})
}

// initDocReaderClient initializes the DocumentReader client (lightweight API).
func initDocReaderClient(cfg *config.Config) (interfaces.DocumentReader, error) {
	addr := strings.TrimSpace(os.Getenv("DOCREADER_ADDR"))
	transport := strings.TrimSpace(os.Getenv("DOCREADER_TRANSPORT"))
	if transport == "" {
		transport = "grpc"
	}
	if addr == "" {
		logger.Infof(context.Background(), "[DocConverter] No DOCREADER_ADDR configured, starting disconnected")
	}
	transport = strings.ToLower(transport)
	switch transport {
	case "http", "https":
		if addr != "" && !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
			addr = "http://" + addr
		}
		return docparser.NewHTTPDocumentReader(addr)
	default:
		return docparser.NewGRPCDocumentReader(addr)
	}
}

// initOllamaService initializes the Ollama service client
// Creates a client for interacting with Ollama API for model inference
// Parameters:
//   - None
//
// Returns:
//   - Configured Ollama service client
//   - Error if initialization fails
func initOllamaService() (*ollama.OllamaService, error) {
	// Get Ollama service from existing factory function
	return ollama.GetOllamaService()
}

func initNeo4jClient() (neo4j.Driver, error) {
	ctx := context.Background()
	if strings.ToLower(os.Getenv("NEO4J_ENABLE")) != "true" {
		logger.Debugf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil, nil
	}
	uri := os.Getenv("NEO4J_URI")
	username := os.Getenv("NEO4J_USERNAME")
	password := os.Getenv("NEO4J_PASSWORD")

	// Retry configuration
	maxRetries := 30                 // Max retry attempts
	retryInterval := 2 * time.Second // Wait between retries

	var driver neo4j.Driver
	var err error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		driver, err = neo4j.NewDriver(uri, neo4j.BasicAuth(username, password, ""))
		if err != nil {
			logger.Warnf(ctx, "Failed to create Neo4j driver (attempt %d/%d): %v", attempt, maxRetries, err)
			time.Sleep(retryInterval)
			continue
		}

		err = driver.VerifyAuthentication(ctx, nil)
		if err == nil {
			if attempt > 1 {
				logger.Infof(ctx, "Successfully connected to Neo4j after %d attempts", attempt)
			}
			return driver, nil
		}

		logger.Warnf(ctx, "Failed to verify Neo4j authentication (attempt %d/%d): %v", attempt, maxRetries, err)
		driver.Close(ctx)
		time.Sleep(retryInterval)
	}

	return nil, fmt.Errorf("failed to connect to Neo4j after %d attempts: %w", maxRetries, err)
}

func NewDuckDB() (*sql.DB, error) {
	sqlDB, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("failed to open duckdb: %w", err)
	}

	// Try to install and load required extensions.
	//   - spatial: used for st_read_meta() to enumerate layer (sheet) names from .xlsx/.xls
	//   - excel:   used for read_xlsx() which gives proper type inference per sheet
	bgCtx := context.Background()
	for _, ext := range []string{"spatial", "excel"} {
		if _, err := sqlDB.ExecContext(bgCtx, fmt.Sprintf("INSTALL %s;", ext)); err != nil {
			logger.Warnf(bgCtx, "[DuckDB] Failed to install %s extension: %v", ext, err)
		}
		if _, err := sqlDB.ExecContext(bgCtx, fmt.Sprintf("LOAD %s;", ext)); err != nil {
			logger.Warnf(bgCtx, "[DuckDB] Failed to load %s extension: %v", ext, err)
		}
	}

	return sqlDB, nil
}

// registerWebSearchProviders registers all web search provider types to the registry.
// Each provider type is registered with its factory function that accepts parameters.
// Provider instances are created on-demand when tenants configure them.
func registerWebSearchProviders(registry *infra_web_search.Registry) {
	registry.Register("duckduckgo", infra_web_search.NewDuckDuckGoProvider)
	registry.Register("google", infra_web_search.NewGoogleProvider)
	registry.Register("bing", infra_web_search.NewBingProvider)
	registry.Register("tavily", infra_web_search.NewTavilyProvider)
	registry.Register("ollama", infra_web_search.NewOllamaProvider)
	registry.Register("baidu", infra_web_search.NewBaiduProvider)
}

// registerIMAdapterFactories registers adapter factories for each IM platform
// and loads enabled channels from the database.
func registerIMAdapterFactories(imService *imPkg.Service) {
	ctx := context.Background()

	// Register WeCom adapter factory
	imService.RegisterAdapterFactory("wecom", func(factoryCtx context.Context, channel *imPkg.IMChannel, msgHandler func(context.Context, *imPkg.IncomingMessage) error) (imPkg.Adapter, context.CancelFunc, error) {
		creds, err := parseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse wecom credentials: %w", err)
		}

		mode := channel.Mode
		if mode == "" {
			mode = "websocket"
		}

		switch mode {
		case "webhook":
			corpAgentID := 0
			if v, ok := creds["corp_agent_id"]; ok {
				switch val := v.(type) {
				case float64:
					corpAgentID = int(val)
				case int:
					corpAgentID = val
				}
			}
			adapter, err := wecom.NewWebhookAdapter(
				getString(creds, "corp_id"),
				getString(creds, "agent_secret"),
				getString(creds, "token"),
				getString(creds, "encoding_aes_key"),
				corpAgentID,
				getString(creds, "api_base_url"),
			)
			if err != nil {
				return nil, nil, err
			}
			return adapter, nil, nil

		case "websocket":
			client, err := wecom.NewLongConnClient(
				getString(creds, "bot_id"),
				getString(creds, "bot_secret"),
				getString(creds, "ws_endpoint"),
				getString(creds, "bot_name"),
				msgHandler,
			)
			if err != nil {
				return nil, nil, err
			}

			wsCtx, wsCancel := context.WithCancel(context.Background())
			go func() {
				if err := client.Start(wsCtx); err != nil && wsCtx.Err() == nil {
					logger.Errorf(context.Background(), "[IM] WeCom long connection stopped for channel %s: %v", channel.ID, err)
				}
			}()

			adapter := wecom.NewWSAdapter(client)
			return adapter, wsCancel, nil

		default:
			return nil, nil, fmt.Errorf("unknown WeCom mode: %s", mode)
		}
	})

	// Register Feishu adapter factory
	imService.RegisterAdapterFactory("feishu", func(factoryCtx context.Context, channel *imPkg.IMChannel, msgHandler func(context.Context, *imPkg.IncomingMessage) error) (imPkg.Adapter, context.CancelFunc, error) {
		creds, err := parseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse feishu credentials: %w", err)
		}

		appID := getString(creds, "app_id")
		appSecret := getString(creds, "app_secret")
		verificationToken := getString(creds, "verification_token")
		encryptKey := getString(creds, "encrypt_key")

		// Always create the HTTP adapter (needed for SendReply in both modes)
		adapter := feishu.NewAdapter(appID, appSecret, verificationToken, encryptKey)

		mode := channel.Mode
		if mode == "" {
			mode = "websocket"
		}

		switch mode {
		case "webhook":
			return adapter, nil, nil

		case "websocket":
			client := feishu.NewLongConnClient(appID, appSecret, msgHandler)

			wsCtx, wsCancel := context.WithCancel(context.Background())
			go func() {
				if err := client.Start(wsCtx); err != nil && wsCtx.Err() == nil {
					logger.Errorf(context.Background(), "[IM] Feishu long connection stopped for channel %s: %v", channel.ID, err)
				}
			}()

			return adapter, wsCancel, nil

		default:
			return nil, nil, fmt.Errorf("unknown Feishu mode: %s", mode)
		}
	})

	// Register Slack adapter factory
	imService.RegisterAdapterFactory("slack", func(factoryCtx context.Context, channel *imPkg.IMChannel, msgHandler func(context.Context, *imPkg.IncomingMessage) error) (imPkg.Adapter, context.CancelFunc, error) {
		creds, err := parseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse slack credentials: %w", err)
		}

		mode := channel.Mode
		if mode == "" {
			mode = "websocket"
		}

		switch mode {
		case "webhook":
			api := slackpkg.New(getString(creds, "bot_token"))
			adapter := slack.NewWebhookAdapter(api, getString(creds, "signing_secret"))
			return adapter, func() {}, nil

		case "websocket":
			client := slack.NewLongConnClient(
				getString(creds, "app_token"),
				getString(creds, "bot_token"),
				msgHandler,
			)

			adapter := slack.NewAdapter(client, client.GetAPI())

			wsCtx, wsCancel := context.WithCancel(context.Background())
			go func() {
				if err := client.Start(wsCtx); err != nil && wsCtx.Err() == nil {
					logger.Errorf(context.Background(), "[IM] Slack long connection stopped for channel %s: %v", channel.ID, err)
				}
			}()

			return adapter, wsCancel, nil

		default:
			return nil, nil, fmt.Errorf("unsupported slack mode: %s", mode)
		}
	})

	// Register Telegram adapter factory
	imService.RegisterAdapterFactory("telegram", func(factoryCtx context.Context, channel *imPkg.IMChannel, msgHandler func(context.Context, *imPkg.IncomingMessage) error) (imPkg.Adapter, context.CancelFunc, error) {
		creds, err := parseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse telegram credentials: %w", err)
		}

		botToken := getString(creds, "bot_token")

		mode := channel.Mode
		if mode == "" {
			mode = "websocket"
		}

		switch mode {
		case "webhook":
			secretToken := getString(creds, "secret_token")
			adapter := telegram.NewWebhookAdapter(botToken, secretToken)
			return adapter, nil, nil

		case "websocket":
			client := telegram.NewLongConnClient(botToken, msgHandler)

			wsCtx, wsCancel := context.WithCancel(context.Background())
			go func() {
				if err := client.Start(wsCtx); err != nil && wsCtx.Err() == nil {
					logger.Errorf(context.Background(), "[IM] Telegram long polling stopped for channel %s: %v", channel.ID, err)
				}
			}()

			adapter := telegram.NewAdapter(client, botToken)
			return adapter, wsCancel, nil

		default:
			return nil, nil, fmt.Errorf("unsupported telegram mode: %s", mode)
		}
	})

	// Register DingTalk adapter factory
	imService.RegisterAdapterFactory("dingtalk", func(factoryCtx context.Context, channel *imPkg.IMChannel, msgHandler func(context.Context, *imPkg.IncomingMessage) error) (imPkg.Adapter, context.CancelFunc, error) {
		creds, err := parseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse dingtalk credentials: %w", err)
		}

		clientID := getString(creds, "client_id")
		clientSecret := getString(creds, "client_secret")
		cardTemplateID := getString(creds, "card_template_id")

		mode := channel.Mode
		if mode == "" {
			mode = "websocket"
		}

		switch mode {
		case "webhook":
			adapter := dingtalk.NewWebhookAdapter(clientID, clientSecret, cardTemplateID)
			return adapter, nil, nil

		case "websocket":
			client := dingtalk.NewLongConnClient(clientID, clientSecret, msgHandler)

			wsCtx, wsCancel := context.WithCancel(context.Background())
			go func() {
				if err := client.Start(wsCtx); err != nil && wsCtx.Err() == nil {
					logger.Errorf(context.Background(), "[IM] DingTalk stream stopped for channel %s: %v", channel.ID, err)
				}
			}()

			adapter := dingtalk.NewAdapter(client, clientID, clientSecret, cardTemplateID)
			return adapter, wsCancel, nil

		default:
			return nil, nil, fmt.Errorf("unsupported dingtalk mode: %s", mode)
		}
	})

	// Register Mattermost adapter factory (outgoing webhook + REST API).
	imService.RegisterAdapterFactory("mattermost", func(factoryCtx context.Context, channel *imPkg.IMChannel, msgHandler func(context.Context, *imPkg.IncomingMessage) error) (imPkg.Adapter, context.CancelFunc, error) {
		creds, err := parseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse mattermost credentials: %w", err)
		}

		mode := channel.Mode
		if mode == "" {
			mode = "webhook"
		}
		if mode != "webhook" {
			return nil, nil, fmt.Errorf("unsupported mattermost mode: %s (only webhook is supported)", mode)
		}

		siteURL := getString(creds, "site_url")
		botToken := getString(creds, "bot_token")
		outgoingToken := getString(creds, "outgoing_token")
		botUserID := getString(creds, "bot_user_id")

		if outgoingToken == "" {
			return nil, nil, fmt.Errorf("mattermost outgoing_token is required")
		}

		client, err := mattermost.NewClient(siteURL, botToken)
		if err != nil {
			return nil, nil, err
		}

		postReplyToMain := credentialBool(creds, "post_to_main")
		adapter := mattermost.NewAdapter(client, outgoingToken, botUserID, postReplyToMain)
		return adapter, func() {}, nil
	})
	// Register WeChat adapter factory
	imService.RegisterAdapterFactory("wechat", func(factoryCtx context.Context, channel *imPkg.IMChannel, msgHandler func(context.Context, *imPkg.IncomingMessage) error) (imPkg.Adapter, context.CancelFunc, error) {
		creds, err := parseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse wechat credentials: %w", err)
		}

		botToken := getString(creds, "bot_token")
		ilinkBotID := getString(creds, "ilink_bot_id")

		if botToken == "" || ilinkBotID == "" {
			return nil, nil, fmt.Errorf("wechat credentials require bot_token and ilink_bot_id")
		}

		adapter := wechat.NewAdapter(botToken, ilinkBotID)
		client := wechat.NewLongPollClient(botToken, ilinkBotID, msgHandler)

		pollCtx, pollCancel := context.WithCancel(context.Background())
		go func() {
			if err := client.Start(pollCtx); err != nil && pollCtx.Err() == nil {
				logger.Errorf(context.Background(), "[IM] WeChat long-poll stopped for channel %s: %v", channel.ID, err)
			}
		}()

		return adapter, pollCancel, nil
	})

	// Load and start all enabled channels from database
	if err := imService.LoadAndStartChannels(); err != nil {
		logger.Warnf(ctx, "[IM] Failed to load channels from database: %v", err)
	}
}

// parseCredentials parses the JSONB credentials field into a map.
func parseCredentials(data []byte) (map[string]interface{}, error) {
	if len(data) == 0 {
		return map[string]interface{}{}, nil
	}
	var creds map[string]interface{}
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

// getString safely extracts a string value from a credentials map.
func getString(creds map[string]interface{}, key string) string {
	if v, ok := creds[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// credentialBool reads a boolean from JSON credentials (bool, string "true"/"1", or non-zero number).
func credentialBool(creds map[string]interface{}, key string) bool {
	v, ok := creds[key]
	if !ok {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		s := strings.TrimSpace(strings.ToLower(x))
		return s == "true" || s == "1" || s == "yes"
	case float64:
		return x != 0
	case int:
		return x != 0
	default:
		return false
	}
}

// initConnectorRegistry creates and populates the connector registry with all available connectors.
func initConnectorRegistry() *datasource.ConnectorRegistry {
	registry := datasource.NewConnectorRegistry()

	// Register Feishu connector
	_ = registry.Register(feishuConnector.NewConnector())

	// Register Notion connector
	_ = registry.Register(notionConnector.NewConnector())

	// Register Yuque connector
	_ = registry.Register(yuqueConnector.NewConnector())

	// Future connectors will be registered here:
	// _ = registry.Register(confluenceConnector.NewConnector())
	// _ = registry.Register(githubConnector.NewConnector())

	return registry
}

// startDataSourceScheduler starts the data source cron scheduler and registers cleanup.
func startDataSourceScheduler(scheduler *datasource.Scheduler, cleaner interfaces.ResourceCleaner) {
	if err := scheduler.Start(context.Background()); err != nil {
		logger.Warnf(context.Background(), "[Container] data source scheduler start failed: %v", err)
	}

	cleaner.RegisterWithName("DataSourceScheduler", func() error {
		scheduler.Stop()
		return nil
	})
}
