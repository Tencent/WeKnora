// Package container 의존성 주입 컨테이너 설정을 구현합니다.
// 서비스, 리포지토리 및 핸들러에 대한 중앙 집중식 구성을 제공합니다.
// 이 패키지는 모든 의존성을 연결하고 적절한 수명 주기 관리를 보장합니다.
package container

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	esv7 "github.com/elastic/go-elasticsearch/v7"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/panjf2000/ants/v2"
	"github.com/qdrant/go-client/qdrant"
	"github.com/redis/go-redis/v9"
	"go.uber.org/dig"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/docreader/client"
	"github.com/Tencent/WeKnora/internal/application/repository"
	elasticsearchRepoV7 "github.com/Tencent/WeKnora/internal/application/repository/retriever/elasticsearch/v7"
	elasticsearchRepoV8 "github.com/Tencent/WeKnora/internal/application/repository/retriever/elasticsearch/v8"
	neo4jRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/neo4j"
	postgresRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/postgres"
	qdrantRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/qdrant"
	"github.com/Tencent/WeKnora/internal/application/service"
	chatpipline "github.com/Tencent/WeKnora/internal/application/service/chat_pipline"
	"github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/application/service/llmcontext"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/database"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/router"
	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/tracing"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// BuildContainer 의존성 주입 컨테이너를 구성합니다.
// 애플리케이션에 필요한 모든 구성 요소, 서비스, 리포지토리 및 핸들러를 등록합니다.
// 적절한 의존성 해결이 포함된 완전히 구성된 애플리케이션 컨테이너를 생성합니다.
// 매개변수:
//   - container: 의존성을 추가할 기본 dig 컨테이너
//
// 반환값:
//   - 모든 애플리케이션 의존성이 등록된 구성된 컨테이너
func BuildContainer(container *dig.Container) *dig.Container {
	// 리소스의 적절한 정리를 위해 리소스 정리기 등록
	must(container.Provide(NewResourceCleaner, dig.As(new(interfaces.ResourceCleaner))))

	// 핵심 인프라 구성
	must(container.Provide(config.LoadConfig))
	must(container.Provide(initTracer))
	must(container.Provide(initDatabase))
	must(container.Provide(initFileService))
	must(container.Provide(initRedisClient))
	must(container.Provide(initAntsPool))
	must(container.Provide(initContextStorage))

	// 고루틴 풀 정리 핸들러 등록
	must(container.Invoke(registerPoolCleanup))

	// 검색 기능을 위한 검색 엔진 레지스트리 초기화
	must(container.Provide(initRetrieveEngineRegistry))

	// 외부 서비스 클라이언트
	must(container.Provide(initDocReaderClient))
	must(container.Provide(initOllamaService))
	must(container.Provide(initNeo4jClient))
	must(container.Provide(stream.NewStreamManager))
	must(container.Provide(NewDuckDB))

	// 데이터 리포지토리 계층
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
	must(container.Provide(repository.NewMCPServiceRepository))
	must(container.Provide(repository.NewCustomAgentRepository))
	must(container.Provide(service.NewWebSearchStateService))

	// MCP 클라이언트 연결 관리를 위한 MCP 관리자
	must(container.Provide(mcp.NewMCPManager))

	// 비즈니스 서비스 계층
	must(container.Provide(service.NewTenantService))
	must(container.Provide(service.NewKnowledgeBaseService))
	must(container.Provide(service.NewKnowledgeService))
	must(container.Provide(service.NewChunkService))
	must(container.Provide(service.NewKnowledgeTagService))
	must(container.Provide(embedding.NewBatchEmbedder))
	must(container.Provide(service.NewModelService))
	must(container.Provide(service.NewDatasetService))
	must(container.Provide(service.NewEvaluationService))
	must(container.Provide(service.NewUserService))

	// 추출 서비스 - 이름으로 개별 추출기 등록
	must(container.Provide(service.NewChunkExtractService, dig.Name("chunkExtracter")))
	must(container.Provide(service.NewDataTableSummaryService, dig.Name("dataTableSummary")))

	must(container.Provide(service.NewMessageService))
	must(container.Provide(service.NewMCPServiceService))
	must(container.Provide(service.NewCustomAgentService))

	// 웹 검색 서비스 (AgentService에 필요)
	must(container.Provide(service.NewWebSearchService))

	// 에이전트 서비스 계층 (이벤트 버스, 웹 검색 서비스 필요)
	// AgentService 생성 시 CreateAgentEngine 메서드에 SessionService가 매개변수로 전달됨
	must(container.Provide(event.NewEventBus))
	must(container.Provide(service.NewAgentService))

	// 세션 서비스 (에이전트 서비스에 의존)
	// SessionService는 AgentService 후에 생성되며 필요할 때 AgentService.CreateAgentEngine에 자신을 전달함
	must(container.Provide(service.NewSessionService))

	must(container.Provide(router.NewAsyncqClient))
	must(container.Provide(router.NewAsynqServer))

	// 채팅 요청 처리를 위한 채팅 파이프라인 구성 요소
	must(container.Provide(chatpipline.NewEventManager))
	must(container.Invoke(chatpipline.NewPluginTracing))
	must(container.Invoke(chatpipline.NewPluginSearch))
	must(container.Invoke(chatpipline.NewPluginRerank))
	must(container.Invoke(chatpipline.NewPluginMerge))
	must(container.Invoke(chatpipline.NewPluginDataAnalysis))
	must(container.Invoke(chatpipline.NewPluginIntoChatMessage))
	must(container.Invoke(chatpipline.NewPluginChatCompletion))
	must(container.Invoke(chatpipline.NewPluginChatCompletionStream))
	must(container.Invoke(chatpipline.NewPluginStreamFilter))
	must(container.Invoke(chatpipline.NewPluginFilterTopK))
	must(container.Invoke(chatpipline.NewPluginRewrite))
	must(container.Invoke(chatpipline.NewPluginLoadHistory))
	must(container.Invoke(chatpipline.NewPluginExtractEntity))
	must(container.Invoke(chatpipline.NewPluginSearchEntity))
	must(container.Invoke(chatpipline.NewPluginSearchParallel))

	// HTTP 핸들러 계층
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
	must(container.Provide(handler.NewCustomAgentHandler))

	// 라우터 구성
	must(container.Provide(router.NewRouter))
	must(container.Invoke(router.RunAsynqServer))

	return container
}

// must는 오류 처리를 위한 헬퍼 함수입니다.
// 오류가 nil이 아니면 패닉을 발생시키며, 반드시 성공해야 하는 구성 단계에 유용합니다.
// 매개변수:
//   - err: 확인할 오류
func must(err error) {
	if err != nil {
		panic(err)
	}
}

// initTracer OpenTelemetry 추적기를 초기화합니다.
// 애플리케이션 전체의 가시성을 위해 분산 추적을 설정합니다.
// 매개변수:
//   - 없음
//
// 반환값:
//   - 구성된 추적기 인스턴스
//   - 초기화 실패 시 오류
func initTracer() (*tracing.Tracer, error) {
	return tracing.InitTracer()
}

func initRedisClient() (*redis.Client, error) {
	db, err := strconv.Atoi(os.Getenv("REDIS_DB"))
	if err != nil {
		return nil, err
	}

	client := redis.NewClient(&redis.Options{
		Addr:     os.Getenv("REDIS_ADDR"),
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       db,
	})

	// 연결 검증
	_, err = client.Ping(context.Background()).Result()
	if err != nil {
		return nil, fmt.Errorf("Redis 연결 실패: %w", err)
	}

	return client, nil
}

func initContextStorage(redisClient *redis.Client) (llmcontext.ContextStorage, error) {
	storage, err := llmcontext.NewRedisStorage(redisClient, 24*time.Hour, "context:")
	if err != nil {
		return nil, err
	}
	return storage, nil
}

// initDatabase 데이터베이스 연결을 초기화합니다.
// 환경 구성에 따라 데이터베이스 연결을 생성하고 구성합니다.
// 여러 데이터베이스 백엔드(PostgreSQL)를 지원합니다.
// 매개변수:
//   - cfg: 애플리케이션 구성
//
// 반환값:
//   - 구성된 데이터베이스 연결
//   - 연결 실패 시 오류
func initDatabase(cfg *config.Config) (*gorm.DB, error) {
	var dialector gorm.Dialector
	var migrateDSN string
	switch os.Getenv("DB_DRIVER") {
	case "postgres":
		// GORM용 DSN (키-값 형식)
		gormDSN := fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
			os.Getenv("DB_HOST"),
			os.Getenv("DB_PORT"),
			os.Getenv("DB_USER"),
			os.Getenv("DB_PASSWORD"),
			os.Getenv("DB_NAME"),
			"disable",
		)
		dialector = postgres.Open(gormDSN)

		// golang-migrate용 DSN (URL 형식)
		// !@#와 같은 특수 문자를 처리하기 위해 비밀번호 URL 인코딩
		dbPassword := os.Getenv("DB_PASSWORD")
		encodedPassword := url.QueryEscape(dbPassword)

		// RETRIEVE_DRIVER에 postgres가 포함되어 있는지 확인하여 skip_embedding 결정
		retrieveDriver := strings.Split(os.Getenv("RETRIEVE_DRIVER"), ",")
		skipEmbedding := "true"
		if slices.Contains(retrieveDriver, "postgres") {
			skipEmbedding = "false"
		}
		logger.Infof(context.Background(), "Skip embedding: %s", skipEmbedding)

		migrateDSN = fmt.Sprintf(
			"postgres://%s:%s@%s:%s/%s?sslmode=disable&options=-c%%20app.skip_embedding=%s",
			os.Getenv("DB_USER"),
			encodedPassword, // 인코딩된 비밀번호 사용
			os.Getenv("DB_HOST"),
			os.Getenv("DB_PORT"),
			os.Getenv("DB_NAME"),
			skipEmbedding,
		)

		// 디버그 로그 (비밀번호 로그 남기지 않음)
		logger.Infof(context.Background(), "DB Config: user=%s host=%s port=%s dbname=%s",
			os.Getenv("DB_USER"),
			os.Getenv("DB_HOST"),
			os.Getenv("DB_PORT"),
			os.Getenv("DB_NAME"),
		)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", os.Getenv("DB_DRIVER"))
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		return nil, err
	}

	// 자동으로 데이터베이스 마이그레이션 실행 (선택 사항, 환경 변수를 통해 비활성화 가능)
	// 자동 마이그레이션을 비활성화하려면 AUTO_MIGRATE=false로 설정
	// 더티 상태에서 자동 복구를 활성화하려면 AUTO_RECOVER_DIRTY=true로 설정
	if os.Getenv("AUTO_MIGRATE") != "false" {
		logger.Infof(context.Background(), "Running database migrations...")

		autoRecover := os.Getenv("AUTO_RECOVER_DIRTY") != "false"
		migrationOpts := database.MigrationOptions{
			AutoRecoverDirty: autoRecover,
		}

		// 기본 마이그레이션 실행 (임베딩을 포함한 모든 버전 관리 마이그레이션)
		// 임베딩 마이그레이션은 DSN의 skip_embedding 매개변수에 따라 조건부로 실행됩니다
		if err := database.RunMigrationsWithOptions(migrateDSN, migrationOpts); err != nil {
			// 경고 로그를 남기지만 시작을 실패시키지는 않음 - 마이그레이션은 외부에서 처리될 수 있음
			logger.Warnf(context.Background(), "Database migration failed: %v", err)
			logger.Warnf(
				context.Background(),
				"Continuing with application startup. Please run migrations manually if needed.",
			)
		}
	} else {
		logger.Infof(context.Background(), "Auto-migration is disabled (AUTO_MIGRATE=false)")
	}

	// 기본 SQL DB 객체 가져오기
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	// 연결 풀 매개변수 구성
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(time.Duration(10) * time.Minute)

	return db, nil
}

// initFileService 파일 저장소 서비스를 초기화합니다.
// 구성에 따라 적절한 파일 저장소 서비스를 생성합니다.
// 여러 저장소 백엔드(MinIO, COS, 로컬 파일 시스템)를 지원합니다.
// 매개변수:
//   - cfg: 애플리케이션 구성
//
// 반환값:
//   - 구성된 파일 서비스 구현체
//   - 초기화 실패 시 오류
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

// initRetrieveEngineRegistry 검색 엔진 레지스트리를 초기화합니다.
// 구성에 따라 다양한 검색 엔진 백엔드를 설정하고 구성합니다.
// 여러 검색 엔진(PostgreSQL, ElasticsearchV7, ElasticsearchV8)을 지원합니다.
// 매개변수:
//   - db: 데이터베이스 연결
//   - cfg: 애플리케이션 구성
//
// 반환값:
//   - 구성된 검색 엔진 레지스트리
//   - 초기화 실패 시 오류
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

	if slices.Contains(retrieveDriver, "qdrant") {
		qdrantHost := os.Getenv("QDRANT_HOST")
		if qdrantHost == "" {
			qdrantHost = "localhost"
		}

		qdrantPort := 6334 // 기본 포트
		if portStr := os.Getenv("QDRANT_PORT"); portStr != "" {
			if port, err := strconv.Atoi(portStr); err == nil {
				qdrantPort = port
			}
		}

		// 인증을 위한 API 키 (선택 사항)
		qdrantAPIKey := os.Getenv("QDRANT_API_KEY")

		// TLS 구성 (선택 사항, 기본값 false)
		// 명시적으로 "false" 또는 "0"으로 설정하지 않는 한 TLS 활성화 (대소문자 구분 안 함)
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
			qdrantRepository := qdrantRepo.NewQdrantRetrieveEngineRepository(client)
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
	return registry, nil
}

// initAntsPool 고루틴 풀을 초기화합니다.
// 동시 작업 실행을 위한 관리형 고루틴 풀을 생성합니다.
// 매개변수:
//   - cfg: 애플리케이션 구성
//
// 반환값:
//   - 구성된 고루틴 풀
//   - 초기화 실패 시 오류
func initAntsPool(cfg *config.Config) (*ants.Pool, error) {
	// 구성에 지정되지 않은 경우 기본값 5 사용
	poolSize := os.Getenv("CONCURRENCY_POOL_SIZE")
	if poolSize == "" {
		poolSize = "5"
	}
	poolSizeInt, err := strconv.Atoi(poolSize)
	if err != nil {
		return nil, err
	}
	// 더 나은 성능을 위해 사전 할당을 사용하여 풀 설정
	return ants.NewPool(poolSizeInt, ants.WithPreAlloc(true))
}

// registerPoolCleanup 정리를 위해 고루틴 풀을 등록합니다.
// 애플리케이션 종료 시 고루틴 풀의 적절한 정리를 보장합니다.
// 매개변수:
//   - pool: 고루틴 풀
//   - cleaner: 리소스 정리기
func registerPoolCleanup(pool *ants.Pool, cleaner interfaces.ResourceCleaner) {
	cleaner.RegisterWithName("AntsPool", func() error {
		pool.Release()
		return nil
	})
}

// initDocReaderClient 문서 리더 클라이언트를 초기화합니다.
// 문서 리더 서비스와 상호 작용하기 위한 클라이언트를 생성합니다.
// 매개변수:
//   - cfg: 애플리케이션 구성
//
// 반환값:
//   - 구성된 문서 리더 클라이언트
//   - 초기화 실패 시 오류
func initDocReaderClient(cfg *config.Config) (*client.Client, error) {
	// 환경 변수 또는 구성에서 DocReader URL 사용
	docReaderURL := os.Getenv("DOCREADER_ADDR")
	if docReaderURL == "" && cfg.DocReader != nil {
		docReaderURL = cfg.DocReader.Addr
	}
	return client.NewClient(docReaderURL)
}

// initOllamaService Ollama 서비스 클라이언트를 초기화합니다.
// 모델 추론을 위해 Ollama API와 상호 작용하는 클라이언트를 생성합니다.
// 매개변수:
//   - 없음
//
// 반환값:
//   - 구성된 Ollama 서비스 클라이언트
//   - 초기화 실패 시 오류
func initOllamaService() (*ollama.OllamaService, error) {
	// 기존 팩토리 함수에서 Ollama 서비스 가져오기
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

	// 재시도 구성
	maxRetries := 30                 // 최대 재시도 횟수
	retryInterval := 2 * time.Second // 재시도 간격

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
	ctx := context.Background()
	// Excel 지원을 포함하는 spatial 확장 설치 및 로드
	// 참고: DuckDB는 기본적으로 Excel을 지원하지 않으므로 해결 방법이 필요함
	// 옵션 1: spatial 확장 설치 (사용 가능한 경우)
	installSQL := "INSTALL spatial;"
	if _, err := sqlDB.ExecContext(ctx, installSQL); err != nil {
		logger.Warnf(ctx, "[DuckDB] Failed to install spatial extension (may already be installed): %v", err)
	}

	loadSQL := "LOAD spatial;"
	if _, err := sqlDB.ExecContext(ctx, loadSQL); err != nil {
		logger.Warnf(ctx, "[DuckDB] Failed to load spatial extension: %v", err)
	}
	return sqlDB, nil
}
