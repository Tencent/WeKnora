// Package container implements dependency injection container setup
// Provides centralized configuration for services, repositories, and handlers
// This package is responsible for wiring up all dependencies and ensuring proper lifecycle management
package container

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	esv7 "github.com/elastic/go-elasticsearch/v7"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/panjf2000/ants/v2"
	"go.uber.org/dig"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	elasticsearchRepoV7 "github.com/Tencent/WeKnora/internal/application/repository/retriever/elasticsearch/v7"
	elasticsearchRepoV8 "github.com/Tencent/WeKnora/internal/application/repository/retriever/elasticsearch/v8"
	neo4jRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/neo4j"
	postgresRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/postgres"
	"github.com/Tencent/WeKnora/internal/application/service"
	chatpipline "github.com/Tencent/WeKnora/internal/application/service/chat_pipline"
	"github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/router"
	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/tracing"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/services/docreader/src/client"
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
	// Register resource cleaner for proper cleanup of resources
	must(container.Provide(NewResourceCleaner, dig.As(new(interfaces.ResourceCleaner))))

	// Core infrastructure configuration
	must(container.Provide(config.LoadConfig))
	must(container.Provide(initTracer))
	must(container.Provide(initDatabase))
	must(container.Provide(initFileService))
	must(container.Provide(initAntsPool))

	// Register goroutine pool cleanup handler
	must(container.Invoke(registerPoolCleanup))

	// Initialize retrieval engine registry for search capabilities
	must(container.Provide(initRetrieveEngineRegistry))

	// External service clients
	must(container.Provide(initDocReaderClient))
	must(container.Provide(initOllamaService))
	must(container.Provide(initNeo4jClient))
	must(container.Provide(stream.NewStreamManager))

	// Data repositories layer
	must(container.Provide(repository.NewTenantRepository))
	must(container.Provide(repository.NewKnowledgeBaseRepository))
	must(container.Provide(repository.NewKnowledgeRepository))
	must(container.Provide(repository.NewChunkRepository))
	must(container.Provide(repository.NewSessionRepository))
	must(container.Provide(repository.NewMessageRepository))
	must(container.Provide(repository.NewModelRepository))
	must(container.Provide(repository.NewUserRepository))
	must(container.Provide(repository.NewAuthTokenRepository))
	must(container.Provide(neo4jRepo.NewNeo4jRepository))

	// Business service layer
	must(container.Provide(service.NewTenantService))
	must(container.Provide(service.NewKnowledgeBaseService))
	must(container.Provide(service.NewKnowledgeService))
	must(container.Provide(service.NewSessionService))
	must(container.Provide(service.NewMessageService))
	must(container.Provide(service.NewChunkService))
	must(container.Provide(embedding.NewBatchEmbedder))
	must(container.Provide(service.NewModelService))
	must(container.Provide(service.NewDatasetService))
	must(container.Provide(service.NewEvaluationService))
	must(container.Provide(service.NewUserService))
	must(container.Provide(service.NewChunkExtractService))

	// Chat pipeline components for processing chat requests
	must(container.Provide(chatpipline.NewEventManager))
	must(container.Invoke(chatpipline.NewPluginTracing))
	must(container.Invoke(chatpipline.NewPluginSearch))
	must(container.Invoke(chatpipline.NewPluginRerank))
	must(container.Invoke(chatpipline.NewPluginMerge))
	must(container.Invoke(chatpipline.NewPluginIntoChatMessage))
	must(container.Invoke(chatpipline.NewPluginChatCompletion))
	must(container.Invoke(chatpipline.NewPluginChatCompletionStream))
	must(container.Invoke(chatpipline.NewPluginStreamFilter))
	must(container.Invoke(chatpipline.NewPluginFilterTopK))
	must(container.Invoke(chatpipline.NewPluginPreprocess))
	must(container.Invoke(chatpipline.NewPluginRewrite))
	must(container.Invoke(chatpipline.NewPluginExtractEntity))
	must(container.Invoke(chatpipline.NewPluginSearchEntity))

	// HTTP handlers layer
	must(container.Provide(handler.NewTenantHandler))
	must(container.Provide(handler.NewKnowledgeBaseHandler))
	must(container.Provide(handler.NewKnowledgeHandler))
	must(container.Provide(handler.NewChunkHandler))
	must(container.Provide(handler.NewSessionHandler))
	must(container.Provide(handler.NewMessageHandler))
	must(container.Provide(handler.NewModelHandler))
	must(container.Provide(handler.NewEvaluationHandler))
	must(container.Provide(handler.NewInitializationHandler))
	must(container.Provide(handler.NewAuthHandler))
	must(container.Provide(handler.NewSystemHandler))

	// Router configuration
	must(container.Provide(router.NewRouter))
	must(container.Provide(router.NewAsyncqClient))
	must(container.Provide(router.NewAsynqServer))
	must(container.Invoke(router.RunAsynqServer))

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
	switch os.Getenv("DB_DRIVER") {
	case "postgres":
		dsn := fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
			os.Getenv("DB_HOST"),
			os.Getenv("DB_PORT"),
			os.Getenv("DB_USER"),
			os.Getenv("DB_PASSWORD"),
			os.Getenv("DB_NAME"),
			"disable",
		)
		dialector = postgres.Open(dsn)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", os.Getenv("DB_DRIVER"))
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		return nil, err
	}

	// Auto-migrate database tables
	err = db.AutoMigrate(
		&types.User{},
		&types.AuthToken{},
		&types.KnowledgeBase{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to auto-migrate database tables: %v", err)
	}

	// Get underlying SQL DB object
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	// Configure connection pool parameters
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(time.Duration(10) * time.Minute)

	return db, nil
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
	switch os.Getenv("STORAGE_TYPE") {
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
			false,
		)
	case "cos":
		if os.Getenv("COS_BUCKET_NAME") == "" ||
			os.Getenv("COS_REGION") == "" ||
			os.Getenv("COS_SECRET_ID") == "" ||
			os.Getenv("COS_SECRET_KEY") == "" ||
			os.Getenv("COS_PATH_PREFIX") == "" {
			return nil, fmt.Errorf("missing COS configuration")
		}
		return file.NewCosFileService(
			os.Getenv("COS_BUCKET_NAME"),
			os.Getenv("COS_REGION"),
			os.Getenv("COS_SECRET_ID"),
			os.Getenv("COS_SECRET_KEY"),
			os.Getenv("COS_PATH_PREFIX"),
		)
	case "local":
		return file.NewLocalFileService(os.Getenv("LOCAL_STORAGE_BASE_DIR")), nil
	case "dummy":
		return file.NewDummyFileService(), nil
	default:
		return nil, fmt.Errorf("unsupported storage type: %s", os.Getenv("STORAGE_TYPE"))
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
	if slices.Contains(retrieveDriver, "elasticsearch_v8") {
		client, err := elasticsearch.NewTypedClient(elasticsearch.Config{
			Addresses: []string{os.Getenv("ELASTICSEARCH_ADDR")},
			Username:  os.Getenv("ELASTICSEARCH_USERNAME"),
			Password:  os.Getenv("ELASTICSEARCH_PASSWORD"),
		})
		if err != nil {
			log.Errorf("Create elasticsearch_v8 client failed: %v", err)
		} else {
			elasticsearchRepo := elasticsearchRepoV8.NewElasticsearchEngineRepository(client, cfg)
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
			elasticsearchRepo := elasticsearchRepoV7.NewElasticsearchEngineRepository(client, cfg)
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
	return registry, nil
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

// initDocReaderClient initializes the document reader client
// Creates a client for interacting with the document reader service
// Parameters:
//   - cfg: Application configuration
//
// Returns:
//   - Configured document reader client
//   - Error if initialization fails
func initDocReaderClient(cfg *config.Config) (*client.Client, error) {
	// Use the DocReader URL from environment or config
	docReaderURL := os.Getenv("DOCREADER_ADDR")
	if docReaderURL == "" && cfg.DocReader != nil {
		docReaderURL = cfg.DocReader.Addr
	}
	return client.NewClient(docReaderURL)
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
	if strings.ToLower(os.Getenv("NEO4J_ENABLE")) != "true" {
		logger.Debugf(context.Background(), "NOT SUPPORT RETRIEVE GRAPH")
		return nil, nil
	}
	uri := os.Getenv("NEO4J_URI")
	username := os.Getenv("NEO4J_USERNAME")
	password := os.Getenv("NEO4J_PASSWORD")

	driver, err := neo4j.NewDriver(uri, neo4j.BasicAuth(username, password, ""))
	if err != nil {
		return nil, err
	}
	err = driver.VerifyAuthentication(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	return driver, nil
}
